package installstats

import (
	"fmt"
	"sync"
	"testing"
)

func TestHealthConcurrent(t *testing.T) {
	h := NewHealth()
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 500 {
				h.Panic()
				h.HTTP5xx()
				h.RenderError()
				h.PluginError("a")
			}
		}()
	}
	// Drain while writers run; the totals must still add up.
	var sum Counts
	for range 50 {
		sum = sum.add(h.Drain())
	}
	wg.Wait()
	sum = sum.add(h.Drain())
	if sum.Panics != 4000 || sum.HTTP5xx != 4000 || sum.RenderErrors != 4000 || sum.PluginErrors["a"] != 4000 {
		t.Fatalf("lost counts: %+v", sum)
	}
}

func TestHealthDrainRestore(t *testing.T) {
	h := NewHealth()
	h.Panic()
	h.MigrationFailed(2)
	h.PluginError("p")
	c := h.Drain()
	if c.Panics != 1 || c.MigrationFailed != 2 || c.PluginErrors["p"] != 1 {
		t.Fatalf("drain: %+v", c)
	}
	if again := h.Drain(); !again.IsZero() {
		t.Fatalf("second drain not empty: %+v", again)
	}
	h.HTTP5xx()
	h.Restore(c)
	got := h.Drain()
	if got.Panics != 1 || got.HTTP5xx != 1 || got.MigrationFailed != 2 || got.PluginErrors["p"] != 1 {
		t.Fatalf("restore: %+v", got)
	}
}

func TestHealthPluginCap(t *testing.T) {
	h := NewHealth()
	for i := range maxPluginErrorKeys + 50 {
		h.PluginError(fmt.Sprintf("p%d", i))
	}
	c := h.Drain()
	if len(c.PluginErrors) != maxPluginErrorKeys+1 || c.PluginErrors[otherKey] != 50 {
		t.Fatalf("%d keys, other=%d", len(c.PluginErrors), c.PluginErrors[otherKey])
	}
	h.PluginError("p0") // a known name after the drain still counts under its own key
	if h.Drain().PluginErrors["p0"] != 1 {
		t.Fatal("name lost")
	}
}

func TestHealthNilSafe(t *testing.T) {
	var h *Health
	h.Panic()
	h.HTTP5xx()
	h.RenderError()
	h.MigrationFailed(3)
	h.PluginError("x")
	h.Restore(Counts{Panics: 1})
	if c := h.Drain(); !c.IsZero() {
		t.Fatalf("nil drain: %+v", c)
	}
}
