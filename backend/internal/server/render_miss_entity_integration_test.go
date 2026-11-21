//go:build integration

package server

import (
	"context"
	"regexp"
	"strings"
	"testing"
)

// The resolver's row is the page's entity: it is not read a second time by id.
func TestRenderMissEntityReadOnce(t *testing.T) {
	byID := regexp.MustCompile(`WHERE e\.id = \$1 AND e\.status = 'published'`)
	e := newMissEnv(t)
	// The seeded footer menu links an entity, which is itself a by-id read.
	if _, err := e.srv.db.Pool().Exec(context.Background(), `DELETE FROM hatamoto_menus`); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/blog/post-01", "/news/post-01", "/about"} {
		code, body, stmts := e.missRender(t, path, true)
		if code != 200 {
			t.Fatalf("GET %s = %d", path, code)
		}
		n := countStmts(stmts, byID)
		t.Logf("%s: %d statements, %d by-id reads", path, len(stmts), n)
		if n != 0 {
			t.Errorf("%s: %d by-id entity reads, want 0", path, n)
		}
		if !strings.Contains(body, "<h1") {
			t.Errorf("%s: no page rendered", path)
		}
	}
}
