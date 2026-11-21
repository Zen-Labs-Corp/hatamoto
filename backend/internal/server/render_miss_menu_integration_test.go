//go:build integration

package server

import (
	"context"
	"regexp"
	"strings"
	"testing"
)

// Both menus load in two statements (menus, then their items) however many
// locations the page reads.
func TestRenderMissMenuStatements(t *testing.T) {
	menus, items := regexp.MustCompile(`FROM hatamoto_menus\b`), regexp.MustCompile(`FROM hatamoto_menu_items\b`)

	e := newMissEnv(t)
	for _, path := range []string{"/", "/blog/post-01", "/blog", "/notes"} {
		code, body, stmts := e.missRender(t, path, true)
		if code != 200 {
			t.Fatalf("GET %s = %d", path, code)
		}
		m, i := countStmts(stmts, menus), countStmts(stmts, items)
		t.Logf("%s: %d statements, %d menu, %d item", path, len(stmts), m, i)
		if m != 1 || i != 1 {
			t.Errorf("%s: %d menu and %d item statements, want 1 and 1", path, m, i)
		}
		if !strings.Contains(body, "Elsewhere") || !strings.Contains(body, "About") {
			t.Errorf("%s: seeded menu items missing from the page", path)
		}
	}

	t.Run("no menus", func(t *testing.T) {
		e := newMissEnv(t)
		if _, err := e.srv.db.Pool().Exec(context.Background(), `DELETE FROM hatamoto_menus`); err != nil {
			t.Fatal(err)
		}
		code, body, stmts := e.missRender(t, "/blog", true)
		if code != 200 {
			t.Fatalf("GET /blog = %d", code)
		}
		m, i := countStmts(stmts, menus), countStmts(stmts, items)
		t.Logf("/blog without menus: %d statements, %d menu, %d item", len(stmts), m, i)
		if m != 1 || i != 0 {
			t.Errorf("%d menu and %d item statements, want 1 and 0", m, i)
		}
		if strings.Contains(body, "Elsewhere") {
			t.Error("a deleted menu still renders")
		}
	})
}
