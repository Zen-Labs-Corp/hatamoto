package admin

import (
	"strings"
	"testing"

	"hatamoto.dev/core/internal/admin/ui"
)

// The opt-in filter bar params render only when set; the plain bar's bytes are
// pinned by the people-list and entities-list goldens.
func TestPartialFilterbarOptIns(t *testing.T) {
	t.Parallel()

	a := newPartialAdmin(t)
	bar := ui.Table{
		ID: "browse", BaseURL: "/admin/plugins/browse", SearchLabel: "Search plugins",
		Live: true, Target: "browse-results", Bare: true, ChipsOutside: true, FilterCount: 1,
		Segmented: &ui.Segmented{Label: "Publisher", Name: "tier", Items: []ui.SegmentedItem{{Label: "All", Pressed: true}, {Label: "Community", Value: "community"}}},
		PopoverFilters: []ui.Filter{
			{Name: "perm", Label: "Permissions", Control: "checkbox", Options: []ui.Option{{Value: "a", Label: "No network", Selected: true}, {Value: "b", Label: "No scripts"}}},
			{Name: "hide", Label: "Hide installed", Control: "switch", Value: "1", OffValue: "0", Checked: true},
		},
		Chips: []ui.Chip{{Label: "No network", RemoveHref: "/x"}}, ClearHref: "/admin/plugins/browse",
	}
	got, err := a.RenderPartial("hm/filterbar", bar)
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	for _, want := range []string{
		`hm-filterbar--bare`, `role="search"`,
		`change from:#browse-seg, change from:#browse-filters, submit`,
		`hx-target="#browse-results" hx-select="#browse-results" hx-select-oob="#browse-count"`,
		`<fieldset class="hm-segmented" id="browse-seg">`,
		`<span class="hm-btn-count" id="browse-count"><span class="hm-visually-hidden">, </span>1`,
		`<legend class="hm-field__label">Permissions</legend>`,
		`name="perm" value="a" checked>`, `name="perm" value="b">`,
		`role="switch" id="browse-f1" name="hide" value="1" checked>`, `<input type="hidden" name="hide" value="0">`,
		`<noscript>`, `data-hm-apply`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in %s", want, s)
		}
	}
	for _, bad := range []string{`>Apply</button>`, `hm-filter-chips`} {
		if strings.Contains(s, bad) {
			t.Errorf("unexpected %q in %s", bad, s)
		}
	}

	chips, err := a.RenderPartial("hm/filterchips", bar)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(chips), `aria-label="Remove filter: No network"`) {
		t.Errorf("chips lack the remove label: %s", chips)
	}

	bar.FilterCount = 0
	bar.Chips = nil
	got, _ = a.RenderPartial("hm/filterbar", bar)
	if !strings.Contains(string(got), `id="browse-count" hidden>`) {
		t.Errorf("an empty live count must stay in the DOM, hidden: %s", got)
	}
}
