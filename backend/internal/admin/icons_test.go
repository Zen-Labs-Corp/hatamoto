package admin

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestIconsAllNamesPresent walks the templates and Go sources for icon names
// and asserts each is a key of icons. Names built at run time are covered by
// the `Icon:` literals (nav.go included).
func TestIconsAllNamesPresent(t *testing.T) {
	t.Parallel()

	tmplRx := regexp.MustCompile(`\bicon "([a-z0-9-]+)"`)
	goRx := regexp.MustCompile(`\bIcon:\s*"([a-z0-9-]+)"`)
	used := map[string][]string{}
	add := func(name, where string) { used[name] = append(used[name], where) }

	err := filepath.WalkDir("../../web/admin", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".html") {
			return err
		}
		b, err := os.ReadFile(p)
		for _, m := range tmplRx.FindAllStringSubmatch(string(b), -1) {
			add(m[1], p)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range files {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range goRx.FindAllStringSubmatch(string(b), -1) {
			add(m[1], p)
		}
	}
	if len(used) < 10 {
		t.Fatalf("found only %d icon names; the scan is broken", len(used))
	}
	for name, where := range used {
		if _, ok := icons[name]; !ok {
			t.Errorf("icon %q is not in the icon map (used in %v)", name, where)
		}
	}
}

// TestUnknownIconFailsRender: an unknown icon name aborts the template
// instead of rendering nothing.
func TestUnknownIconFailsRender(t *testing.T) {
	t.Parallel()

	if _, err := iconSVG("nope"); err == nil {
		t.Fatal("iconSVG(nope) returned no error")
	}
	a := newRig(t).a
	if _, err := a.RenderPartial("hm/icon", map[string]any{"Name": "nope"}); err == nil {
		t.Fatal("hm/icon with an unknown name rendered without error")
	}
	if _, err := a.RenderPartial("hm/icon", map[string]any{"Name": "trash"}); err != nil {
		t.Fatalf("known icon failed: %v", err)
	}
}
