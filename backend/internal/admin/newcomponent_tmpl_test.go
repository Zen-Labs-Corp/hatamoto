package admin

import (
	"os"
	"strings"
	"testing"
)

// scripts/new-component.sh inserts into these files at fixed markers. Losing a
// marker would only show when someone scaffolds, so check them here.
func TestNewComponentAnchorsExist(t *testing.T) {
	t.Parallel()

	for file, marker := range map[string]string{
		"../../../docs/ADMIN_UI_RULES.md": "<!-- new-components-above -->",
		"styleguide.go":                   "new-components-above (scripts/new-component.sh inserts here)",
	} {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if n := strings.Count(string(b), marker); n != 1 {
			t.Errorf("%s: want exactly one %q for scripts/new-component.sh, found %d", file, marker, n)
		}
	}
}
