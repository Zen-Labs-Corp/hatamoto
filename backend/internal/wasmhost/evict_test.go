package wasmhost

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	extism "github.com/extism/go-sdk"

	"hatamoto.dev/sdk"
)

// fakeClock is a settable clock for the idle tests.
type fakeClock struct{ ns atomic.Int64 }

func newFakeClock() *fakeClock {
	c := &fakeClock{}
	c.ns.Store(time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC).UnixNano())
	return c
}
func (c *fakeClock) Now() time.Time          { return time.Unix(0, c.ns.Load()).UTC() }
func (c *fakeClock) Advance(d time.Duration) { c.ns.Add(int64(d)) }

// clockedHost is newHost with the fake clock installed before any load.
func clockedHost(t *testing.T) (*Host, *fakeClock) {
	t.Helper()
	h := newHost(t)
	c := newFakeClock()
	h.now = c.Now
	return h, c
}

func recordCount(p *plugin) int {
	n := 0
	p.pooledMem.Range(func(_, _ any) bool { n++; return true })
	return n
}

// isClosed reports whether inst's module is closed, the way a call finds out.
func isClosed(inst *extism.Plugin) bool {
	_, _, err := inst.CallWithContext(context.Background(), "echo", []byte("x"))
	return isClosedErr(err)
}

// fill pools n instances in addition to what is there, stamping each with the
// current fake time.
func fill(t *testing.T, p *plugin, n int) []*extism.Plugin {
	t.Helper()
	var out []*extism.Plugin
	for range n {
		inst, err := p.newInstance(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		p.tryPushPooled(inst)
		out = append(out, inst)
	}
	return out
}

func TestSweepShrinksIdlePoolToOne(t *testing.T) {
	h, clock := clockedHost(t)
	load(t, h, "good", sdk.Limits{MaxInstances: 4})
	p := (*h.reg.Load())["good"]

	// A burst of 4 concurrent calls fills the pool with 4 instances.
	var mu sync.Mutex
	var used []*extism.Plugin
	var barrier sync.WaitGroup
	barrier.Add(4)
	p.beforeCall = func(inst *extism.Plugin) {
		mu.Lock()
		used = append(used, inst)
		mu.Unlock()
		barrier.Done()
		barrier.Wait()
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			if _, err := call(h, "good", "echo", "x"); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	p.beforeCall = nil
	if p.poolLen() != 4 || len(used) != 4 {
		t.Fatalf("burst left pool %d, used %d, want 4 and 4", p.poolLen(), len(used))
	}

	clock.Advance(IdleWindow)
	closed := p.sweepIdle(h.now(), h.idleWindow)
	h.evicted.Add(int64(closed))
	if closed != 3 || p.poolLen() != 1 || h.evicted.Load() != 3 {
		t.Fatalf("closed %d, pool %d, evicted %d, want 3, 1, 3", closed, p.poolLen(), h.evicted.Load())
	}
	if n := recordCount(p); n != 1 {
		t.Fatalf("pooledMem has %d entries, want 1", n)
	}
	open := 0
	for _, inst := range used {
		if !isClosed(inst) {
			open++
		}
	}
	if open != 1 {
		t.Fatalf("%d instances still open, want 1", open)
	}
	if _, err := call(h, "good", "echo", "x"); err != nil {
		t.Fatalf("call after sweep: %v", err)
	}
}

func TestSweepKeepsRecentlyUsed(t *testing.T) {
	h, clock := clockedHost(t)
	load(t, h, "good", sdk.Limits{MaxInstances: 4})
	p := (*h.reg.Load())["good"]
	// Pool, oldest first: loaded at t0, then t0+2m, t0+4m, t0+6m.
	clock.Advance(2 * time.Minute)
	a := fill(t, p, 1)
	clock.Advance(2 * time.Minute)
	b := fill(t, p, 1)
	clock.Advance(2 * time.Minute)
	c := fill(t, p, 1)

	// now = t0+6m: only the first is older than 5 min.
	if n := p.sweepIdle(h.now(), IdleWindow); n != 1 {
		t.Fatalf("closed %d, want 1 (the t0 instance)", n)
	}
	if p.poolLen() != 3 || isClosed(a[0]) {
		t.Fatalf("pool %d, t0+2m instance closed %v", p.poolLen(), isClosed(a[0]))
	}
	// 4 min later everything is stale, but the newest must survive.
	clock.Advance(4 * time.Minute)
	if n := p.sweepIdle(h.now(), IdleWindow); n != 2 {
		t.Fatalf("closed %d, want 2", n)
	}
	if p.poolLen() != 1 || !isClosed(a[0]) || !isClosed(b[0]) || isClosed(c[0]) {
		t.Fatalf("survivor is not the most recently returned instance (pool %d)", p.poolLen())
	}
	if recordCount(p) != 1 {
		t.Fatalf("pooledMem has %d entries, want 1", recordCount(p))
	}
}

func TestSweepNeverClosesBelowOne(t *testing.T) {
	h, clock := clockedHost(t)
	load(t, h, "good", sdk.Limits{MaxInstances: 2})
	p := (*h.reg.Load())["good"]
	clock.Advance(time.Hour)
	if n := p.sweepIdle(h.now(), IdleWindow); n != 0 || p.poolLen() != 1 {
		t.Fatalf("pool of 1: closed %d, pool %d", n, p.poolLen())
	}
	// Empty pool (the instance is out on a call): nothing happens, nothing starts.
	inst := p.popTop()
	if n := p.sweepIdle(h.now(), IdleWindow); n != 0 || p.poolLen() != 0 || recordCount(p) != 0 {
		t.Fatalf("empty pool: closed %d, pool %d, records %d", n, p.poolLen(), recordCount(p))
	}
	p.release(inst)
}

func TestSweepDuringConcurrentCalls(t *testing.T) {
	h, _ := clockedHost(t)
	load(t, h, "good", sdk.Limits{MaxInstances: 4})
	p := (*h.reg.Load())["good"]
	stop := make(chan struct{})
	var swept sync.WaitGroup
	swept.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			h.evicted.Add(int64(p.sweepIdle(h.now(), 0)))
			if len(p.slots) > cap(p.slots) {
				t.Error("slots exceed capacity")
			}
		}
	})
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 25 {
				if _, err := call(h, "good", "echo", "x"); err != nil {
					t.Error(err)
					return
				}
			}
		})
	}
	wg.Wait()
	close(stop)
	swept.Wait()
	if d := p.discarded.Load(); d != 0 {
		t.Fatalf("discarded %d instances: a sweep closed one in use", d)
	}
	if p.poolLen() < 1 || recordCount(p) != p.poolLen() {
		t.Fatalf("pool %d, records %d", p.poolLen(), recordCount(p))
	}
}

func TestSweepSkipsClosedPlugin(t *testing.T) {
	h, clock := clockedHost(t)
	load(t, h, "good", sdk.Limits{MaxInstances: 3})
	p := (*h.reg.Load())["good"]
	fill(t, p, 2)
	h.Unload(context.Background(), "good")
	clock.Advance(time.Hour)
	if n := p.sweepIdle(h.now(), IdleWindow); n != 0 {
		t.Fatalf("sweep of a closed plugin closed %d", n)
	}
}

func TestStatsReportsEvictedAndWindow(t *testing.T) {
	h := newHost(t)
	if st := h.Stats(); st.Evicted != 0 || st.IdleWindow != IdleWindow {
		t.Fatalf("stats = %+v", st)
	}
	h.evicted.Add(7)
	if st := h.Stats(); st.Evicted != 7 {
		t.Fatalf("evicted = %d, want 7", st.Evicted)
	}
}
