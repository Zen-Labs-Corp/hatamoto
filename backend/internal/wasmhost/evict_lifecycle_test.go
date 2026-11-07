package wasmhost

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"testing"
	"time"

	"hatamoto.dev/sdk"
)

// waitFor polls cond until it holds or the deadline passes.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestSweeperEvictsOnTick(t *testing.T) {
	h, clock := clockedHost(t)
	h.sweepEvery = 5 * time.Millisecond
	h.idleWindow = time.Minute
	load(t, h, "good", sdk.Limits{MaxInstances: 4})
	p := (*h.reg.Load())["good"]
	fill(t, p, 3)
	if len(p.pool) != 4 {
		t.Fatalf("pool = %d, want 4", len(p.pool))
	}
	clock.Advance(2 * time.Minute)
	waitFor(t, "the sweeper to shrink the pool", func() bool { return len(p.pool) == 1 })
	if got := h.Stats().Evicted; got != 3 {
		t.Fatalf("evicted = %d, want 3", got)
	}
	if recordCount(p) != 1 {
		t.Fatalf("pooledMem has %d entries, want 1", recordCount(p))
	}
}

func TestCloseStopsSweeperNoLeak(t *testing.T) {
	settle := func() int {
		for range 10 {
			runtime.GC()
			time.Sleep(25 * time.Millisecond)
		}
		return runtime.NumGoroutine()
	}
	g0 := settle()
	for range 20 {
		h, err := New(nil, cacheDir)
		if err != nil {
			t.Fatal(err)
		}
		if err := h.load(context.Background(), manifest("cycle", sdk.Limits{}), hostileWasm); err != nil {
			t.Fatal(err)
		}
		h.Close(context.Background())
	}
	if g1 := settle(); g1 > g0+2 {
		t.Fatalf("goroutines %d -> %d after 20 cycles", g0, g1)
	}
}

func TestCloseIdempotent(t *testing.T) {
	h := newHost(t) // its cleanup closes a third time
	load(t, h, "good", sdk.Limits{})
	h.Close(context.Background())
	h.Close(context.Background())
	if n := len(h.Loaded()); n != 0 {
		t.Fatalf("%d plugins loaded after Close", n)
	}
}

func TestUnloadAndReloadDuringSweep(t *testing.T) {
	h, _ := clockedHost(t)
	h.sweepEvery = time.Millisecond
	h.idleWindow = 0 // every pooled instance is always stale
	load(t, h, "good", sdk.Limits{MaxInstances: 4})
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
				}
				if _, err := call(h, "good", "echo", "x"); err != nil && !errors.Is(err, ErrNotLoaded) {
					t.Errorf("call during reload: %v", err)
					return
				}
			}
		})
	}
	for range 5 {
		load(t, h, "good", sdk.Limits{MaxInstances: 4})
		time.Sleep(5 * time.Millisecond)
	}
	if !h.Unload(context.Background(), "good") {
		t.Fatal("unload reported not loaded")
	}
	close(stop)
	wg.Wait()
	// A double close would panic or error inside wazero; every call above
	// went through without a discarded (closed) instance being reported.
	if _, err := call(h, "good", "echo", "x"); !errors.Is(err, ErrNotLoaded) {
		t.Fatalf("after unload: %v", err)
	}
}

func TestLoadAfterCloseStartsNoSweeper(t *testing.T) {
	h, err := New(nil, cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	h.Close(context.Background())
	if err := h.load(context.Background(), manifest("late", sdk.Limits{}), hostileWasm); err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	started := h.started
	h.mu.Unlock()
	if started {
		t.Fatal("sweeper started after Close")
	}
	h.Unload(context.Background(), "late")
}
