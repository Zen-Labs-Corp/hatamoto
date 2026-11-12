package auth

import (
	"context"
	"errors"
	"testing"
	"time"
)

const miB = int64(1) << 20

// useLimit sizes the gate from an injected memory limit for one test.
func useLimit(t *testing.T, limit int64) *hashGate {
	t.Helper()
	old := memoryLimit
	memoryLimit = func() int64 { return limit }
	resetHashGate()
	t.Cleanup(func() { memoryLimit = old; resetHashGate() })
	return theGate()
}

func TestGateSlots(t *testing.T) {
	const noLimit = int64(1<<63 - 1)
	tests := []struct {
		name  string
		limit int64
		procs int
		want  int
	}{
		{"128 MiB", 128 * miB, 8, 1},
		{"256 MiB", 256 * miB, 8, 3},
		{"512 MiB", 512 * miB, 8, 4},
		{"1 GiB", 1024 * miB, 8, 4},
		{"tiny", 1 * miB, 8, 1},
		{"no limit 1 cpu", noLimit, 1, 1},
		{"no limit 2 cpu", noLimit, 2, 2},
		{"no limit 8 cpu", noLimit, 8, 4},
		{"zero", 0, 8, 4},
		{"zero procs", noLimit, 0, 1},
	}
	for _, tt := range tests {
		if got := gateSlots(tt.limit, tt.procs); got != tt.want {
			t.Errorf("%s: gateSlots = %d, want %d", tt.name, got, tt.want)
		}
	}
}

func TestCostOf(t *testing.T) {
	if got := costOf(argon2Memory); got != 1 {
		t.Errorf("target cost = %d", got)
	}
	if got := costOf(64 * 1024); got != 4 {
		t.Errorf("legacy 64 MiB cost = %d, want 4", got)
	}
}

func TestHashPassword_BoundedConcurrency(t *testing.T) {
	g := useLimit(t, 512*miB) // 4 slots
	var rel []func()
	for range g.cap {
		r, err := g.acquire(context.Background(), 1)
		if err != nil {
			t.Fatal(err)
		}
		rel = append(rel, r)
	}
	done := make(chan struct{})
	go func() {
		if _, err := HashPassword("correct horse"); err != nil {
			t.Error(err)
		}
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("hash N+1 ran while all slots were busy")
	case <-time.After(150 * time.Millisecond):
	}
	rel[0]() // one finishes: the waiting hash may proceed
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("waiting hash never ran after a slot freed")
	}
	for _, r := range rel[1:] {
		r()
	}
}

// A legacy 64 MiB verify takes the whole gate; cost never exceeds capacity.
func TestHashGate_LegacyCostExcludesOthers(t *testing.T) {
	g := useLimit(t, 512*miB)
	rel, err := g.acquire(context.Background(), costOf(64*1024))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := g.acquire(ctx, 1); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("acquire during legacy verify = %v, want deadline", err)
	}
	rel()

	small := useLimit(t, 128*miB) // capacity 1: cost 4 clamps to 1, no deadlock
	rel, err = small.acquire(context.Background(), 4)
	if err != nil {
		t.Fatal(err)
	}
	rel()
}

func BenchmarkHashPassword(b *testing.B) {
	for b.Loop() {
		if _, err := HashPassword("correct horse"); err != nil {
			b.Fatal(err)
		}
	}
}

// A waiter whose request context is cancelled returns promptly and never hashes.
func TestHashPassword_CancelledWaiterReturnsPromptly(t *testing.T) {
	g := useLimit(t, 512*miB)
	enc, err := HashPassword("correct horse") // before the slots are taken
	if err != nil {
		t.Fatal(err)
	}
	var rel []func()
	for range g.cap {
		r, err := g.acquire(context.Background(), 1)
		if err != nil {
			t.Fatal(err)
		}
		rel = append(rel, r)
	}
	ctx, cancel := context.WithCancel(context.Background())
	type result struct {
		ok  bool
		err error
	}
	verify := make(chan result, 1)
	hash := make(chan error, 1)
	go func() {
		ok, err := VerifyPasswordContext(ctx, "correct horse", enc)
		verify <- result{ok, err}
	}()
	go func() {
		_, err := HashPasswordContext(ctx, "correct horse")
		hash <- err
	}()
	select {
	case <-verify:
		t.Fatal("verify returned while it should still be queued")
	case <-time.After(100 * time.Millisecond):
	}
	cancel()
	select {
	case r := <-verify:
		if r.ok || !errors.Is(r.err, context.Canceled) {
			t.Errorf("verify = %v, %v", r.ok, r.err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled verify still waiting")
	}
	select {
	case err := <-hash:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("hash err = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled hash still waiting")
	}
	// Already-cancelled context never hashes even when a slot is free.
	rel[0]()
	if _, err := HashPasswordContext(ctx, "x"); !errors.Is(err, context.Canceled) {
		t.Errorf("pre-cancelled hash err = %v", err)
	}
	for _, r := range rel[1:] {
		r()
	}
}

// While a large acquirer waits, a later small one queues behind it instead of
// slipping into the free units.
func TestHashGate_LargeWaiterIsNotStarved(t *testing.T) {
	g := useLimit(t, 512*miB) // 4 slots
	hold, err := g.acquire(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	big := make(chan func(), 1)
	go func() {
		rel, err := g.acquire(context.Background(), 4)
		if err != nil {
			t.Error(err)
			return
		}
		big <- rel
	}()
	time.Sleep(100 * time.Millisecond) // the big one is queued

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if rel, err := g.acquire(ctx, 1); err == nil {
		rel()
		t.Fatal("a small acquirer jumped the queue of a waiting large one")
	}
	hold()
	select {
	case rel := <-big:
		rel()
	case <-time.After(5 * time.Second):
		t.Fatal("the large waiter never got the gate")
	}
	// A cancelled head of the queue lets the next waiter through.
	hold, err = g.acquire(context.Background(), 4)
	if err != nil {
		t.Fatal(err)
	}
	cctx, ccancel := context.WithCancel(context.Background())
	go func() { _, _ = g.acquire(cctx, 4) }()
	time.Sleep(50 * time.Millisecond)
	next := make(chan func(), 1)
	go func() {
		rel, _ := g.acquire(context.Background(), 1)
		next <- rel
	}()
	time.Sleep(50 * time.Millisecond)
	ccancel()
	hold()
	select {
	case rel := <-next:
		rel()
	case <-time.After(5 * time.Second):
		t.Fatal("waiter behind a cancelled one never got the gate")
	}
}
