package admin

import (
	"strings"
	"testing"

	"hatamoto.dev/core/internal/admin/ui"
)

func TestPartialSearchSelect(t *testing.T) {
	t.Parallel()

	a := newPartialAdmin(t)
	s := checkPartial(t, a, "hm/searchselect", "empty", map[string]any{"ID": "ent", "Name": "entity", "Label": "Page or post", "Src": "/admin/pick/entities"})
	for _, want := range []string{`role="combobox"`, `aria-controls="ent-list"`, `data-src="/admin/pick/entities"`, `name="entity_q"`} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q", want)
		}
	}
	checkPartial(t, a, "hm/searchselect", "picked", map[string]any{"ID": "ent", "Name": "entity", "Label": "Page or post", "Src": "/admin/pick/entities", "Value": "0b1c", "Selected": "About us", "Help": "Pick one."})
	s = checkPartial(t, a, "hm/searchselect", "error", map[string]any{"ID": "ent", "Name": "entity", "Label": "Page or post", "Src": "/admin/pick/entities", "Required": true, "Error": "Choose a page"})
	if !strings.Contains(s, `aria-invalid="true"`) || !strings.Contains(s, `aria-describedby="ent-help ent-error"`) {
		t.Errorf("error wiring: %s", s)
	}
	if _, err := a.RenderPartial("hm/searchselect", map[string]any{"Name": "x", "Label": "x", "Src": "/x"}); err == nil {
		t.Error("a search-select without an ID must not render")
	}
}

func TestPartialTabsShowZero(t *testing.T) {
	t.Parallel()

	a := newPartialAdmin(t)
	items := []ui.Tab{{Label: "Tags", Href: "/t", Count: 3, Active: true}, {Label: "Series", Href: "/s"}}
	s := checkPartial(t, a, "hm/tabs", "show-zero", map[string]any{"Label": "Kinds", "Items": items, "Variant": "sub", "ShowZero": true})
	if !strings.Contains(s, `Tags <span class="hm-tabs__count">3</span>`) || !strings.Contains(s, `Series <span class="hm-tabs__count">0</span>`) {
		t.Errorf("a zero count must show when ShowZero is set: %s", s)
	}
	s = checkPartial(t, a, "hm/tabs", "hide-zero", map[string]any{"Label": "Kinds", "Items": items, "Variant": "sub"})
	if strings.Contains(s, ">0<") {
		t.Errorf("a zero count stays hidden by default: %s", s)
	}
}
