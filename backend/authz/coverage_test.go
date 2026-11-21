package authz_test

import (
	"bufio"
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"hatamoto.dev/core/authz"
)

type coverageRow struct {
	file    string
	kind    string
	actions []string
	roles   string // the roles= field: owner or owner,editor; empty on other rows
	n       int    // role-check lines the row accounts for (the n= field)
}

func readCoverage(t *testing.T) []coverageRow {
	t.Helper()
	f, err := os.Open("testdata/coverage.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var rows []coverageRow
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line, _, _ := strings.Cut(sc.Text(), "#")
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) < 3 || len(fields) > 5 {
			t.Fatalf("coverage.txt:%d: want <path> group|inline|flag|other <actions> [roles=<roles>] [n=<count>]", n)
		}
		switch fields[1] {
		case "group", "inline", "flag", "other":
		default:
			t.Fatalf("coverage.txt:%d: unknown row kind %q", n, fields[1])
		}
		row := coverageRow{file: fields[0], kind: fields[1], actions: strings.Split(fields[2], ",")}
		if fields[2] == "-" {
			if row.kind != "other" {
				t.Fatalf("coverage.txt:%d: only other rows may have no action", n)
			}
			row.actions = nil
		}
		rest := fields[3:]
		if len(rest) > 0 && strings.HasPrefix(rest[0], "roles=") {
			row.roles = strings.TrimPrefix(rest[0], "roles=")
			rest = rest[1:]
		}
		switch {
		case row.kind == "other" && row.roles != "":
			t.Fatalf("coverage.txt:%d: other rows take no roles=", n)
		case row.kind != "other" && row.roles != "owner" && row.roles != "owner,editor":
			t.Fatalf("coverage.txt:%d: want roles=owner or roles=owner,editor", n)
		}
		if len(rest) > 1 {
			t.Fatalf("coverage.txt:%d: unexpected field %q", n, rest[1])
		}
		if len(rest) == 1 {
			v, ok := strings.CutPrefix(rest[0], "n=")
			c, err := strconv.Atoi(v)
			if !ok || err != nil || c < 1 || row.kind == "group" {
				t.Fatalf("coverage.txt:%d: want n=<count> on an inline, flag or other row", n)
			}
			row.n = c
		}
		rows = append(rows, row)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return rows
}

// catalogueConstants returns the Action* constants declared in actions.go.
func catalogueConstants(t *testing.T) map[string]bool {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "actions.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, sp := range gd.Specs {
			vs := sp.(*ast.ValueSpec)
			for i, name := range vs.Names {
				if !strings.HasPrefix(name.Name, "Action") || i >= len(vs.Values) {
					continue
				}
				lit, ok := vs.Values[i].(*ast.BasicLit)
				if !ok {
					t.Fatalf("actions.go: %s is not a string literal", name.Name)
				}
				v, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatal(err)
				}
				got[v] = true
			}
		}
	}
	return got
}

// TestCatalogueConstantsMatchTable fails when an Action constant has no row in
// the default table, or the table has an action without a constant.
func TestCatalogueConstantsMatchTable(t *testing.T) {
	consts := catalogueConstants(t)
	if len(consts) == 0 {
		t.Fatal("no Action constants found in actions.go")
	}
	inTable := map[string]bool{}
	for _, a := range authz.Actions() {
		inTable[string(a)] = true
		if !consts[string(a)] {
			t.Errorf("action %q is in the default table but has no Action constant", a)
		}
	}
	for c := range consts {
		if !inTable[c] {
			t.Errorf("action constant %q has no entry in the default table (default.go)", c)
		}
	}
}

func TestCoverageRowsAreValid(t *testing.T) {
	known := map[string]bool{}
	for _, a := range authz.Actions() {
		known[string(a)] = true
	}
	cited := map[string]bool{}
	for _, r := range readCoverage(t) {
		if _, err := os.Stat(filepath.Join("..", r.file)); err != nil {
			t.Errorf("%s: file does not exist (%v)", r.file, err)
		}
		for _, a := range r.actions {
			if !known[a] {
				t.Errorf("%s: unknown action %q", r.file, a)
			}
			cited[a] = true
		}
	}
	for a := range known {
		if !cited[a] {
			t.Errorf("action %q is not cited by any row in authz/testdata/coverage.txt", a)
		}
	}
}

