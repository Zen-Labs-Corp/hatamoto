// Package idle lets background loops back off when an install has served no
// request for a while, so an idle process stops holding a database connection
// open. Idle loops share one aligned "beat": they all check in one burst, and
// the pool can drain in the gaps between bursts. The first request after an
// idle spell wakes every waiting loop at once.
package idle

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

const (
	minBeat = 5 * time.Minute
	maxBeat = 15 * time.Minute
)

// Beat returns the idle beat for an idle threshold: twice the threshold, kept
// within 5 to 15 minutes, so the gap between beats always exceeds the time a
// pooled connection may sit idle before it is closed.
func Beat(after time.Duration) time.Duration {
	return min(max(2*after, minBeat), maxBeat)
}

// Gate tracks request activity and paces the loops that opt in. Build one with
// New. A nil *Gate is valid: Touch is a no-op and Wait is a plain timer.
type Gate struct {
	after, beat time.Duration
	now         func() time.Time
	epoch       time.Time
	last        atomic.Int64 // unix nanos of the latest request

	mu    sync.Mutex
	wake  chan struct{} // closed, then replaced, on each idle->active edge
	timer func(d time.Duration) (<-chan time.Time, func())
}

// New builds a Gate that counts as idle after `after` without a Touch. Idle
// waits align to multiples of `beat` (<= 0: Beat(after)). after <= 0 means
// never idle. now nil means time.Now. The gate starts active.
func New(after, beat time.Duration, now func() time.Time) *Gate {
	if now == nil {
		now = time.Now
	}
	if beat <= 0 {
		beat = Beat(after)
	}
	g := &Gate{after: after, beat: beat, now: now, epoch: now(), wake: make(chan struct{}), timer: realTimer}
	g.last.Store(g.epoch.UnixNano())
	return g
}

func realTimer(d time.Duration) (<-chan time.Time, func()) {
	t := time.NewTimer(d)
	return t.C, func() { t.Stop() }
}

// SetTimer replaces the timer Wait blocks on. For tests; call before use.
func (g *Gate) SetTimer(f func(d time.Duration) (<-chan time.Time, func())) {
	if g != nil {
		g.timer = f
	}
}

// Touch records a request. When the gate was idle it wakes every waiting
// loop, exactly once per idle spell.
func (g *Gate) Touch() {
	if g == nil {
		return
	}
	now := g.now().UnixNano()
	prev := g.last.Swap(now)
	if g.after > 0 && now-prev >= int64(g.after) {
		g.Kick()
	}
}

// Kick wakes every idle waiting loop once, without counting as activity: the
// gate stays idle and each loop checks, then waits again. Call it when work
// was queued in this process that a loop's sampled next-due time cannot know.
func (g *Gate) Kick() {
	if g == nil {
		return
	}
	g.mu.Lock()
	close(g.wake)
	g.wake = make(chan struct{})
	g.mu.Unlock()
}

// Idle reports whether no request has been seen for the idle threshold.
func (g *Gate) Idle() bool {
	if g == nil || g.after <= 0 {
		return false
	}
	return g.now().UnixNano()-g.last.Load() >= int64(g.after)
}

// nextBeat is the first beat boundary strictly after t.
func (g *Gate) nextBeat(t time.Time) time.Time {
	d := t.Sub(g.epoch)
	if d < 0 {
		return g.epoch
	}
	return g.epoch.Add((d/g.beat + 1) * g.beat)
}

// Wait blocks until the loop should check again and reports false when ctx
// ended. While active that is `normal`. While idle it is the next shared beat,
// or the time nextDue reports if that is sooner (but never sooner than
// `normal` from now, so an overdue item cannot spin the loop). A request that
// ends the idle spell cuts the wait short. nextDue may be nil.
func (g *Gate) Wait(ctx context.Context, normal time.Duration, nextDue func(context.Context) (time.Time, bool)) bool {
	return g.WaitPlanned(ctx, normal, nextDue, nil)
}

// WaitPlanned is Wait that also reports the planned wake-up time through
// planned before it blocks (for "next run" displays). planned may be nil.
func (g *Gate) WaitPlanned(ctx context.Context, normal time.Duration, nextDue func(context.Context) (time.Time, bool), planned func(time.Time)) bool {
	if g == nil {
		if planned != nil {
			planned(time.Now().Add(normal))
		}
		return block(ctx, realTimer, normal, nil)
	}
	g.mu.Lock()
	wake := g.wake
	g.mu.Unlock()
	now := g.now()
	d := normal
	if g.Idle() {
		target := g.nextBeat(now)
		if nextDue != nil {
			if due, ok := nextDue(ctx); ok {
				target = minTime(target, maxTime(due, now.Add(normal)))
			}
		}
		d = max(target.Sub(now), 0)
	} else {
		wake = nil // an active loop has nothing to be woken for
	}
	if planned != nil {
		planned(now.Add(d))
	}
	return block(ctx, g.timer, d, wake)
}

func block(ctx context.Context, timer func(time.Duration) (<-chan time.Time, func()), d time.Duration, wake <-chan struct{}) bool {
	c, stop := timer(d)
	defer stop()
	select {
	case <-ctx.Done():
		return false
	case <-wake:
	case <-c:
	}
	return ctx.Err() == nil
}

func minTime(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}
	return a
}

func maxTime(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}
