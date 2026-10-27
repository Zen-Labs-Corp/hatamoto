package admin

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// js/builder/colournames.js is the builder's copy of builderColourNames; the two must say the same.
func TestColourNamesMirrorJS(t *testing.T) {
	t.Parallel()

	src, err := os.ReadFile(filepath.Join("..", "..", "web", "admin", "js", "builder", "colournames.js"))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, m := range regexp.MustCompile(`(\w+): "([^"]+)"`).FindAllStringSubmatch(string(regexp.MustCompile(`(?m)^export const COLOUR_NAME = \{.*\};`).Find(src)), -1) {
		got[m[1]] = m[2]
	}
	if len(got) != len(builderColourNames) {
		t.Fatalf("colournames.js has %v, Go has %v", got, builderColourNames)
	}
	for k, v := range builderColourNames {
		if got[k] != v {
			t.Errorf("%s: js %q, go %q", k, got[k], v)
		}
	}
}