// TestCoverageRolesMatchDefaults fails when a row's roles= field differs from
// the default answer of an action it cites, for an owner and for an editor
// session. It catches a check mapped to an action that answers differently.
func TestCoverageRolesMatchDefaults(t *testing.T) {
	ctx := context.Background()
	owner := authz.Subject{UserID: "u1", Role: "owner"}
	editor := authz.Subject{UserID: "u2", Role: "editor"}
	d := authz.Default()
	for _, r := range readCoverage(t) {
		for _, a := range r.actions {
			for _, c := range []struct {
				who  string
				subj authz.Subject
			}{{"owner", owner}, {"editor", editor}} {
				got, err := d.Can(ctx, c.subj, authz.Action(a), authz.Object{})
				if err != nil {
					t.Fatalf("%s: %s: %v", r.file, a, err)
				}
				want := c.who == "owner" || r.roles == "owner,editor"
				if got != want {
					t.Errorf("%s: row says roles=%s but %s answers %v for an %s; map the check to an action with the same default, or add one", r.file, r.roles, a, got, c.who)
				}
			}
		}
	}
}

var gateLine = regexp.MustCompile(`RequireRole\(|RequireOwnerOrKeyScope\(`)

// TestCoverageGroupRowsMatchGates fails when a RequireRole group is added or
// removed in the admin or server without a matching row.
func TestCoverageGroupRowsMatchGates(t *testing.T) {
	rows := map[string]int{}
	for _, r := range readCoverage(t) {
		if r.kind == "group" {
			rows[r.file]++
		}
	}
	gates := map[string]int{}
	for _, dir := range []string{"internal/admin", "internal/server"} {
		files, err := filepath.Glob(filepath.Join("..", dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range files {
			if strings.HasSuffix(p, "_test.go") {
				continue
			}
			src, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			n := 0
			for _, l := range strings.Split(string(src), "\n") {
				l = strings.TrimSpace(l)
				if strings.HasPrefix(l, "func ") || strings.HasPrefix(l, "//") {
					continue
				}
				if gateLine.MatchString(l) {
					n++
				}
			}
			if n > 0 {
				gates[dir+"/"+filepath.Base(p)] = n
			}
		}
	}
	for file, n := range gates {
		if rows[file] != n {
			t.Errorf("%s: %d RequireRole gates, %d group rows in authz/testdata/coverage.txt; add a row (and an action if none fits)", file, n, rows[file])
		}
	}
	for file, m := range rows {
		if gates[file] == 0 {
			t.Errorf("%s: %d group rows in authz/testdata/coverage.txt but no RequireRole gates", file, m)
		}
	}
}

var roleCheckLine = regexp.MustCompile(`[Rr]ole\s*(==|!=)\s*(string\()?(user\.Role(Owner|Editor)|"owner"|"editor")|requireOwner\(`)

// countRoleChecks counts, per file, the lines that compare a role with owner or
// editor or call requireOwner, ignoring comments, func declarations and the
// RequireRole lines the group rows count.
func countRoleChecks(t *testing.T, src string) int {
	t.Helper()
	n := 0
	for _, l := range strings.Split(src, "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "func ") || strings.HasPrefix(l, "//") || gateLine.MatchString(l) {
			continue
		}
		if roleCheckLine.MatchString(l) {
			n++
		}
	}
	return n
}

// TestCoverageInlineRowsMatchRoleChecks fails when an admin or server file has
// more or fewer role checks than its inline, flag and other rows account for, so
// a new hard-coded owner check cannot go unlisted.
func TestCoverageInlineRowsMatchRoleChecks(t *testing.T) {
	rows := map[string]int{}
	for _, r := range readCoverage(t) {
		rows[r.file] += r.n
	}
	seen := map[string]bool{}
	for _, dir := range []string{"internal/admin", "internal/server"} {
		files, err := filepath.Glob(filepath.Join("..", dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range files {
			if strings.HasSuffix(p, "_test.go") {
				continue
			}
			src, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			file := dir + "/" + filepath.Base(p)
			seen[file] = true
			if got := countRoleChecks(t, string(src)); got != rows[file] {
				t.Errorf("%s: %d role checks in the source, %d accounted for by n= in authz/testdata/coverage.txt; add or update an inline, flag or other row", file, got, rows[file])
			}
		}
	}
	for file, n := range rows {
		if n > 0 && !seen[file] {
			t.Errorf("%s: n=%d in authz/testdata/coverage.txt but the file is not in internal/admin or internal/server", file, n)
		}
	}
}

// TestCountRoleChecksSeesAMissingRow guards the counter itself: an owner check
// in a new handler is counted, a comment and a func line are not.
func TestCountRoleChecksSeesAMissingRow(t *testing.T) {
	src := "// u.Role == \"owner\"\nfunc (a *Admin) requireOwner() {}\nif u.Role != \"owner\" {\n}\nif !a.requireOwner(w, r) {\n}\nx := RequireRole(\"owner\")\n"
	if got := countRoleChecks(t, src); got != 2 {
		t.Errorf("got %d, want 2", got)
	}
}
