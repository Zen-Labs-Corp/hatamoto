//go:build integration

package server

import (
	"regexp"
	"testing"
)

// The related cards' images load in the related fetch (one statement), not
// one statement per card.
func TestRenderMissRelatedImageStatements(t *testing.T) {
	re := regexp.MustCompile(`reverse\(public_url\) LIKE`)
	e := newMissEnv(t)
	for _, c := range []struct {
		path string
		max  int
	}{
		{"/blog/post-01", 2}, // the featured image, then the related cards
		{"/news/post-01", 1}, // no other post: only the featured image
	} {
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
