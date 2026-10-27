package builder

import (
	"strings"
	"sync"
	"testing"
)

func TestNoCoreKindHasUnderscore(t *testing.T) {
	for name := range reg.Load().kinds {
		if strings.Contains(name, "_") {
			t.Errorf("core kind %q contains \"_\", reserved for plugin widgets", name)
		}
	}
}

func TestLoadWidgetsRefusesUnderscore(t *testing.T) {
	_, err := loadWidgets(mapWith("my_kind", tabsSchema), nativeKinds)
	if err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("err = %v, want reserved", err)
	}
}

// Readers must be safe while snapshots are swapped (run with -race).
func TestRegistrySwapRace(t *testing.T) {
	base := reg.Load()
	t.Cleanup(func() { reg.Store(base) })
	doc := &Doc{V: 2, Nodes: []*Node{{ID: "a", T: "heading", P: map[string]any{"text": "x", "level": 2}}}}
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if len(Kinds()) == 0 {
					t.Error("empty Kinds")
				}
				if err := doc.Validate(); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	for i := 0; i < 50; i++ {
		reg.Store(newRegistry(base.kinds))
	}
	close(stop)
	wg.Wait()
}

// A pack kind appearing and vanishing while a document that uses it (with
// bindings) is validated must never panic or fail: the call reads one snapshot.
func TestRegistryPackSwapDuringValidate(t *testing.T) {
	w := packOf(t, "promo", "banner", promoSchema)
	t.Cleanup(func() { SetPackKinds(nil) })
	doc := &Doc{V: 2, Nodes: []*Node{
		{ID: "p", T: "promo_banner", P: map[string]any{"title": "x"}, B: map[string]string{"link": "entity.url"},
			C: []*Node{{ID: "h", T: "heading", P: map[string]any{"text": "y", "level": 2}}}},
		{ID: "q", T: "promo_banner", P: map[string]any{"title": "z"}},
	}}
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if err := doc.Validate(); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	for i := 0; i < 300; i++ {
		if i%2 == 0 {
			_ = SetPackKinds([]PackWidgetSchema{w})
		} else {
			_ = SetPackKinds(nil)
		}
	}
	close(stop)
	wg.Wait()
}
