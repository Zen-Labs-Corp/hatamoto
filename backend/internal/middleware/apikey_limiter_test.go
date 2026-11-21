package middleware

import (
	"runtime"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestAllowClampsNonPositive(t *testing.T) {
	rl := NewAPIKeyRateLimiter()
	defer rl.Stop()

	for _, limit := range []int{0, -3} {
		if !rl.Allow(uuid.New(), limit) {
			t.Fatalf("limit %d: first request must be allowed (clamped to default)", limit)
		}
	}
}

// keyClock is a manually advanced clock for API key limiter tests.
type keyClock struct{ t time.Time }

func (c *keyClock) now() time.Time          { return c.t }
func (c *keyClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func TestAPIKeyLimiterStartsNoGoroutine(t *testing.T) {
	g0 := runtime.NumGoroutine()
	rls := make([]*APIKeyRateLimiter, 50)
	for i := range rls {
		rls[i] = NewAPIKeyRateLimiter()
	}
	if d := runtime.NumGoroutine() - g0; d >= 10 {
		t.Fatalf("50 limiters started %d goroutines", d)
	}
	runtime.KeepAlive(rls)
}

func TestAPIKeyLimiterSweepsLazily(t *testing.T) {
	clk := &keyClock{t: time.Unix(1_700_000_000, 0)}
	rl := NewAPIKeyRateLimiter()
	rl.now = clk.now
	rl.lastSweep = clk.t
	k1, k2 := uuid.New(), uuid.New()

	rl.Allow(k1, 100)
	clk.advance(6 * time.Minute)
	rl.Allow(k2, 100)
	if _, ok := rl.limiters[k1]; ok || len(rl.limiters) != 1 {
		t.Fatalf("stale limiter not swept: %d entries", len(rl.limiters))
	}

	rl.limiters[k1] = &keyLimiter{lastSeen: clk.t.Add(-time.Hour)}
	clk.advance(30 * time.Second)
	rl.Allow(k2, 100)
	if len(rl.limiters) != 2 {
		t.Fatalf("swept again within sweepEvery: %d entries", len(rl.limiters))
	}
}

func TestAPIKeyLimiterBurstKept(t *testing.T) {
	clk := &keyClock{t: time.Unix(1_700_000_000, 0)}
	rl := NewAPIKeyRateLimiter()
	rl.now = clk.now
	k := uuid.New()
	// Limit 100/h: burst max(100/10, 10) = 10, refill is far slower than the loop.
	passed := 0
	for range 11 {
		if rl.Allow(k, 100) {
			passed++
		}
	}
	if passed != 10 {
		t.Fatalf("%d of 11 immediate requests passed, want 10", passed)
	}
}

func TestAPIKeyLimiterTimerFreesFloodWithoutRequests(t *testing.T) {
	rl := NewAPIKeyRateLimiter()
	rl.cleanup, rl.sweepEvery = 10*time.Millisecond, 10*time.Millisecond
	for range 100 {
		rl.Allow(uuid.New(), 100)
	}
	waitFor(t, "stale limiters freed", func() bool {
		rl.mu.Lock()
		defer rl.mu.Unlock()
		return len(rl.limiters) == 0 && !rl.timer.armed
	})
}

func TestAPIKeyLimiterNoTimerWhileEmptyAndStop(t *testing.T) {
	rl := NewAPIKeyRateLimiter()
	if rl.timer.armed || rl.timer.t != nil {
		t.Fatal("timer armed on an empty limiter")
	}
	rl.Allow(uuid.New(), 100)
	if !rl.timer.armed {
		t.Fatal("timer not armed with a limiter")
	}
	rl.Stop()
	rl.Allow(uuid.New(), 100)
	if rl.timer.armed {
		t.Fatal("timer armed after Stop")
	}
}

func TestAPIKeyLimiterZeroValueClock(t *testing.T) {
	rl := &APIKeyRateLimiter{limiters: map[uuid.UUID]*keyLimiter{}}
	if !rl.Allow(uuid.New(), 100) {
		t.Fatal("zero-value limiter refused")
	}
	rl.Stop()
}
