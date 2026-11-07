package wasmhost

import (
	"sync"
	"testing"
	"time"

	extism "github.com/extism/go-sdk"

	"hatamoto.dev/sdk"
)

// With a pool of 4 and one call a minute, the newest-first pool serves every
// call from one instance, so the other three go stale and the sweep closes
// them. A first-in-first-out pool would rotate through all four and never
// shrink.
func TestSteadyTrafficShrinksPoolToOne(t *testing.T) {
	h, clock := clockedHost(t)
	load(t, h, "good", sdk.Limits{MaxInstances: 4})
	p := (*h.reg.Load())["good"]

	// Burst: four concurrent calls hold four instances, all return to the pool.
	var (
		mu      sync.Mutex
		used    []*extism.Plugin
		barrier sync.WaitGroup
		wg      sync.WaitGroup
	)
	barrier.Add(4)
	p.beforeCall = func(inst *extism.Plugin) {
		mu.Lock()
		used = append(used, inst)
		mu.Unlock()
		barrier.Done()
		barrier.Wait()
	}
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

	// Steady traffic: one call per minute, a sweep after each.
	var serving *extism.Plugin
	p.beforeCall = func(inst *extism.Plugin) { serving = inst }
	for range 6 {
		clock.Advance(time.Minute)
		if _, err := call(h, "good", "echo", "x"); err != nil {
			t.Fatal(err)
		}
		h.sweepAll()
	}
	p.beforeCall = nil

	if p.poolLen() != 1 || h.Stats().Evicted != 3 || h.evicted.Load() != 3 || recordCount(p) != 1 {
		t.Fatalf("pool %d, evicted %d, records %d, want 1, 3, 1", p.poolLen(), h.evicted.Load(), recordCount(p))
	}
	open := 0
	for _, inst := range used {
		if !isClosed(inst) {
			open++
			if inst != serving {
				t.Fatal("the survivor is not the instance that served the calls")
			}
		}
	}
	if open != 1 {
		t.Fatalf("%d instances still open, want 1", open)
	}
}

func TestPoolIsLIFO(t *testing.T) {
	h := newHost(t)
	load(t, h, "good", sdk.Limits{MaxInstances: 4})
	p := (*h.reg.Load())["good"]

	a, err := p.newInstance(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	b, err := p.newInstance(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	warm := p.popTop() // load warmed one; start from an empty pool
	defer warm.Close(t.Context())
	p.tryPushPooled(a)
	p.tryPushPooled(b)
	if got := p.popTop(); got != b {
		t.Fatal("first pop is not the instance pushed last")
	}
	if got := p.popTop(); got != a {
		t.Fatal("second pop is not the instance pushed first")
	}
	if p.popTop() != nil {
		t.Fatal("pop on an empty pool returned an instance")
	}
	_ = a.Close(t.Context())
	_ = b.Close(t.Context())

	// Real calls: the instance one call returns is the one the next takes.
	var seen []*extism.Plugin
	p.beforeCall = func(inst *extism.Plugin) { seen = append(seen, inst) }
	for range 2 {
		if _, err := call(h, "good", "echo", "x"); err != nil {
			t.Fatal(err)
		}
	}
	p.beforeCall = nil
	if len(seen) != 2 || seen[0] != seen[1] {
		t.Fatalf("sequential calls used different instances: %v", seen)
	}
}
