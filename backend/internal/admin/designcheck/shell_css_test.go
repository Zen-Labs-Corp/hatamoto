package designcheck

import (
	"os"
	"strings"
	"testing"
)

// Visual-acceptance regressions of the shell and auth CSS (screens/README.md of admin-shell-auth).
func cssSource(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("../../../web/admin/css/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// On phones the sidebar is a top bar stacked over the page: an auto first row keeps its height
// on a short page (404, 403) instead of sharing the 100dvh with the content.
func TestPhoneShellKeepsTopBarHeight(t *testing.T) {
	t.Parallel()

	css := cssSource(t, "base.css")
	if !strings.Contains(css, "grid-template-rows: auto minmax(0, 1fr)") {
		t.Error("base.css: the phone shell grid needs `grid-template-rows: auto minmax(0, 1fr)`")
	}
}

// On phones a header strip with neither a select-all box nor a sort link is hidden, one with a sort
// link stays visible (touch users sort there), and only the users list is flush with the page edge.
func TestPhoneCardsListLayout(t *testing.T) {
	t.Parallel()

	css := cssSource(t, "partials/table.css")
	for _, want := range []string{
		".hm-table--cards thead:not(:has(.hm-table__check, .hm-table__sort))",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("table.css: missing %q", want)
		}
	}
	for _, bad := range []string{":not(:focus-within)", "\n  .hm-tablecard .hm-table--cards tr {", "\n  .hm-table__filters {"} {
		if strings.Contains(css, bad) {
			t.Errorf("table.css: %q hides the sort links or restyles every list on phones", bad)
		}
	}
}

// A long command is shown whole: the forgot page uses a wrapping textarea, not a one-line input.
func TestCommandFieldWraps(t *testing.T) {
	t.Parallel()

	if css := cssSource(t, "base.css"); !strings.Contains(css, ".hm-copyrow textarea.hm-field__control") {
		t.Error("base.css: .hm-copyrow needs a rule for the wrapping textarea")
	}
	b, err := os.ReadFile("../../../web/admin/pages/auth/forgot.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"Control" "textarea"`) {
		t.Error("forgot.html: the command field must be a textarea so a long command wraps")
	}
}

// Phone cards with a select box: the box hangs in the gutter so wrapped cells align with the title.
func TestPhoneCardsSelectBoxHangsInGutter(t *testing.T) {
	t.Parallel()

	css := cssSource(t, "partials/table.css")
	for _, want := range []string{
		".hm-table--cards tbody tr:has(> td.hm-table__check) { position: relative; padding-inline-start: var(--space-8); }",
		".hm-table--cards td.hm-table__check { position: absolute;",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("table.css: missing %q", want)
		}
	}
	if strings.Contains(css, "tr:has(> .hm-table__check)::before") {
		t.Error("table.css: the one-off spacer before the cells came back; only the first line would align")
	}
}

func TestInviteAltLinkAlignsWithField(t *testing.T) {
	t.Parallel()

	if css := cssSource(t, "base.css"); !strings.Contains(css, ".hm-invite__alt { margin-inline-start: calc(-1 * var(--space-3)); }") || strings.Contains(css, ".hm-invite__alt .hm-btn") {
		t.Error("base.css: the ghost link under the invite email needs its left padding pulled back")
	}
}
