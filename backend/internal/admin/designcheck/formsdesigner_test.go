package designcheck

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

var fdClass = regexp.MustCompile(`(?:^|[^\w-])(hm-fd[\w-]*)`)

// Every hm-fd class the designer page or its scripts name has a rule in
// formsdesigner.css, and the file draws no shadow (selection is a border).
func TestFormsDesignerClassesHaveRules(t *testing.T) {
	t.Parallel()

	admin := filepath.Join("..", "..", "..", "web", "admin")
	css := readAdminCSS(t, "formsdesigner.css")
	have := map[string]bool{}
	for _, m := range cssClass.FindAllStringSubmatch(css, -1) {
		have[m[1]] = true
	}
	if len(have) < 10 {
		t.Fatalf("found %d hm-fd rules: wrong file?", len(have))
	}
	srcs := []string{filepath.Join(admin, "pages", "forms", "design.html"), filepath.Join(admin, "js", "formsdesigner.js")}
	more, err := filepath.Glob(filepath.Join(admin, "js", "forms", "*.js"))
	if err != nil {
		t.Fatal(err)
	}
	srcs = append(srcs, more...)
	used := map[string]string{}
	for _, p := range srcs {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		src := string(b)
		if strings.HasSuffix(p, ".html") {
			src = templateAction.ReplaceAllString(src, " ")
		}
		for _, m := range fdClass.FindAllStringSubmatch(src, -1) {
			// a name that ends in "-" is a prefix the script completes at run time
			c := m[1]
			if !strings.HasSuffix(c, "-") && used[c] == "" {
				used[c] = filepath.Base(p)
			}
		}
	}
	var missing []string
	for c, p := range used {
		if !have[c] {
			missing = append(missing, c+" ("+p+")")
		}
	}
	sort.Strings(missing)
	for _, m := range missing {
		t.Errorf("designer class without a CSS rule: %s", m)
	}
	if strings.Contains(css, "box-shadow") {
		t.Error("formsdesigner.css must not use box-shadow")
	}
}
