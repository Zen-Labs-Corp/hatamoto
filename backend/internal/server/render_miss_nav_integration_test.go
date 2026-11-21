//go:build integration

package server

import (
	"regexp"
	"testing"
)

// The homepage lookup is lazy: the seeded footer menu links one entity, so a
// render runs it once (it used to run twice per page, plus in every menu).
func TestRenderMissHomepageLookup(t *testing.T) {
	e := newMissEnv(t)
	re := regexp.MustCompile(`homepage_entity_id`)
	for _, path := range []string{"/blog/post-01", "/blog", "/news", "/notes"} {
		code, _, stmts := e.missRender(t, path, true)
		if code != 200 {
			t.Fatalf("GET %s = %d", path, code)
		}
		n := countStmts(stmts, re)
		t.Logf("%s: %d statements, %d homepage lookups", path, len(stmts), n)
		if n > 1 {
			t.Errorf("%s: %d homepage lookups, want <= 1", path, n)
		}
	}
}
