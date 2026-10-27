package render

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"hatamoto.dev/core/internal/builder"
)

var rootTagRe = regexp.MustCompile(`<([a-z][a-z0-9]*)\b[^>]*\{\{\.Attrs\}\}[^>]*>`)

// A widget whose root element is a role-less <div>/<span> prohibits aria-label
// (axe aria-prohibited-attr): its kind must not offer an accessible name.
func TestPlainRootKindsOfferNoAria(t *testing.T) {
	files, _ := filepath.Glob("widgets/*/widget.html")
	if len(files) == 0 {
		t.Fatal("no widget templates found")
	}
	for _, f := range files {
		kind := filepath.Base(filepath.Dir(f))
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		m := rootTagRe.FindSubmatch(src)
		if m == nil {
			t.Errorf("%s: no root element with {{.Attrs}}", kind)
			continue
		}
		plain := (string(m[1]) == "div" || string(m[1]) == "span") && !regexp.MustCompile(`\brole=`).Match(m[0])
		if plain && builder.AriaAllowed(&builder.Node{T: kind}) {
			t.Errorf("kind %s renders a plain <%s> root but offers an accessible name: add it to noAriaKinds", kind, m[1])
		}
	}
}
