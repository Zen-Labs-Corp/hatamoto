package admin

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// phoneTableBlock returns the (max-width: 640px) block of the table partial CSS.
func phoneTableBlock(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("../../web/admin/css/partials/table.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(b)
	i := strings.Index(css, "@media (max-width: 640px) {")
	j := strings.Index(css, ".hm-table__nowrap")
	if i < 0 || j < i {
		t.Fatal("phone media block not found in table.css")
	}
	return css[i:j]
}

// The card layout depends on data-label, which only hm/table emits. Hand-written .hm-table
// tables (datatable, diff, pkgconfirm, themeactivate) must keep their column headers on phones.
func TestPhoneTableCardsAreOptIn(t *testing.T) {
	t.Parallel()

	sel := regexp.MustCompile(`(?m)^\s*(\.hm-table[^{]*)\{`)
	m := sel.FindAllStringSubmatch(phoneTableBlock(t), -1)
	if len(m) == 0 {
		t.Fatal("no table rules in the phone block")
	}
	for _, r := range m {
		for _, s := range strings.Split(r[1], ",") {
			s = strings.TrimSpace(s)
			if strings.HasPrefix(s, ".hm-table__") { // the filter strip is not a table
				continue
			}
			if !strings.Contains(s, ".hm-table--cards") {
				t.Errorf("phone rule %q applies to every .hm-table; scope it to .hm-table--cards", strings.TrimSpace(s))
			}
		}
	}
	b, err := os.ReadFile("../../web/admin/partials/table.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `<table class="hm-table hm-table--cards">`) {
		t.Error("hm/table must render class hm-table--cards")
	}
}

// The card row is a flex line: the row head (1) and the actions menu (2) share line one, a ::after
// break at order 2 ends it, and the other cells (order 3) are line two. The select box hangs in the
// left gutter (absolute, the row pads it). If any of these rules is lost the checkbox, title and row
// menu can overlap.
func TestPhoneBulkRowKeepsCheckAndActionsApart(t *testing.T) {
	t.Parallel()

	for _, want := range []string{
		".hm-table--cards td.hm-table__check { position: absolute;",
		".hm-table--cards th.hm-table__rowhead { order: 1; flex: 1 1 0; min-width: 0;",
		".hm-table--cards td.hm-table__actions { order: 2;",
		".hm-table--cards tbody tr::after { content: \"\"; order: 2; flex: 0 0 100%;",
		".hm-table--cards td { order: 3;",
		".hm-table--cards td.hm-col-wide { display: none; }",
	} {
		if !strings.Contains(phoneTableBlock(t), want) {
			t.Errorf("phone block lost the card row rule:\n%s", want)
		}
	}
}

// The header look (small, uppercase, muted) is one rule in table.css for every .hm-table.
func TestTableHeaderIsUppercaseSmall(t *testing.T) {
	t.Parallel()

	css := partialCSS(t, "table.css")
	want := ".hm-table thead th { font-size: var(--text-xs); font-weight: var(--weight-medium); text-transform: uppercase; letter-spacing: 0.08em; color: var(--text-muted);"
	if !strings.Contains(css, want) {
		t.Errorf("table.css must carry the shared header rule %q", want)
	}
}
