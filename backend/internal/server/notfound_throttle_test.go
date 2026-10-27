package server

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }

func newTestThrottle(perMin int, block time.Duration) (*nfThrottle, *fakeClock) {
	c := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	th := newNFThrottle(perMin, block)
	th.now = c.now
	return th, c
}

func TestThrottleBlocksAfterBucketAndExpires(t *testing.T) {
	th, clk := newTestThrottle(5, 2*time.Minute)
	for i := 0; i < 5; i++ {
		if th.charge("a") {
			t.Fatalf("blocked after %d charges", i+1)
		}
	}
	if !th.charge("a") || !th.blocked("a") {
		t.Fatal("sixth charge did not block")
	}
	if th.blocked("b") || th.charge("b") {
		t.Error("another key affected")
	}
	clk.t = clk.t.Add(119 * time.Second)
	if !th.blocked("a") {
		t.Error("block ended early")
	}
	clk.t = clk.t.Add(2 * time.Second)
	if th.blocked("a") {
		t.Error("block did not expire")
	}
	if th.charge("a") {
		t.Error("fresh bucket after the block should not block at once")
	}
}

func TestThrottleRefillsSlowly(t *testing.T) {
	th, clk := newTestThrottle(60, time.Minute)
	for i := 0; i < 60; i++ {
		th.charge("a")
	}
	clk.t = clk.t.Add(10 * time.Second) // 10 tokens back
	for i := 0; i < 10; i++ {
		if th.charge("a") {
			t.Fatalf("blocked at refilled charge %d", i+1)
		}
	}
	if !th.charge("a") {
		t.Error("expected block when the refill is used up")
	}
}

func TestThrottleDisabledAtZero(t *testing.T) {
	th, _ := newTestThrottle(0, time.Minute)
	for i := 0; i < 1000; i++ {
		if th.charge("a") {
			t.Fatal("disabled throttle blocked")
		}
	}
	if th.blocked("a") {
		t.Error("disabled throttle reports blocked")
	}
	var nilT *nfThrottle
	if nilT.charge("a") || nilT.blocked("a") {
		t.Error("nil throttle must be a no-op")
	}
}

func TestThrottleSweepsIdleBuckets(t *testing.T) {
	th, clk := newTestThrottle(5, time.Minute)
	th.charge("a")
	clk.t = clk.t.Add(2 * time.Minute)
	th.charge("b") // triggers the sweep
	if _, ok := th.buckets["a"]; ok {
		t.Error("idle bucket kept")
	}
}

func TestProbeCounterLogsNamesOnly(t *testing.T) {
	var buf bytes.Buffer
	p := newProbeCounter(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(p.Stop)
	p.hit("wp-login")
	p.hit("wp-login")
	p.hit("env")
	if !p.flush() {
		t.Fatal("nothing flushed")
	}
	out := buf.String()
	for _, want := range []string{"admin_probe", "name=wp-login", "count=2", "name=env", "count=1"} {
		if !strings.Contains(out, want) {
			t.Errorf("log lacks %q:\n%s", want, out)
		}
	}
	for _, bad := range []string{"/", "?"} {
		if strings.Contains(out, bad) {
			t.Errorf("log contains %q (a path or query?):\n%s", bad, out)
		}
	}
	if p.flush() {
		t.Error("second flush should have nothing")
	}
}

func TestProbeCounterGoroutineExitsWhenIdle(t *testing.T) {
	p := newProbeCounter(slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
	p.interval = 5 * time.Millisecond
	p.hit("env")
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		p.mu.Lock()
		running := p.running
		p.mu.Unlock()
		if !running {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("flush goroutine still running after idle intervals")
}

func TestThrottleCapsTrackedClients(t *testing.T) {
	th, _ := newTestThrottle(2, time.Minute)
	th.maxBuckets = 3
	for _, k := range []string{"a", "b", "c", "d", "e"} {
		th.charge(k)
	}
	if len(th.buckets) != 3 {
		t.Fatalf("tracked %d clients, want the cap of 3", len(th.buckets))
	}
	// An untracked client is never blocked (fail open), a tracked one still is.
	for i := 0; i < 10; i++ {
		if th.charge("e") {
			t.Fatal("an untracked client was blocked")
		}
	}
	for i := 0; i < 3; i++ {
		th.charge("a")
	}
	if !th.blocked("a") {
		t.Error("a tracked client stopped being blocked")
	}
}

func TestThrottleCountsBlocksWithoutScanning(t *testing.T) {
	th, clk := newTestThrottle(1, time.Minute)
	var got []int
	th.onBlock = func(n int) { got = append(got, n) }
	for _, k := range []string{"a", "b"} {
		th.charge(k)
		th.charge(k)
	}
	clk.t = clk.t.Add(2 * time.Minute)
	th.charge("c")
	th.charge("c")
	if len(got) != 3 || got[0] != 1 || got[1] != 2 || got[2] != 1 {
		t.Errorf("blocked counts = %v, want [1 2 1]", got)
	}
	if len(th.blocks) != 1 {
		t.Errorf("block queue length %d, want 1 after expiry", len(th.blocks))
	}
}
