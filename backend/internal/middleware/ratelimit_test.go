package middleware

import (
	"runtime"
	"testing"
	"time"
)

// rlClock is a manually advanced clock for limiter tests.
type rlClock struct{ t time.Time }

func (c *rlClock) now() time.Time          { return c.t }
func (c *rlClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func TestRateLimiterStartsNoGoroutine(t *testing.T) {
	g0 := runtime.NumGoroutine()
	rls := make([]*RateLimiter, 50)
	for i := range rls {
		rls[i] = NewRateLimiter(1, 1)
	}
	g1 := runtime.NumGoroutine()
	if g1-g0 >= 10 {
		t.Fatalf("50 limiters started %d goroutines", g1-g0)
	}
	runtime.KeepAlive(rls)
}

func TestRateLimiterSweepsStaleVisitorsLazily(t *testing.T) {
	clk := &rlClock{t: time.Unix(1_700_000_000, 0)}
	rl := NewRateLimiter(1, 1)
	rl.now = clk.now
	rl.lastSweep = clk.t

	rl.Allow("a")
	clk.advance(4 * time.Minute)
	rl.Allow("b")
	if _, ok := rl.visitors["a"]; ok || len(rl.visitors) != 1 {
		t.Fatalf("stale visitor not swept: %d entries", len(rl.visitors))
	}

	// Within sweepEvery nothing is swept, and a used key is never dropped.
	rl.visitors["c"] = &visitor{lastSeen: clk.t.Add(-time.Hour)}
	clk.advance(30 * time.Second)
	rl.Allow("b")
	if len(rl.visitors) != 2 {
		t.Fatalf("swept again within sweepEvery: %d entries", len(rl.visitors))
	}
}

func TestRateLimiterStopIsSafe(t *testing.T) {
	rl := NewRateLimiter(10, 10)
	rl.Stop()
	rl.Stop()
	if !rl.Allow("x") {
		t.Fatal("Allow refused after Stop")
	}
}

func TestRateLimiterStillLimits(t *testing.T) {
	rl := NewRateLimiter(1, 2)
	if !rl.Allow("x") || !rl.Allow("x") {
		t.Fatal("burst of 2 must pass")
	}
	if rl.Allow("x") {
		t.Fatal("third immediate request must be refused")
	}
}
