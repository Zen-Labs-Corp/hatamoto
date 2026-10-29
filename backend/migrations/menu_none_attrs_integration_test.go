//go:build integration

package migrations_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/google/uuid"
)

const menuNoneAttrsVersion = 202610291200

func TestMenuNoneAttrs(t *testing.T) {
	ctx := context.Background()
	db, _ := scratchDB(t)
	p := provider(t, db)
	if _, err := p.UpTo(ctx, menuNoneAttrsVersion-1); err != nil {
		t.Fatalf("up to previous: %v", err)
	}

	menu := uuid.New()
	if _, err := db.ExecContext(ctx, `INSERT INTO hatamoto_menus (id, name, slug, location) VALUES ($1, 'Footer', 'footer', 'footer')`, menu); err != nil {
		t.Fatalf("insert menu: %v", err)
	}
	insert := func(label, linkType string, url any) {
		t.Helper()
		if _, err := db.ExecContext(ctx,
			`INSERT INTO hatamoto_menu_items (menu_id, label, link_type, url) VALUES ($1, $2, $3, $4)`,
			menu, label, linkType, url); err != nil {
			t.Fatalf("insert %s: %v", label, err)
		}
	}
	insert("hash", "custom", "#")
	insert("path", "custom", "/x")
	insert("group", "custom", nil)
	insert("ext", "external", "https://example.com")
	insert("arch", "archive", "#")

	type item struct {
		linkType string
		url      sql.NullString
	}
	read := func() map[string]item {
		t.Helper()
		rows, err := db.QueryContext(ctx, `SELECT label, link_type, url FROM hatamoto_menu_items`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		out := map[string]item{}
		for rows.Next() {
			var label string
			var it item
			if err := rows.Scan(&label, &it.linkType, &it.url); err != nil {
				t.Fatal(err)
			}
			out[label] = it
		}
		return out
	}
	check := func(got map[string]item, want map[string]string) {
		t.Helper()
		for label, lt := range want {
			if got[label].linkType != lt {
				t.Errorf("%s link_type = %q, want %q", label, got[label].linkType, lt)
			}
		}
	}
	hashLeft := func() int {
		t.Helper()
		var n int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM hatamoto_menu_items WHERE url = '#' AND link_type = 'custom'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	if _, err := p.UpTo(ctx, menuNoneAttrsVersion); err != nil {
		t.Fatalf("up: %v", err)
	}
	got := read()
	check(got, map[string]string{"hash": "none", "path": "custom", "group": "custom", "ext": "external", "arch": "archive"})
	if got["hash"].url.Valid {
		t.Errorf("none item kept url %q", got["hash"].url.String)
	}
	if n := hashLeft(); n != 0 {
		t.Errorf("custom '#' items after up = %d, want 0", n)
	}
	var attrs string
	if err := db.QueryRowContext(ctx, `SELECT attrs::text FROM hatamoto_menu_items WHERE label = 'path'`).Scan(&attrs); err != nil || attrs != "{}" {
		t.Errorf("attrs default = %q (%v), want {}", attrs, err)
	}
	insert("fresh", "none", nil)

	if _, err := p.DownTo(ctx, menuNoneAttrsVersion-1); err != nil {
		t.Fatalf("down: %v", err)
	}
	got = read()
	check(got, map[string]string{"hash": "custom", "fresh": "custom", "path": "custom", "group": "custom"})
	if got["hash"].url.String != "#" || got["fresh"].url.String != "#" {
		t.Errorf("down did not restore '#': %+v", got)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO hatamoto_menu_items (menu_id, label, link_type) VALUES ($1, 'bad', 'none')`, menu); err == nil {
		t.Error("old CHECK accepted link_type none after down")
	}

	if _, err := p.UpTo(ctx, menuNoneAttrsVersion); err != nil {
		t.Fatalf("up again: %v", err)
	}
	check(read(), map[string]string{"hash": "none", "fresh": "none"})
	// Re-applying the statements is a no-op.
	if _, err := db.ExecContext(ctx,
		`UPDATE hatamoto_menu_items SET link_type = 'none', url = NULL WHERE link_type = 'custom' AND url = '#'`); err != nil {
		t.Fatal(err)
	}
	if n := hashLeft(); n != 0 {
		t.Errorf("custom '#' items after second up = %d", n)
	}
}
