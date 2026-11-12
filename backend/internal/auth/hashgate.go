package auth

import (
	"context"
	"math"
	"runtime"
	"runtime/debug"
	"slices"
	"sync"
)

// memoryLimit reads the process memory limit; a variable so tests can inject one.
var memoryLimit = func() int64 { return debug.SetMemoryLimit(-1) }

// gateSlots is the hash gate capacity in units of one target-size hash: a
// quarter of the memory limit, between 1 and 4. With no limit it follows the
// CPU count. It is never below 1.
func gateSlots(limit int64, procs int) int {
	if limit <= 0 || limit == math.MaxInt64 {
		return max(1, min(4, procs))
	}
	return max(1, min(4, int(limit/4/(int64(argon2Memory)*1024))))
}

// hashGate bounds argon2 memory in flight. Units are one target-size hash; a
// larger stored hash takes proportionally more. Acquisition is all-or-nothing
// under one mutex and waiters are served first come first served, so they
// cannot deadlock each other or starve.
type hashGate struct {
	mu   sync.Mutex
	cap  int
	used int
	wake chan struct{}
	// queue holds the waiters in arrival order.
	queue []*waiter
}

func newHashGate(capacity int) *hashGate {
	return &hashGate{cap: max(1, capacity), wake: make(chan struct{})}
}

// costOf is the number of gate units a hash of memKiB occupies.
func costOf(memKiB uint32) int {
	return int((memKiB + argon2Memory - 1) / argon2Memory)
}

// waiter is one queued acquire; a pointer to a non-empty struct is unique.
type waiter struct{ cost int }

// acquire waits for cost units (clamped to [1, capacity]) and returns the
// release func. Waiters are served in arrival order, and a new caller queues
// behind any waiter, so a large acquirer is not starved by a stream of small
// ones. It returns ctx's error, taking nothing, when ctx ends first.
func (g *hashGate) acquire(ctx context.Context, cost int) (func(), error) {
	cost = max(1, min(cost, g.cap))
	me := &waiter{cost: cost}
	queued := false
	for {
		g.mu.Lock()
		if (!queued && len(g.queue) == 0 || queued && g.queue[0] == me) && g.used+cost <= g.cap {
			if queued {
				g.queue = g.queue[1:]
				g.broadcastLocked() // the next waiter may fit too
			}
			g.used += cost
			g.mu.Unlock()
			return func() {
				g.mu.Lock()
				g.used -= cost
				g.broadcastLocked()
				g.mu.Unlock()
			}, nil
		}
		if !queued {
			g.queue = append(g.queue, me)
			queued = true
		}
		w := g.wake
		g.mu.Unlock()
		select {
		case <-w:
		case <-ctx.Done():
			g.mu.Lock()
			g.queue = slices.DeleteFunc(g.queue, func(q *waiter) bool { return q == me })
			g.broadcastLocked() // a waiter behind us may now be first
			g.mu.Unlock()
			return nil, ctx.Err()
		}
	}
}

// broadcastLocked wakes every waiter; the caller holds g.mu.
func (g *hashGate) broadcastLocked() {
	close(g.wake)
	g.wake = make(chan struct{})
}

var gate struct {
	once sync.Once
	g    *hashGate
}

// theGate sizes the gate on first use: the memory limit is applied after
// package init, so it cannot be read earlier.
func theGate() *hashGate {
	gate.once.Do(func() {
		gate.g = newHashGate(gateSlots(memoryLimit(), runtime.GOMAXPROCS(0)))
	})
	return gate.g
}

// resetHashGate forgets the sized gate so the next hash re-reads the limit. Tests only.
func resetHashGate() {
	gate.once = sync.Once{}
	gate.g = nil
}
