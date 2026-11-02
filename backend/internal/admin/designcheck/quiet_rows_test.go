package designcheck

import (
	"strings"
	"testing"
)

// lineWith returns the first line of src that starts with prefix, or "".
func lineWith(src, prefix string) string {
	for _, l := range strings.Split(src, "\n") {
		if strings.HasPrefix(l, prefix) {
			return l
		}
	}
	return ""
}

// Quiet rows and honest nav markers (unslop2): list titles are sumi at medium weight and underline
// only on hover, a published row is text with a decorative success dot (drafts, scheduled and
// archived keep their badge), and a sidebar marker is muted mono with a title and screen-reader
// text saying what it counts. Reverting any of these fails here.
func TestQuietRowsAndNavMarkers(t *testing.T) {
	t.Parallel()

	table := adminAsset(t, "css", "partials", "table.css")
	title := lineWith(table, ".hm-table__rowhead a {")
	if title == "" {
		t.Fatal("table.css: no .hm-table__rowhead a rule")
	}
	for _, want := range []string{"color: var(--text)", "font-weight: var(--weight-medium)"} {
		if !strings.Contains(title, want) {
			t.Errorf("table.css: the row title lacks %q", want)
		}
	}
	if strings.Contains(title, "color-accent") || strings.Contains(title, "accent-link") {
		t.Error("table.css: the row title is accent-coloured")
	}
	if strings.Contains(title, "text-decoration-line: underline") {
		t.Error("table.css: the row title is underlined at rest")
	}
	if hover := lineWith(table, ".hm-table__rowhead a:hover {"); !strings.Contains(hover, "underline") {
		t.Error("table.css: the row title does not underline on hover")
	}
	if dot := lineWith(table, ".hm-status__dot {"); !strings.Contains(dot, "var(--color-success)") {
		t.Error("table.css: .hm-status__dot is not the success colour")
	}

	ents := readSrc(t, "../entities.go")
	if !strings.Contains(ents, "Dot: true") {
		t.Error("entities.go: a published row has no quiet status dot")
	}

	badge := ""
	for _, l := range strings.Split(readWeb(t, "partials/sidebar.html"), "\n") {
		if strings.Contains(l, "hm-nav-link__badge") {
			badge = l
		}
	}
	for _, want := range []string{"title=", "hm-visually-hidden", `aria-hidden="true"`} {
		if !strings.Contains(badge, want) {
			t.Errorf("sidebar.html: the nav marker lacks %q (an unlabelled marker)", want)
		}
	}
	if mark := lineWith(adminAsset(t, "css", "partials", "sidebar.css"), ".hm-nav-link__badge {"); !strings.Contains(mark, "var(--font-mono)") {
		t.Error("sidebar.css: the nav marker is not mono")
	}
}
