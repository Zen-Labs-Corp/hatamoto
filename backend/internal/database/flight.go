package database

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultFlightErrWindow = time.Second
	defaultFlightCap       = 10 * time.Second
	// defaultFlightHardCap is the safety net of a load started by an unbounded
	// caller (admin, API, gate): generous, but a stuck query cannot hang every
	// caller that joins it forever.
	defaultFlightHardCap = 30 * time.Second
)

// Flight shares one in-flight load between concurrent callers and remembers a
// failure for a short window so a broken dependency costs one attempt per
// window instead of one per caller. Successes are not cached: callers keep
// their own TTL cache and use Flight only for the miss path.
//
// Unlike a plain singleflight, the load runs on a context detached from every
// caller (values kept, cancellation dropped, acquire bound removed, Cap
// applied to every load, shorter for a bounded starter), so one cancelled or
// short-bounded caller never fails the others; each waiter still honours its
// own context and its own acquire bound. A load that hit Cap is a limit of the
// bounded (public) callers, not a failure of the dependency: it is not
// remembered, and an unbounded caller that shared it runs its own load.
//
// The zero value is ready to use. A Flight must not be copied after first use.
type Flight[T any] struct {
	// ErrWindow is how long a failed load is shared; 0 means one second.
	ErrWindow time.Duration
	// Cap limits a load started by a bounded caller; 0 means ten seconds.
	Cap time.Duration
	// HardCap limits a load started by an unbounded caller (admin, API, gate);
	// 0 means thirty seconds.
	HardCap time.Duration

	mu      sync.Mutex
	gen     uint64
	cur     *flightCall[T]
	errAt   time.Time
	lastErr error
	now     func() time.Time
	waiting atomic.Int32
}

type flightCall[T any] struct {
	done    chan struct{}
	val     T
	err     error
	expired bool // the load ran out of its cap
	bounded bool // a bounded caller started the load, so expired is not a failure of the load itself
}

func (f *Flight[T]) clock() time.Time {
	if f.now != nil {
		return f.now()
	}
	return time.Now()
}

// WaitForWaiters blocks until n callers are waiting on a load (the one that
// started it included) or timeout passes, and reports whether n was reached. It
// lets tests release a blocked loader only once every caller has joined,
// without guessing with a sleep.
func (f *Flight[T]) WaitForWaiters(n int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for int(f.waiting.Load()) < n {
		if time.Now().After(deadline) {
			return false
		}
		runtime.Gosched()
	}
	return true
}

// Forget invalidates the flight: the shared error is dropped and a load
// already running is detached, so its result is neither committed nor handed
// to later callers. The next Do starts a fresh load.
func (f *Flight[T]) Forget() {
	f.mu.Lock()
	f.gen++
	f.cur = nil
	f.lastErr = nil
	f.mu.Unlock()
}

// Do runs load once for all concurrent callers.
func (f *Flight[T]) Do(ctx context.Context, load func(context.Context) (T, error)) (T, error) {
	return f.DoCommit(ctx, load, nil)
}

// DoCommit is Do plus commit, called once with a successful result while the
// flight lock is held and only if Forget was not called since the load began.
// commit must be quick and must not call back into the Flight.
func (f *Flight[T]) DoCommit(ctx context.Context, load func(context.Context) (T, error), commit func(T)) (T, error) {
	var zero T
	f.mu.Lock()
	window := f.ErrWindow
	if window <= 0 {
		window = defaultFlightErrWindow
	}
	if f.lastErr != nil {
		if f.clock().Sub(f.errAt) < window {
			err := f.lastErr
			f.mu.Unlock()
			return zero, err
		}
		f.lastErr = nil
	}
	gen := f.gen
	c := f.cur
	if c == nil {
		c = &flightCall[T]{done: make(chan struct{})}
		f.cur = c
		go f.lead(ctx, c, gen, load, commit)
	}
	f.waiting.Add(1)
	defer f.waiting.Add(-1)
	f.mu.Unlock()

	var bound <-chan time.Time
	d := AcquireBound(ctx)
	if d > 0 {
		t := time.NewTimer(d)
		defer t.Stop()
		bound = t.C
	}
	select {
	case <-c.done:
		if c.expired && d > 0 {
			// The cap is a limit of the bounded callers: this one gets the
			// same answer as if its own bound had run out.
			NotePoolBusy(ctx)
			return zero, fmt.Errorf("waiting for shared load: %w", ErrPoolBusy)
		}
		if c.expired && c.bounded {
			// The shared load ran out of the cap of a bounded caller; this
			// caller never agreed to that limit, so it loads for itself.
			return f.own(ctx, gen, load, commit)
		}
		return c.val, c.err
	case <-ctx.Done():
		return zero, ctx.Err()
	case <-bound:
		NotePoolBusy(ctx)
		return zero, fmt.Errorf("waiting for shared load: %w", ErrPoolBusy)
	}
}

// own runs load for one caller on its own context and commits a success unless
// Forget was called since gen was read.
func (f *Flight[T]) own(ctx context.Context, gen uint64, load func(context.Context) (T, error), commit func(T)) (T, error) {
	val, err := load(ctx)
	if err == nil && commit != nil {
		func() {
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.gen == gen {
				commit(val)
			}
		}()
	}
	return val, err
}

func (f *Flight[T]) lead(ctx context.Context, c *flightCall[T], gen uint64, load func(context.Context) (T, error), commit func(T)) {
	lctx := WithoutAcquireBound(context.WithoutCancel(ctx))
	bounded := AcquireBound(ctx) > 0
	limit := f.HardCap
	if limit <= 0 {
		limit = defaultFlightHardCap
	}
	if bounded {
		limit = f.Cap
		if limit <= 0 {
			limit = defaultFlightCap
		}
	}
	lctx, cancel := context.WithTimeout(lctx, limit)
	defer cancel()

	var val T
	var err error
	func() {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("shared load panicked: %v", r)
			}
		}()
		val, err = load(lctx)
	}()

	expired := err != nil && errors.Is(lctx.Err(), context.DeadlineExceeded)
	func() {
		f.mu.Lock()
		defer f.mu.Unlock() // also when commit panics
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("shared load commit panicked: %v", r)
			}
		}()
		if f.gen != gen {
			return
		}
		f.cur = nil
		if err == nil {
			if commit != nil {
				commit(val)
			}
		} else if !(expired && bounded) { // a bounded cap expiry is not a failure of the dependency
			f.lastErr, f.errAt = err, f.clock()
		}
	}()
	c.val, c.err, c.expired, c.bounded = val, err, expired, bounded
	close(c.done)
}
