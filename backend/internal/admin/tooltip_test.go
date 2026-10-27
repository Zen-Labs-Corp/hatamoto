package admin

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var tooltipTagRx = regexp.MustCompile(`<[a-zA-Z][^<>]*\bdata-tooltip=[^<>]*>`)

// tooltipsWithoutName returns the tags in src that carry data-tooltip but no
// aria-label. A tooltip is a visual repeat of the name, never the name itself:
// it is CSS content, so screen readers and voice control cannot rely on it.
func tooltipsWithoutName(src string) []string {
	var bad []string
	for _, tag := range tooltipTagRx.FindAllString(src, -1) {
		if !strings.Contains(tag, "aria-label=") {
			bad = append(bad, tag)
		}
	}
	return bad
}

func TestTooltipHasAccessibleName(t *testing.T) {
	t.Parallel()

	if got := tooltipsWithoutName(`<button data-tooltip="Sign out">x</button>`); len(got) != 1 {
		t.Fatalf("scan missed a nameless tooltip: %v", got)
	}
	if got := tooltipsWithoutName(`<button aria-label="Sign out" data-tooltip="Sign out">x</button>`); len(got) != 0 {
		t.Fatalf("scan flagged a named tooltip: %v", got)
	}
	err := filepath.WalkDir("../../web/admin", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".html") {
			return err
		}
		b, err := os.ReadFile(p)
		for _, tag := range tooltipsWithoutName(string(b)) {
			t.Errorf("%s: data-tooltip without aria-label: %s", p, tag)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}
