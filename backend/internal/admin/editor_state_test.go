package admin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Menus must not put keyboard focus on a disabled item (Undo with nothing to undo is
// first in the page editor's bar menu): the focus call would silently do nothing.
func TestMenuKeyboardSkipsDisabledItems(t *testing.T) {
	t.Parallel()

	js, err := os.ReadFile(filepath.Join("..", "..", "web", "admin", "js", "components.js"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(js)
	if !strings.Contains(src, `!i.disabled`) || strings.Contains(src, `m.querySelector('[role="menuitem"]')`) || strings.Contains(src, `const items = [...m.querySelectorAll('[role="menuitem"]')]`) {
		t.Error("menu focus and arrow keys must go through menuItems, which skips disabled items")
	}
}

// Popovers are placed from the visible width (clientWidth, no scrollbar) and the
// date range is right-aligned to its button, so it never runs past the viewport.
func TestPopoverPlacementStaysInViewport(t *testing.T) {
	t.Parallel()

	js, err := os.ReadFile(filepath.Join("..", "..", "web", "admin", "js", "components.js"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(js)
	for _, want := range []string{"document.documentElement.clientWidth", `hm-daterange__pop")`, "vw - m.offsetWidth - 8"} {
		if !strings.Contains(src, want) {
			t.Errorf("components.js lacks %q", want)
		}
	}
	if strings.Contains(src, "innerWidth - m.offsetWidth") {
		t.Error("placement must use clientWidth, innerWidth includes the scrollbar")
	}
}

// Dialog backdrop close needs the press and the release on the backdrop (a drag-select that
// ends outside the panel must not close a form dialog), and the dropzone finds a footer
// Upload button that belongs to the form by form="id" so it can disable it during an upload.
func TestDialogBackdropAndUploadButtonSources(t *testing.T) {
	t.Parallel()

	read := func(f string) string {
		b, err := os.ReadFile(filepath.Join("..", "..", "web", "admin", "js", f))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	comp := read("components.js")
	if !strings.Contains(comp, `addEventListener("pointerdown"`) || !strings.Contains(comp, "backdropDown === dlg") {
		t.Error("components.js: the backdrop must close a dialog only when pointerdown and click both hit the dialog element")
	}
	if !strings.Contains(read("content.js"), "button[form=") {
		t.Error("content.js: the upload button outside the form (form=\"upload-form\") is not looked up, so it stays clickable during an upload")
	}
}
