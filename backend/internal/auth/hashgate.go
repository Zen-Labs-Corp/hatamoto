package auth

import (
	"context"
	"math"
	"runtime"
	"runtime/debug"
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
// under one mutex, so waiters cannot deadlock each other.
type hashGate struct {
	mu   sync.Mutex
	cap  int
	used int
	wake chan struct{}
}

func newHashGate(capacity int) *hashGate {
	return &hashGate{cap: max(1, capacity), wake: make(chan struct{})}
}

// costOf is the number of gate units a hash of memKiB occupies.
func costOf(memKiB uint32) int {
	return int((memKiB + argon2Memory - 1) / argon2Memory)
}

// acquire waits for cost units (clamped to [1, capacity]) and returns the
// release func. It returns ctx's error, taking nothing, when ctx ends first.
func (g *hashGate) acquire(ctx context.Context, cost int) (func(), error) {
	cost = max(1, min(cost, g.cap))
	for {
		g.mu.Lock()
		if g.used+cost <= g.cap {
			g.used += cost
			g.mu.Unlock()
			return func() {
				g.mu.Lock()
				g.used -= cost
				close(g.wake)
				g.wake = make(chan struct{})
				g.mu.Unlock()
			}, nil
		}
		w := g.wake
		g.mu.Unlock()
		select {
		case <-w:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
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
