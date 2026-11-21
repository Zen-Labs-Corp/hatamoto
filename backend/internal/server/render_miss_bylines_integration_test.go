//go:build integration

package server

import (
	"regexp"
	"testing"
)

// A page's bylines are read once and shared by its JSON-LD and its HTML view.
func TestRenderMissBylineStatements(t *testing.T) {
	credits := regexp.MustCompile(`byline_person_id`)
	people := regexp.MustCompile(`WHERE e\.id = ANY`)
	e := newMissEnv(t)
	for _, c := range []struct {
		path           string
		credits, byIDs int
	}{
		{"/", 1, 0},
		{"/blog/post-01", 1, 1}, // credited to a person
		{"/news/post-01", 1, 0}, // no credit: no person lookup
		{"/blog", 1, -1},        // listing: unchanged, ByIDs not pinned
	} {
		code, _, stmts := e.missRender(t, c.path, true)
		if code != 200 {
			t.Fatalf("GET %s = %d", c.path, code)
		}
		cr, by := countStmts(stmts, credits), countStmts(stmts, people)
		t.Logf("%s: %d statements, %d credit reads, %d person reads", c.path, len(stmts), cr, by)
		if cr != c.credits {
			t.Errorf("%s: %d credit reads, want %d", c.path, cr, c.credits)
		}
		if c.byIDs >= 0 && by != c.byIDs {
			t.Errorf("%s: %d person reads, want %d", c.path, by, c.byIDs)
		}
	}
}
