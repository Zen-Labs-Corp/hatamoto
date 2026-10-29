package designcheck

import (
	"os"
	"strings"
	"testing"
)

// css/partials/dropzone.css is the only owner of .hm-dropzone: pages/* load after partials/*,
// so a second rule there silently overrides Compact padding and the neutral hover.
func TestDropzoneCSSHasASingleOwner(t *testing.T) {
	t.Parallel()

	for _, f := range []string{"content.css", "media.css", "forms.css"} {
		if strings.Contains(readAdminCSS(t, f), ".hm-dropzone") {
			t.Errorf("%s styles .hm-dropzone; the owner is css/partials/dropzone.css", f)
		}
	}
	own, err := os.ReadFile("../../../web/admin/css/partials/dropzone.css")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(own), ".hm-dropzone:hover") {
		t.Error("the dropzone border is neutral at rest; accent is only for --over and focus")
	}
}

// Closed, the parent picker shows the chip and "Change"; open, "Done" only. Deleting .hm-parentchip
// once merged the two rules into one selector that hid nothing.
func TestParentPickerHidesWhatTheStateReplaces(t *testing.T) {
	css := readAdminCSS(t, "content.css")
	for _, rule := range []string{
		".hm-parentpick__done, .hm-parentpick[open] .hm-parentpick__open, .hm-parentpick[open] [data-hm-parent-chip] { display: none; }",
		".hm-parentpick[open] .hm-parentpick__done { display: inline; }",
	} {
		if !strings.Contains(css, rule) {
			t.Errorf("content.css lacks %q", rule)
		}
	}
}
