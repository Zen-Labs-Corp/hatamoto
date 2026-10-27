package admin

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

var (
	templateAction = regexp.MustCompile(`(?s)\{\{.*?\}\}`)
	classAttr      = regexp.MustCompile(`class="([^"]*)"`)
	cssClass       = regexp.MustCompile(`\.(hm-[A-Za-z0-9_-]+)`)
)

// unstyledHooks are hm- classes templates carry that have no rule today:
// structural or test hooks. Do not add to this list:
// give the class a rule instead. Found when this test was written (v1-23a).
var unstyledHooks = map[string]bool{
	"hm-be__crawl":           true,
	"hm-be__tools":           true,
	"hm-bp":                  true,
	"hm-bp__ld":              true,
	"hm-diff__row":           true,
	"hm-editor-page":         true,
	"hm-entityform":          true,
	"hm-formbuilder":         true,
	"hm-menuform":            true,
	"hm-menutree__target":    true,
	"hm-pagination__summary": true,
	"hm-pe__empty":           true,
	"hm-pe__tools":           true,
	"hm-plugin":              true,
	"hm-schemaform":          true,
}

// Every hm- class a template uses has a CSS rule. A rule deleted while its
// templates stay (hm-btn--danger-text once lost its colour that way) fails here.
func TestTemplateClassesHaveCSSRules(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..", "web", "admin")
	have := map[string]bool{}
	used := map[string]string{} // class -> first template
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		switch {
		case strings.HasSuffix(p, ".css"):
			for _, m := range cssClass.FindAllStringSubmatch(string(b), -1) {
				have[m[1]] = true
			}
		case strings.HasSuffix(p, ".html"):
			// {{...}} actions build names at render time; only literal words count.
			src := templateAction.ReplaceAllString(string(b), " ")
			for _, m := range classAttr.FindAllStringSubmatch(src, -1) {
				for _, c := range strings.Fields(m[1]) {
					if strings.HasPrefix(c, "hm-") && !strings.HasSuffix(c, "-") && used[c] == "" {
						used[c] = p
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(have) < 100 || len(used) < 100 {
		t.Fatalf("found %d css classes and %d template classes: wrong root?", len(have), len(used))
	}
	var missing []string
	for c, p := range used {
		if !have[c] && !unstyledHooks[c] {
			missing = append(missing, c+" ("+p+")")
		}
	}
	sort.Strings(missing)
	for _, m := range missing {
		t.Errorf("template class without a CSS rule: %s", m)
	}
}
