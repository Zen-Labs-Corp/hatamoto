package authz_test

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"hatamoto.dev/core/authz"
)

type coverageRow struct {
	file    string
	kind    string
	actions []string
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
		if len(fields) != 3 || (fields[1] != "group" && fields[1] != "inline") {
			t.Fatalf("coverage.txt:%d: want <path> group|inline <actions>", n)
		}
		rows = append(rows, coverageRow{fields[0], fields[1], strings.Split(fields[2], ",")})
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return rows
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
