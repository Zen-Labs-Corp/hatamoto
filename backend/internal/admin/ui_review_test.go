package admin

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

var submitTag = regexp.MustCompile(`<button[^>]*type="submit"[^>]*>`)

// firstSubmit returns the first submit button tag inside the first form that has the given class.
func firstSubmit(t *testing.T, page, formClass string) string {
	t.Helper()
	i := strings.Index(page, `class="`+formClass)
	if i < 0 {
		t.Fatalf("no form with class %q", formClass)
	}
	rest := page[i:]
	if end := strings.Index(rest, "</form>"); end >= 0 {
		rest = rest[:end]
	}
	m := submitTag.FindString(rest)
	if m == "" {
		t.Fatal("form has no submit button")
	}
	return m
}

// Enter submits with the first submit button in tree order, so on the rule form that must be Save.
func TestLinkingRuleFormDefaultSubmitIsSave(t *testing.T) {
	t.Parallel()

	g, _, _, _ := newLinkRig(t)
	body := g.do("GET", "/admin/settings/linking/rules/new", "owner-token", nil, nil).Body.String()
	first := firstSubmit(t, body, "hm-cardform")
	if strings.Contains(first, "preview") || strings.Contains(first, "name=") {
		t.Errorf("first submit must be the plain save button, got %s", first)
	}
	if !strings.Contains(body, "Preview matches") {
		t.Error("preview button missing")
	}
}

// The menu editor keeps Delete menu then Save in its sticky savebar.
func TestMenuEditSaveInSavebar(t *testing.T) {
	t.Parallel()

	g, fm := newMenuRig(t)
	m := fm.add("Main", "header")
	body := g.do("GET", "/admin/theme/menus/"+m.ID.String(), "owner-token", nil, nil).Body.String()
	bar := body[strings.Index(body, `<div class="hm-savebar">`):]
	del, save := strings.Index(bar, "Delete menu"), strings.Index(bar, `<span>Save</span>`)
	if del < 0 || save < del {
		t.Errorf("savebar needs Delete menu then Save: %s", bar)
	}
}

func TestBareSwitchHasOneNameAndErrorLinks(t *testing.T) {
	t.Parallel()

	a := newPartialAdmin(t)
	s := renderField(t, a, map[string]any{"ID": "page-cache", "Name": "enabled", "Value": "on", "Label": "Page cache",
		"Control": "switch", "Bare": true, "Error": "Choose On or Off."})
	if strings.Contains(s, "Page cache") || strings.Contains(s, "hm-visually-hidden") {
		t.Errorf("a bare switch must not repeat the label the page already shows: %s", s)
	}
	if !strings.Contains(s, `aria-invalid="true"`) || !strings.Contains(s, `aria-describedby="page-cache-error"`) {
		t.Errorf("error not linked to the switch: %s", s)
	}
	if strings.Contains(s, "Choose On or Off.") {
		t.Errorf("a bare switch must not print the error the page shows: %s", s)
	}
}

func TestPerformanceSwitchLinksItsError(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile("../../web/admin/pages/settings/performance.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{`(index .Errs "page-cache")`, `(index .Errs "preload")`} {
		if !strings.Contains(string(b), k) {
			t.Errorf("performance.html must pass %s as Error", k)
		}
	}
}

func TestDialogPrimaryWithFormAttr(t *testing.T) {
	t.Parallel()

	a := newPartialAdmin(t)
	out, err := a.RenderPartial("hm/dialog-end", map[string]any{"Primary": "Save", "PrimaryAttrs": map[string]string{"form": "edit-form"}})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, `<form method="dialog"`) || !strings.Contains(s, `type="submit"`) || !strings.Contains(s, `form="edit-form"`) || !strings.Contains(s, "hm-btn--primary") {
		t.Errorf("primary slot must pass form=: %s", s)
	}
}

func readWeb(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile("../../web/admin/" + p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestImportOptionsSeparatesPicksFromSwitches(t *testing.T) {
	t.Parallel()

	b := readWeb(t, "pages/import/options.html")
	fs := b[strings.Index(b, "<fieldset"):strings.Index(b, "</fieldset>")]
	if strings.Contains(fs, `"Control" "switch"`) {
		t.Error("the content type group must hold only checkboxes")
	}
}
