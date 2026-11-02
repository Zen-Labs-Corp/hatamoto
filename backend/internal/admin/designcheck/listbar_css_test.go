package designcheck

import (
	"strings"
	"testing"
)

// Signs 2, 3 and 6 (unslop2): a list toolbar is one 32px row, the Default/SEO toggle lives in the
// table header, selects carry the lucide chevron, and counts are mono. Reverting the row, a control
// height or radius, the chevron, the header toggle or the separate view strip fails here.
func TestListToolbarIsOneSizedRow(t *testing.T) {
	t.Parallel()

	listbar := adminAsset(t, "css", "partials", "listbar.css")
	for _, want := range []string{"display: flex", "gap: var(--space-2)", "border-bottom: 1px solid var(--border)"} {
		if !strings.Contains(listbar, want) {
			t.Errorf("listbar.css: the row lacks %q", want)
		}
	}
	for _, f := range []string{"field.css", "select.css", "segmented.css", "tabs.css"} {
		css := adminAsset(t, "css", "partials", f)
		var found bool
		for _, line := range strings.Split(css, "\n") {
			if strings.HasPrefix(line, ".hm-listbar ") && strings.Contains(line, "var(--control-h-sm)") {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: controls in .hm-listbar must be var(--control-h-sm) high", f)
		}
		if f != "tabs.css" && !strings.Contains(css, ".hm-listbar") {
			t.Errorf("%s: no .hm-listbar rule", f)
		}
	}
	for _, f := range []string{"field.css", "select.css", "segmented.css"} {
		var found bool
		for _, line := range strings.Split(adminAsset(t, "css", "partials", f), "\n") {
			if strings.HasPrefix(line, ".hm-listbar ") && strings.Contains(line, "var(--radius-md)") {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: controls in .hm-listbar must use var(--radius-md)", f)
		}
	}

	for _, f := range []string{"field.css", "select.css"} {
		css := adminAsset(t, "css", "partials", f)
		if !strings.Contains(css, "data:image/svg+xml") || !strings.Contains(css, "m6 9 6 6 6-6") {
			t.Errorf("%s: selects must show the lucide chevron-down", f)
		}
		if strings.Contains(css, "linear-gradient") {
			t.Errorf("%s: the gradient triangle is back", f)
		}
	}

	tabs := adminAsset(t, "css", "partials", "tabs.css")
	if strings.Contains(tabs, `.hm-entities .hm-tabs[data-variant="segmented"]`) {
		t.Error("tabs.css: the separate view strip row is back; the toggle belongs in the table header")
	}
	if table := readWeb(t, "partials/table.html"); !strings.Contains(table, "hm-table__views") || !strings.Contains(table, `"Changes" "view"`) {
		t.Error("table.html: the Default/SEO toggle must render in the thead actions cell")
	}
	for _, page := range []string{"pages/entities/list.html", "pages/taxonomies/list.html"} {
		if strings.Contains(readWeb(t, page), `"Changes" "view"`) {
			t.Errorf("%s: the view toggle must come from the table (ViewTabs), not a strip above it", page)
		}
	}

	for f, sel := range map[string]string{
		"segmented.css": ".hm-segmented__count {",
		"tabs.css":      ".hm-tabs__count {",
		"settings.css":  ".hm-subnav--entities .hm-subnav__marker {",
	} {
		var found bool
		for _, line := range strings.Split(adminAsset(t, "css", "partials", f), "\n") {
			if strings.HasPrefix(line, sel) && strings.Contains(line, "font-family: var(--font-mono)") {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: %s must set counts in --font-mono", f, sel)
		}
	}
}

// The Filters button sits beside 32px md-radius controls and must match them.
func TestListToolbarFiltersButtonRadius(t *testing.T) {
	t.Parallel()

	var found bool
	for _, line := range strings.Split(adminAsset(t, "css", "partials", "button.css"), "\n") {
		if strings.HasPrefix(line, ".hm-listbar__end > .hm-btn") && strings.Contains(line, "border-radius: var(--radius-md)") {
			found = true
		}
	}
	if !found {
		t.Error("button.css: the Filters button in .hm-listbar must use var(--radius-md)")
	}
}

// On a phone the nowrap status segment scrolls inside the card; it must not run past it.
func TestListToolbarSegmentScrollsOnPhones(t *testing.T) {
	t.Parallel()

	css := adminAsset(t, "css", "partials", "segmented.css")
	i := strings.Index(css, "@media (max-width: 640px)")
	if i < 0 {
		t.Fatal("segmented.css: no phone breakpoint for the list toolbar segment")
	}
	block := css[i:]
	if j := strings.Index(block, "\n}"); j >= 0 {
		block = block[:j]
	}
	for _, want := range []string{".hm-listbar .hm-segmented", "max-width: 100%", "overflow-x: auto"} {
		if !strings.Contains(block, want) {
			t.Errorf("segmented.css phone block lacks %q", want)
		}
	}
}
