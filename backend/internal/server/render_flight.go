package server

import (
	"context"
	"sync"
	"time"
)

// defaultFlightWait bounds how long a follower parks behind a leader; a
// leader stalled past it no longer holds the page hostage.
const defaultFlightWait = 5 * time.Second

// renderFlight lets concurrent misses on one page key share one render. Only a
// result the leader stored is shared, so a follower gets exactly what a cache
// hit would give; anything else sends followers to render on their own.
// The zero value is ready to use.
type renderFlight struct {
	mu    sync.Mutex
	calls map[string]*flightCall
	// maxWait overrides defaultFlightWait when positive (tests).
	maxWait time.Duration
}

type flightCall struct {
	done    chan struct{}
	once    sync.Once
	res     flightResult
	waiters int // guarded by renderFlight.mu
}

type flightResult struct {
	body   []byte
	stored bool
}

// join reports whether the caller leads the render of key; a follower must
// wait on the returned call.
func (f *renderFlight) join(key string) (c *flightCall, leader bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if c, ok := f.calls[key]; ok {
		c.waiters++
		return c, false
	}
	if f.calls == nil {
		f.calls = make(map[string]*flightCall)
	}
	c = &flightCall{done: make(chan struct{})}
	f.calls[key] = c
	return c, true
}

// finish publishes the leader's result and releases the followers. Only the
// first call per flight counts, so a deferred finish after an explicit one is
// a no-op.
func (f *renderFlight) finish(key string, c *flightCall, res flightResult) {
	c.once.Do(func() {
		f.mu.Lock()
		if f.calls[key] == c {
			delete(f.calls, key)
		}
		f.mu.Unlock()
		c.res = res
		close(c.done)
	})
}

// wait blocks until the leader finishes, for at most the flight's bound; a
// timeout gives an empty result, so the follower renders on its own. ok is
// false when ctx ends first.
func (f *renderFlight) wait(ctx context.Context, c *flightCall) (res flightResult, ok bool) {
	limit := f.maxWait
	if limit <= 0 {
		limit = defaultFlightWait
	}
	timer := time.NewTimer(limit)
	defer timer.Stop()
	select {
	case <-c.done:
		return c.res, true
	case <-timer.C:
		return flightResult{}, true
	case <-ctx.Done():
		return flightResult{}, false
	}
}

// waiting is the number of followers parked on key (tests).
func (f *renderFlight) waiting(key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	if c, ok := f.calls[key]; ok {
		return c.waiters
	}
	return 0
}
