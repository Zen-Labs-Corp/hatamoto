//go:build integration

package server

import (
	"regexp"
	"testing"
)

// The variants of every card image load in one statement, however many cards
// the page shows, and a page without images reads none.
func TestRenderMissImageStatements(t *testing.T) {
	re := regexp.MustCompile(`reverse\(public_url\) LIKE`)
	e := newMissEnv(t)
	for _, c := range []struct {
		path string
		max  int
	}{{"/blog", 1}, {"/news", 1}, {"/notes", 0}} {
		code, _, stmts := e.missRender(t, c.path, true)
		if code != 200 {
			t.Fatalf("GET %s = %d", c.path, code)
		}
		n := countStmts(stmts, re)
		t.Logf("%s: %d statements, %d variant lookups", c.path, len(stmts), n)
		if n > c.max {
			t.Errorf("%s: %d variant statements, want <= %d", c.path, n, c.max)
		}
	}
}
