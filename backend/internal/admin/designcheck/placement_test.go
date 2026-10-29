package designcheck

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// Static design checks live in this package so `make design-check` (this package
// plus internal/admin/a11y) runs every one of them in seconds, after every edit
// (scripts/check-changed.sh) and before every push. A check that reads the admin
// CSS from a test in internal/admin would only run with the slow package, so it
// fails here unless it also needs the admin server, and then it is named below.

// cssSourceRead matches a test that reads admin CSS source by path.
var cssSourceRead = regexp.MustCompile(`web/admin/css/|"admin", "css"|adminAsset\(t, "css"|readWeb\(t, "css/`)

// placementAllow names internal/admin tests that read admin CSS and must stay
// there: they render through the admin server first. Each needs a reason.
var placementAllow = map[string]string{
	"menus_test.go TestMenuNoJSShowsOnlyTheTargetOfItsType": "posts the menu form to check the no-JS markup, then checks the CSS that relies on it",
	"dashboard_test.go TestDashboardGridLayout":             "renders the dashboard as owner and editor, then checks the grid CSS against the rendered sections",
}

func TestDesignChecksLiveHere(t *testing.T) {
	t.Parallel()

	files, err := filepath.Glob(filepath.Join("..", "*_test.go"))
	if err != nil || len(files) < 50 {
		t.Fatalf("found %d internal/admin test files: wrong directory? (%v)", len(files), err)
	}
	seen := map[string]bool{}
	for _, f := range files {
		fset := token.NewFileSet()
		src, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		body := readSrc(t, f)
		for _, d := range src.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || !strings.HasPrefix(fn.Name.Name, "Test") {
				continue
			}
			text := body[fset.Position(fn.Pos()).Offset:fset.Position(fn.End()).Offset]
			if !cssSourceRead.MatchString(text) {
				continue
			}
			key := filepath.Base(f) + " " + fn.Name.Name
			if _, ok := placementAllow[key]; ok {
				seen[key] = true
				continue
			}
			t.Errorf("%s:%d %s reads admin CSS source: move it to internal/admin/designcheck (or, if it must render through the server, add it to placementAllow with a reason)",
				filepath.ToSlash(f), fset.Position(fn.Pos()).Line, fn.Name.Name)
		}
	}
	var stale []string
	for key, reason := range placementAllow {
		if strings.TrimSpace(reason) == "" {
			t.Errorf("placementAllow %q has no reason", key)
		}
		if !seen[key] {
			stale = append(stale, key)
		}
	}
	sort.Strings(stale)
	for _, key := range stale {
		t.Errorf("placementAllow %q matches nothing: remove it", key)
	}
}

// The package stays fast because it never compiles internal/admin: the checks
// read files, they do not call the admin code.
func TestDesignCheckDoesNotImportAdmin(t *testing.T) {
	t.Parallel()

	files, err := filepath.Glob("*_test.go")
	if err != nil || len(files) < 10 {
		t.Fatalf("found %d files: wrong directory? (%v)", len(files), err)
	}
	for _, f := range files {
		src, err := parser.ParseFile(token.NewFileSet(), f, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range src.Imports {
			if p, _ := strconv.Unquote(imp.Path.Value); p == "hatamoto.dev/core/internal/admin" {
				t.Errorf("%s imports internal/admin; design checks read files only", f)
			}
		}
	}
}
