package builder

import (
	"encoding/json"
	"testing"
)

// p.extra must not carry section keys: they would shadow the page_sections
// mirror's own (isVisible, title) and flip the page on the next v1 save.
func TestExtraRejectsReservedSectionKeys(t *testing.T) {
	for _, k := range []string{"id", "type", "title", "content", "isVisible", "sort_order"} {
		doc := `{"v":2,"nodes":[{"id":"a","t":"component","p":{"type":"x","extra":{"` + k + `":1}}}]}`
		if err := ValidateRaw([]byte(doc)); err == nil {
			t.Errorf("extra.%s accepted", k)
		}
	}
	ok := `{"v":2,"nodes":[{"id":"a","t":"component","p":{"type":"x","extra":{"anchor":"top"}}}]}`
	if err := ValidateRaw([]byte(ok)); err != nil {
		t.Errorf("plain extra rejected: %v", err)
	}
}

func TestFromSectionsStripsReservedExtraKeys(t *testing.T) {
	d := FromSections([]Section{{ID: "a", Type: "x", Extra: map[string]any{"isVisible": false, "title": "GHOST", "anchor": "top"}}})
	comps := Components(d)
	if len(comps) != 1 {
		t.Fatalf("components = %d", len(comps))
	}
	extra, _ := comps[0].P["extra"].(map[string]any)
	if len(extra) != 1 || extra["anchor"] != "top" {
		t.Errorf("extra = %v, want only anchor", extra)
	}
	raw, _ := json.Marshal(d)
	if err := ValidateRaw(raw); err != nil {
		t.Errorf("doc invalid: %v", err)
	}
}
