package designcheck

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The save state is a live region: display:none would drop it from the accessibility
// tree, so a narrow window would hide autosave errors from everyone.
func TestEditorSaveStateIsNeverDisplayNone(t *testing.T) {
	t.Parallel()

	css, err := os.ReadFile(filepath.Join("..", "..", "..", "web", "admin", "css", "base.css"))
	if err != nil {
		t.Fatal(err)
	}
	rule := regexp.MustCompile(`[^{}]*hm-editor-bar__state[^{}]*\{[^}]*display:\s*none`)
	if m := rule.Find(css); m != nil {
		t.Errorf("the save state is hidden: %s", m)
	}
}

// Tab and toggle rows scroll sideways but never fade their edge: a mask washed out
// the last segment of every toggle.
func TestTabRowsHaveNoEdgeFade(t *testing.T) {
	t.Parallel()

	files, _ := filepath.Glob(filepath.Join("..", "..", "..", "web", "admin", "css", "*", "*.css"))
	for _, f := range files {
		css, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(css), "mask-image") {
			t.Errorf("%s uses mask-image", f)
		}
	}
}
