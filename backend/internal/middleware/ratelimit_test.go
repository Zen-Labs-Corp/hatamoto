package middleware

import "testing"

func TestRateLimiterVisitorCap(t *testing.T) {
	t.Parallel()
	rl := NewRateLimiter(0.001, 1)
	defer rl.Stop()
	rl.maxVisitors = 3
	for _, k := range []string{"k1", "k2", "k3"} {
		if !rl.Allow(k) {
			t.Fatalf("%s: first request refused", k)
		}
	}
	if !rl.Allow("k4") {
		t.Fatal("k4: first request on the shared limiter refused")
	}
	if rl.Allow("k5") {
		t.Fatal("k5 shares the overflow limiter, which is spent: want refused")
	}
	if rl.Allow("k1") {
		t.Fatal("k1: second request allowed, a tracked key keeps its own bucket")
	}
	if n := len(rl.visitors); n != 3 {
		t.Fatalf("visitors = %d, want 3", n)
	}
}
