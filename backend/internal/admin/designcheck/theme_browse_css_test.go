package designcheck

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// Theme Browse cards are screenshot-first: a fixed 16:10 frame cropped from the
// top, a 260px auto-fill grid, and only design tokens for colour (light + dark).
func TestThemeBrowseCSS(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile("../../../web/admin/css/pages/themebrowse.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(b)
	for _, want := range []string{
		"repeat(auto-fill, minmax(min(260px, 100%), 1fr))",
		"aspect-ratio: 16 / 10",
		"object-fit: cover",
		"object-position: top",
		"var(--color-kasumi)",
		"var(--color-success)",
		"var(--color-accent)",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("themebrowse.css: missing %q", want)
		}
	}
	if raw := regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b|\b(?:rgb|hsl)a?\(`).FindString(css); raw != "" {
		t.Errorf("themebrowse.css: raw colour %q, use tokens", raw)
	}
	if strings.Contains(css, "!important") {
		t.Error("themebrowse.css: !important is not allowed")
	}
}
