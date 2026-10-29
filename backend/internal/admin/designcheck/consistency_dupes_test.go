package designcheck

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Primitives the consistency work folded into shared ones (hm/segmented, hm/tabs, hm/badge,
// hm/button, hm/callout, hm/stats with Variant "strip"). A second copy under another name is how
// the admin drifted, so none of these may come back in CSS, JS, templates, goldens or the Go code
// that emits class strings.
//
// Prefixes match any class or token that starts with the name: the deleted CSS was almost all
// sub-classes (hm-aia-kpi__value, hm-aia-seg__label), so a pasted-back copy can rename the root
// and still be caught.
var removedPrefixes = []string{
	"hm-aia-kpi", "hm-aia-att", "hm-aia-seg", "hm-aia-agt", "hm-aia-stack", "hm-aia-g2", "hm-aia-gh",
	"hm-aia-notecard", "hm-aia-back", "hm-bs-act", "hm-bs-warn", "hm-taglist", "hm-parentchip",
	"hm-bs-chip", "hm-bs-badge", "hm-pe__widths", "js/aia/spark.js", "aia/spark", "hm-seg__", "hm-seg--",
}

// Whole tokens only, because a live sibling shares the start: hm-be__tabrow, hm-bs-tab-canvas,
// data-hm-bs-tabs. (data-hm-bs-act and data-hm-bs-warn are template hooks; a name preceded by
// "-" is never a token start, so the prefixes above leave them alone.)
var removedTokens = []string{
	"hm-aia-sectiontitle", "hm-aia-sub", "hm-be__tab", "hm-bs-tab", "data-aia-showall",
	"hm-seg", "hm/seg", // the old setrow pill control and its partial; hm/segmented replaced them
}

func removedPrimitiveRE() *regexp.Regexp {
	quote := func(names []string) string {
		q := make([]string, len(names))
		for i, n := range names {
			q[i] = regexp.QuoteMeta(n)
		}
		return strings.Join(q, "|")
	}
	return regexp.MustCompile(`(^|[^A-Za-z0-9_-])((?:` + quote(removedPrefixes) + `)[A-Za-z0-9_-]*|(?:` + quote(removedTokens) + `)($|[^A-Za-z0-9_-]))`)
}

// removedHit returns the removed name a line uses, or "".
func removedHit(re *regexp.Regexp, line string) string {
	if m := re.FindStringSubmatch(line); m != nil {
		return strings.TrimRight(m[2], " \t\"'<>=/,;:.)}")
	}
	return ""
}

func TestNoDuplicatePrimitives(t *testing.T) {
	re := removedPrimitiveRE()
	roots := []string{
		filepath.Join("..", "..", "..", "web", "admin", "css"),
		filepath.Join("..", "..", "..", "web", "admin", "js"),
		filepath.Join("..", "..", "..", "web", "admin", "pages"),
		filepath.Join("..", "..", "..", "web", "admin", "partials"),
		"..",                            // internal/admin: the Go code that emits class strings; *_test.go files name these on purpose
		filepath.Join("..", "testdata"), // goldens
	}
	for _, root := range roots {
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if root == ".." && p != root { // ".." stays flat: only internal/admin's own files
					return fs.SkipDir
				}
				return nil
			}
			if root == ".." && (!strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go")) {
				return nil
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			for i, line := range strings.Split(string(b), "\n") {
				if n := removedHit(re, line); n != "" {
					t.Errorf("%s:%d uses %q, a primitive that was folded into a shared one (see consistency_dupes_test.go)", p, i+1, n)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

// A copy pasted back from history under a sub-class name must fail; the live siblings must not.
func TestRemovedPrimitivePatternCatchesSubClasses(t *testing.T) {
	re := removedPrimitiveRE()
	for _, line := range []string{
		`<ul class="hm-aia-kpis"><li class="hm-aia-kpicell">`,
		`.hm-aia-kpi__value { font-weight: 400; }`,
		`.hm-aia-seg__input:checked + .hm-aia-seg__label {}`,
		`<span class="hm-aia-att__count">`,
		`<tr class="hm-aia-agt-row">`,
		`<div class="hm-aia-stack-tight">`,
		`<div class="hm-aia-g2--wide">`,
		`<h3 class="hm-aia-gh__sub">`,
		`<a class="hm-aia-notecard__x">`,
		`<a class="hm-aia-back-link">`,
		`<button class="hm-bs-act--ghost">`,
		`<p class="hm-bs-warn-box">`,
		`<ul class="hm-taglist hm-taglist--wrap">`,
		`<span class="hm-parentchip__name">`,
		`<span class="hm-bs-chip-x">`,
		`<span class="hm-bs-badge-x">`,
		`<script src="/admin/js/aia/spark.js"></script>`,
		`<div class="hm-be__tab">`,
		`.hm-seg { display: flex; }`,
		`<span class="hm-seg__opt">`,
		`{{template "hm/seg" .}}`,
	} {
		if removedHit(re, line) == "" {
			t.Errorf("not caught: %s", line)
		}
	}
	for _, line := range []string{
		`<div class="hm-be__tabrow" data-hm-bs-act data-hm-bs-warn data-hm-bs-badge>`,
		`id="hm-bs-tab-canvas" data-hm-bs-tab data-hm-bs-tabs`,
		`<div class="hm-segmented hm-tablist hm-statcard hm-badge hm-aia-chart">`,
		`<span class="hm-segmented__opt">{{template "hm/segmented" .}}`,
	} {
		if n := removedHit(re, line); n != "" {
			t.Errorf("%q is a live name, reported as %q", line, n)
		}
	}
}

func TestSparkJSIsGone(t *testing.T) {
	if _, err := os.Stat(filepath.Join("..", "..", "..", "web", "admin", "js", "aia", "spark.js")); err == nil {
		t.Error("js/aia/spark.js is back; sparklines come from hm/stats (Spark.Live) and the shared chart code")
	}
}
