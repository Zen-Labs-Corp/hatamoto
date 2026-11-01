package admin

import (
	"strings"
	"testing"

	"hatamoto.dev/core/internal/admin/ui"
)

func TestPartialTableBulkBar(t *testing.T) {
	t.Parallel()

	s := renderChips(t, "hm/table", fixtureTable())
	if strings.Contains(s, "Tick rows") {
		t.Errorf("the bulk bar keeps no lead-in text: %s", s)
	}
	if !strings.Contains(s, `<span data-hm-bulk-count aria-live="polite"></span>`) {
		t.Errorf("the bulk count slot is missing: %s", s)
	}
}

func TestPartialTableRowPostButton(t *testing.T) {
	t.Parallel()

	tbl := fixtureTable()
	tbl.Rows[0].Actions = append(tbl.Rows[0].Actions, ui.Action{Label: "Retry", Post: "/admin/x/retry", Button: true, Fields: map[string]string{"id": "7"}})
	s := renderChips(t, "hm/table", tbl)
	want := `<form method="post" action="/admin/x/retry"><input type="hidden" name="_csrf" value="tok"><input type="hidden" name="id" value="7"><button type="submit" class="hm-btn hm-btn--secondary hm-btn--sm"><span>Retry</span></button></form>`
	if !strings.Contains(s, want) {
		t.Errorf("missing the Retry form:\n%s", s)
	}
	if strings.Contains(s, `role="menuitem">Retry`) {
		t.Errorf("a button action stays out of the row menu: %s", s)
	}
}

func TestPartialCellMonoClip(t *testing.T) {
	t.Parallel()

	a := newPartialAdmin(t)
	out, err := a.RenderPartial("hm/cell", ui.Cell{Text: "/blog/{slug}", Mono: true, Clip: true})
	if err != nil {
		t.Fatal(err)
	}
	if want := `<span class="hm-table__mono hm-table__clip" title="/blog/{slug}">/blog/{slug}</span>`; string(out) != want {
		t.Errorf("cell = %s, want %s", out, want)
	}
}

func TestPartialSectionheadactOverflow(t *testing.T) {
	t.Parallel()

	type section struct{ Label, Desc string }
	view := struct {
		CSRF           string
		CurrentSection *section
		SectionTabs    []ui.Tab
	}{CSRF: "tok", CurrentSection: &section{Label: "Linking", Desc: "Rules"}}
	out, err := newPartialAdmin(t).RenderPartial("hm/sectionheadact", map[string]any{
		"V":        view,
		"Action":   map[string]any{"Label": "New rule", "Href": "/admin/new", "Variant": "primary"},
		"Overflow": []ui.Action{{Label: "Delete all rules", Post: "/admin/rules/delete-all", Danger: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{`popovertarget="hm-overflow"`, `<form method="post" action="/admin/rules/delete-all"><input type="hidden" name="_csrf" value="tok">`, `hm-menu__item hm-menu__item--danger`} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in %s", want, s)
		}
	}
	plain, err := newPartialAdmin(t).RenderPartial("hm/sectionheadact", map[string]any{"V": view})
	if err != nil || strings.Contains(string(plain), "hm-overflow") {
		t.Errorf("no Overflow, no menu: %v %s", err, plain)
	}
}
