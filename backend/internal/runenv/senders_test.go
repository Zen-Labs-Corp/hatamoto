package runenv_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/runenv"
)

// backendRoot returns the module root. `go test` runs with the package
// directory as cwd (also under -trimpath and on remote builders), so walk up
// from it to the directory holding go.mod.
func backendRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the test directory")
		}
		dir = parent
	}
}

// declaredFamilies returns every constant of type Family declared in runenv.go.
func declaredFamilies(t *testing.T, root string) map[string]bool {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(root, "internal", "runenv", "runenv.go"), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, s := range gd.Specs {
			vs := s.(*ast.ValueSpec)
			if id, ok := vs.Type.(*ast.Ident); ok && id.Name == "Family" {
				for _, n := range vs.Names {
					out[n.Name] = true
				}
			}
		}
	}
	return out
}

// TestEverySenderFamilyIsGatedAndTested fails when a Family has no
// runenv.Suppress call site in production code or no test referencing it.
func TestEverySenderFamilyIsGatedAndTested(t *testing.T) {
	root := backendRoot(t)
	families := declaredFamilies(t, root)
	if len(families) == 0 {
		t.Fatal("no Family constants found in runenv.go")
	}

	listed := map[string]bool{}
	for _, f := range runenv.Families() {
		listed[string(f)] = true
	}
	if len(listed) != len(families) {
		t.Fatalf("runenv.Families() lists %d families but runenv.go declares %d constants; keep them in sync", len(listed), len(families))
	}

	runenvDir := filepath.Join(root, "internal", "runenv")
	suppressed := map[string]bool{}
	tested := map[string]bool{}
	gatedDirs := map[string]map[string]bool{}
	var testFiles []struct {
		dir  string
		refs map[string]bool
	}

	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case "testdata", "vendor", "node_modules", ".git":
				return filepath.SkipDir
			}
			if path == runenvDir {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return perr
		}
		isTest := strings.HasSuffix(path, "_test.go")
		refs := map[string]bool{}
		ast.Inspect(f, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.CallExpr:
				if isTest {
					return true
				}
				sel, ok := x.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "Suppress" || !isIdent(sel.X, "runenv") {
					return true
				}
				for _, a := range x.Args {
					if name, ok := runenvSelector(a); ok {
						suppressed[name] = true
						if gatedDirs[filepath.Dir(path)] == nil {
							gatedDirs[filepath.Dir(path)] = map[string]bool{}
						}
						gatedDirs[filepath.Dir(path)][name] = true
					}
				}
			case *ast.SelectorExpr:
				if isTest {
					if name, ok := runenvSelector(x); ok {
						refs[name] = true
					}
				}
			}
			return true
		})
		if isTest {
			testFiles = append(testFiles, struct {
				dir  string
				refs map[string]bool
			}{filepath.Dir(path), refs})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, tf := range testFiles {
		for name := range tf.refs {
			if gatedDirs[tf.dir][name] {
				tested[name] = true
			}
		}
	}

	var noGate, noTest []string
	for name := range families {
		if !suppressed[name] {
			noGate = append(noGate, name)
		} else if !tested[name] {
			noTest = append(noTest, name)
		}
	}
	sort.Strings(noGate)
	sort.Strings(noTest)
	if len(noGate) > 0 {
		t.Errorf("families without a runenv.Suppress call in non-test code: %v; add runenv.Suppress at the sender's lowest send point", noGate)
	}
	if len(noTest) > 0 {
		t.Errorf("families without a test referencing runenv.<Family> next to their gate: %v; add a staging-mode test", noTest)
	}
}

func isIdent(e ast.Expr, name string) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == name
}

func runenvSelector(e ast.Expr) (string, bool) {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok || !isIdent(sel.X, "runenv") {
		return "", false
	}
	return sel.Sel.Name, true
}
