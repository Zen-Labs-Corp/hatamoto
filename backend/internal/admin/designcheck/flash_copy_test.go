package designcheck

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Flashes are sentences: a message written as a literal ends with ".", "!" or
// "?" (docs/ADMIN_UI_RULES.md, Copy). The lint reads every non-test file of
// internal/admin and checks the message argument of each SetFlash call:
//
//   - a string literal, or a concatenation ending in one, must end in a stop;
//   - fmt.Sprintf is judged by its format string;
//   - a concatenation with literals that ends in a call or variable is wrong:
//     wrap it in fullStop(...) or sentence(...);
//   - sentence(...), fullStop(...) and plain variables or calls (built elsewhere) are skipped.

// flashAllow lists deliberate exceptions. Each needs a reason.
var flashAllow = []uiAllowEntry{
	{Rule: "FLASH", File: "media.go", Needle: `"Uploaded %d. %s"`, Reason: "the second sentence is the joined per-file problems, each already a sentence"},
}

func lintFlashes(t *testing.T, name string, src []byte) []uiViolation {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, src, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	lines := strings.Split(string(src), "\n")
	var out []uiViolation
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) != 4 {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); !ok || sel.Sel.Name != "SetFlash" {
			return true
		}
		if msg := flashProblem(call.Args[3]); msg != "" {
			line := fset.Position(call.Pos()).Line
			out = append(out, uiViolation{File: name, Line: line, Rule: "FLASH", Msg: msg, Text: lines[line-1]})
		}
		return true
	})
	return out
}

func hasLiteral(e ast.Expr) bool {
	found := false
	ast.Inspect(e, func(n ast.Node) bool {
		if l, ok := n.(*ast.BasicLit); ok && l.Kind == token.STRING {
			found = true
		}
		return !found
	})
	return found
}

func endsInStop(l *ast.BasicLit) bool {
	s, err := strconv.Unquote(l.Value)
	return err == nil && strings.TrimRight(s, " ") != "" && strings.ContainsAny(s[len(s)-1:], ".!?")
}

// flashProblem returns what is wrong with a flash message expression, or "".
func flashProblem(e ast.Expr) string {
	const stop = "a flash is a sentence: it must end with . ! or ?"
	switch x := e.(type) {
	case *ast.BasicLit:
		if x.Kind == token.STRING && !endsInStop(x) {
			return stop
		}
	case *ast.BinaryExpr:
		if x.Op != token.ADD || !hasLiteral(x) {
			return ""
		}
		if l, ok := x.Y.(*ast.BasicLit); ok {
			if !endsInStop(l) {
				return stop
			}
			return ""
		}
		if c, ok := x.Y.(*ast.CallExpr); ok && (isIdent(c.Fun, "sentence") || isIdent(c.Fun, "fullStop")) {
			return ""
		}
		return "a message built from literals and a value does not end with a stop: wrap it in fullStop(...) or sentence(...)"
	case *ast.CallExpr:
		if sel, ok := x.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Sprintf" && len(x.Args) > 0 {
			if l, ok := x.Args[0].(*ast.BasicLit); ok && l.Kind == token.STRING && !endsInStop(l) {
				return stop
			}
		}
	}
	return ""
}

func isIdent(e ast.Expr, name string) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == name
}

func TestFlashCopy(t *testing.T) {
	t.Parallel()

	files, err := filepath.Glob(filepath.Join("..", "*.go"))
	if err != nil || len(files) < 50 {
		t.Fatalf("found %d Go files: wrong directory? (%v)", len(files), err)
	}
	var vs []uiViolation
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		vs = append(vs, lintFlashes(t, filepath.Base(name), src)...)
	}
	left, bad := applyAllow(flashAllow, vs)
	for _, v := range left {
		t.Error(v.String() + "\n\t" + strings.TrimSpace(v.Text))
	}
	for _, b := range bad {
		t.Error(b)
	}
}

func TestFlashCopySelfTest(t *testing.T) {
	t.Parallel()

	src := func(call string) []byte {
		return []byte("package admin\nfunc f() {\n\t" + call + "\n}\n")
	}
	bad := []string{
		`a.SetFlash(w, r, "success", "Saved")`,
		"a.SetFlash(w, r, \"success\", `Saved`)",
		`a.SetFlash(w, r, "error", "Failed: "+err.Error())`,
		`a.SetFlash(w, r, "success", "Added "+name)`,
		`a.SetFlash(w, r, "success", "Added "+name+" ok")`,
		`a.SetFlash(w, r, "success", fmt.Sprintf("Sent %d", n))`,
	}
	for _, c := range bad {
		if got := lintFlashes(t, "x.go", src(c)); len(got) != 1 {
			t.Errorf("%s: want 1 violation, got %d", c, len(got))
		}
	}
	good := []string{
		`a.SetFlash(w, r, "success", "Saved.")`,
		`a.SetFlash(w, r, "success", "Saved!")`,
		`a.SetFlash(w, r, "error", "Failed: "+sentence(err.Error()))`,
		`a.SetFlash(w, r, "success", "Added "+name+".")`,
		`a.SetFlash(w, r, "success", fmt.Sprintf("Sent %d.", n))`,
		`a.SetFlash(w, r, kind, msg)`,
		`a.SetFlash(w, r, "error", err.Error())`,
		`a.Other(w, r, "success", "Saved")`,
	}
	for _, c := range good {
		if got := lintFlashes(t, "x.go", src(c)); len(got) != 0 {
			t.Errorf("%s: want none, got %v", c, got)
		}
	}
	_, stale := applyAllow([]uiAllowEntry{{Rule: "FLASH", File: "x.go", Needle: "nothing here", Reason: "stale"}}, nil)
	if len(stale) != 1 {
		t.Errorf("a stale allowlist entry must fail, got %v", stale)
	}
}
