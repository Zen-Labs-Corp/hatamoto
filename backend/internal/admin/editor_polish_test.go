package admin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func adminAsset(t *testing.T, rel ...string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(append([]string{"..", "..", "web", "admin"}, rel...)...))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The writing view is a page, not a field: the blog editor shows no box or ring
// around the text on focus or hover; the caret is the indicator.
func TestBlogWritingAreaHasNoBox(t *testing.T) {
	t.Parallel()

	css := adminAsset(t, "css", "pages", "blogeditor.css")
	for _, want := range []string{".hm-be .hm-ed--blog:focus-within { border: 0;", ".hm-be .hm-ed--blog .ProseMirror:focus-visible { outline: none;"} {
		if !strings.Contains(css, want) {
			t.Errorf("blogeditor.css lacks %q", want)
		}
	}
}

// The settings panel is a full-height column joined to the top bar: sticky under it,
// hairline on its left, scrolling on its own, with flat sections instead of cards.
func TestBlogSidePanelIsFlushColumn(t *testing.T) {
	t.Parallel()

	css := adminAsset(t, "css", "pages", "blogeditor.css")
	for _, want := range []string{
		".hm-page:has(.hm-be) { max-width: none; padding: 0; gap: 0; }",
		"position: sticky; top: 56px; height: calc(100vh - 56px); overflow-y: auto;",
		"border-left: 1px solid var(--border)",
		".hm-be__side .hm-card { background: transparent; border: 0; border-bottom: 1px solid var(--border); border-radius: 0; box-shadow: none; }",
		"@media (min-width: 960px)",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("blogeditor.css lacks %q", want)
		}
	}
}

// Preview is a native modal dialog (Esc closes, focus returns to the button), not a
// pane that pushes the editor around.
func TestBlogPreviewIsModalDialog(t *testing.T) {
	t.Parallel()

	html := adminAsset(t, "pages", "entities", "blogedit.html")
	if !strings.Contains(html, `<dialog class="hm-dialog hm-be__dialog" id="be-preview"`) || !strings.Contains(html, "data-hm-be-preview-close") {
		t.Error("the post preview is not a dialog with a Close button")
	}
	js := adminAsset(t, "js", "blogeditor.js")
	for _, want := range []string{"dialog.showModal()", `dialog?.addEventListener("close", () => toggle?.focus())`} {
		if !strings.Contains(js, want) {
			t.Errorf("blogeditor.js lacks %q", want)
		}
	}
}

// Publish at is the analytics-style preset picker over the native datetime-local
// input, which stays in the form as the no-JavaScript fallback.
func TestBlogPublishAtIsPresetPicker(t *testing.T) {
	t.Parallel()

	html := adminAsset(t, "pages", "entities", "blogedit.html")
	for _, want := range []string{
		`data-hm-be-sched-pop`, `data-hm-be-sched-preset="tomorrow"`, `data-hm-be-sched-preset="monday"`, `"popovertargetaction" "hide"`,
		`"Name" "schedule" "Label" "Publish at" "Type" "datetime-local"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("blogedit.html lacks %q", want)
		}
	}
}

// Several terms typed into the chips are created on save, each with its own kind,
// and joined to the chosen ones; the no-JavaScript single field still works.
func TestBlogSaveCreatesTypedTerms(t *testing.T) {
	t.Parallel()

	r := newBlogRig(t)
	body := r.get(t, "owner-token", r.path(), "")
	has(t, body, `data-hm-terms data-hm-terms-create`, `data-hm-terms-kind=`, `data-hm-terms-new`, `name="new_term_name"`)
	f := r.form(t)
	f.Set("term", fid(33).String())
	f["new_term"] = []string{"category:Guides", "tag:Fresh", "Bare tag", "category:"}
	f.Set("new_term_name", "Old way")
	f.Set("new_term_kind", "tag")
	f.Set("_op", "save")
	if rec := r.post(r.path(), f); rec.Code != 303 {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}
	got := map[string]string{}
	for _, tm := range r.terms.terms {
		got[tm.Name] = tm.Kind
	}
	for name, kind := range map[string]string{"Guides": "category", "Fresh": "tag", "Bare tag": "tag", "Old way": "tag"} {
		if got[name] != kind {
			t.Errorf("term %q kind %q, want %q (all: %v)", name, got[name], kind, got)
		}
	}
	if ids := r.terms.attach[r.ent.ID]; len(ids) != 5 {
		t.Errorf("attached %d terms, want the chosen one and four new: %v", len(ids), ids)
	}
}

// The token input is a Tab stop with its own focus ring (the chips' box is not the indicator).
func TestTokenInputHasFocusRing(t *testing.T) {
	t.Parallel()

	if css := adminAsset(t, "css", "pages", "blogeditor.css"); !strings.Contains(css, ".hm-tokens__input:focus-visible { outline: var(--focus-ring);") {
		t.Error("the token input has no focus ring")
	}
}
