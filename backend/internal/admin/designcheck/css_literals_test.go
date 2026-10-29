package designcheck

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var colourLiteral = regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b|\brgba?\(|\bhsla?\(`)

// previewFrames are the only places a literal colour is allowed outside
// tokens.css: the editors' preview frames show the visitor's page, which is
// white whatever the admin theme is. Keyed by file, matched by selector.
var previewFrames = map[string][]string{
	"pageeditor.css": {".hm-pe__frame", ".hm-ed__preview"},
	"blogeditor.css": {".hm-be__frame"},
}

// Colours come from tokens.css, so a theme change (dark, auto) reaches every
// surface. A literal in a partial or in a script would skip the dark theme.
func TestCSSLiterals(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..", "..", "web", "admin")
	var files []string
	err := filepath.WalkDir(filepath.Join(root, "css"), func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".css") && d.Name() != "tokens.css" {
			files = append(files, p)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 10 {
		t.Fatalf("found only %d files to check", len(files))
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		allowed := previewFrames[filepath.Base(f)]
		for i, line := range strings.Split(string(b), "\n") {
			if !colourLiteral.MatchString(line) {
				continue
			}
			ok := false
			for _, sel := range allowed {
				ok = ok || strings.Contains(line, sel)
			}
			if !ok {
				t.Errorf("%s:%d has a literal colour, use a token: %s", filepath.ToSlash(f), i+1, strings.TrimSpace(line))
			}
		}
	}
}

// ISC-31: Tailwind-style status colours (the pastel utilities) are gone.
func TestNoPastelUtilityClasses(t *testing.T) {
	t.Parallel()

	pastel := regexp.MustCompile(`bg-(green|red)-`)
	err := filepath.WalkDir(filepath.Join("..", "..", "..", "web", "admin"), func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.Contains(p, string(filepath.Separator)+"vendor"+string(filepath.Separator)) {
			return err
		}
		b, err := os.ReadFile(p)
		if err == nil && pastel.Match(b) {
			t.Errorf("%s uses a bg-green-/bg-red- utility class", filepath.ToSlash(p))
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

// A tab row whose underline hangs below its box (bottom: -1px) overflows
// vertically, and with overflow-x: auto that shows a scrollbar on every row.
func TestTabsRowsNeverOverflowVertically(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile(filepath.Join("..", "..", "..", "web", "admin", "css", "partials", "tabs.css"))
	if err != nil {
		t.Fatal(err)
	}
	css := string(b)
	if strings.Contains(css, "bottom: -1px") {
		t.Error("tabs.css: an underline at bottom: -1px makes the tab row overflow vertically; use bottom: 0 and an inset box-shadow hairline")
	}
	rule := regexp.MustCompile(`(?m)^\.hm-tabs__list, \.hm-tablist \{[^}]*\}`).FindString(css)
	if !strings.Contains(rule, "overflow-y: hidden") || !strings.Contains(rule, "scrollbar-width: none") {
		t.Errorf(".hm-tabs__list, .hm-tablist must set overflow-y: hidden and scrollbar-width: none, got %q", rule)
	}
}
