package database

import (
	"context"
	"fmt"
	"sync"
	"time"
)

const (
	defaultFlightErrWindow = time.Second
	defaultFlightCap       = 10 * time.Second
)

// Flight shares one in-flight load between concurrent callers and remembers a
// failure for a short window so a broken dependency costs one attempt per
// window instead of one per caller. Successes are not cached: callers keep
// their own TTL cache and use Flight only for the miss path.
//
// Unlike a plain singleflight, the load runs on a context detached from every
// caller (values kept, cancellation dropped, acquire bound removed, Cap
// applied), so one cancelled or short-bounded caller never fails the others;
// each waiter still honours its own context and its own acquire bound.
//
// The zero value is ready to use. A Flight must not be copied after first use.
type Flight[T any] struct {
	// ErrWindow is how long a failed load is shared; 0 means one second.
	ErrWindow time.Duration
	// Cap limits one load; 0 means ten seconds.
	Cap time.Duration

	mu      sync.Mutex
	gen     uint64
	cur     *flightCall[T]
	errAt   time.Time
	lastErr error
	now     func() time.Time
}

type flightCall[T any] struct {
	done chan struct{}
	val  T
	err  error
}

func (f *Flight[T]) clock() time.Time {
	if f.now != nil {
		return f.now()
	}
	return time.Now()
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
	c := f.cur
	if c == nil {
		c = &flightCall[T]{done: make(chan struct{})}
		f.cur = c
		go f.lead(ctx, c, f.gen, load, commit)
	}
	f.mu.Unlock()

	var bound <-chan time.Time
	if d := AcquireBound(ctx); d > 0 {
		t := time.NewTimer(d)
		defer t.Stop()
		bound = t.C
	}
	select {
	case <-c.done:
		return c.val, c.err
	case <-ctx.Done():
		return zero, ctx.Err()
	case <-bound:
		NotePoolBusy(ctx)
		return zero, fmt.Errorf("waiting for shared load: %w", ErrPoolBusy)
	}
}

func (f *Flight[T]) lead(ctx context.Context, c *flightCall[T], gen uint64, load func(context.Context) (T, error), commit func(T)) {
	limit := f.Cap
	if limit <= 0 {
		limit = defaultFlightCap
	}
	lctx, cancel := context.WithTimeout(WithoutAcquireBound(context.WithoutCancel(ctx)), limit)
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

	f.mu.Lock()
	if f.gen == gen {
		f.cur = nil
		if err != nil {
			f.lastErr, f.errAt = err, f.clock()
		} else if commit != nil {
			commit(val)
		}
	}
	f.mu.Unlock()
	c.val, c.err = val, err
	close(c.done)
}
