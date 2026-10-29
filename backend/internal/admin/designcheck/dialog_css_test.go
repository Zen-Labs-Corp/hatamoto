package designcheck

import (
	"os"
	"strings"
	"testing"
)

// overflow:hidden on every open dialog clipped the builder shortcuts sheet (no
// .hm-dialog__body) with no way to reach Close on a short viewport. Only dialogs with the
// body/foot structure may pin their footer; all others scroll.
func TestDialogOverflowOnlyHiddenWithBody(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile("../../../web/admin/css/partials/dialog.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(b)
	if strings.Contains(css, ".hm-dialog[open] { display: flex; flex-direction: column; overflow: hidden; }") {
		t.Error("every open dialog is overflow:hidden; scope it to :has(.hm-dialog__body)")
	}
	for _, want := range []string{
		".hm-dialog[open] { display: flex; flex-direction: column; overflow: auto; }",
		".hm-dialog[open]:has(.hm-dialog__body) { overflow: hidden; }",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("dialog.css lacks %q", want)
		}
	}
}
