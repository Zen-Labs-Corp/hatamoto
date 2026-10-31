package designcheck

import (
	"strings"
	"testing"
)

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

// The token input is a Tab stop with its own focus ring (the chips' box is not the indicator).
func TestTokenInputHasFocusRing(t *testing.T) {
	t.Parallel()

	if css := adminAsset(t, "css", "pages", "blogeditor.css"); !strings.Contains(css, ".hm-tokens__input:focus-visible { outline: var(--focus-ring);") {
		t.Error("the token input has no focus ring")
	}
}

// A search-select inside a dialog lists its results in the flow: the scrolling
// dialog body would otherwise clip the floating list to about one row.
func TestSearchSelectInDialogIsNotClipped(t *testing.T) {
	t.Parallel()

	css := adminAsset(t, "css", "partials", "searchselect.css")
	if want := ".hm-dialog__body .hm-searchselect__list { position: static; }"; !strings.Contains(css, want) {
		t.Errorf("searchselect.css lacks %q", want)
	}
}
