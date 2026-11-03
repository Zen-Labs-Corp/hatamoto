//go:build integration

package migrations_test

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
)

const changesetsVersion = 202611031000

var originTables = []string{
	"hatamoto_entities", "hatamoto_terms", "hatamoto_menus", "hatamoto_redirects", "hatamoto_media",
}

func columnExists(t *testing.T, db *sql.DB, table, column string) bool {
	t.Helper()
	var n int
	err := db.QueryRowContext(context.Background(),
		`SELECT count(*) FROM information_schema.columns WHERE table_name = $1 AND column_name = $2`,
		table, column).Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	return n > 0
}

// Every pre-existing content row is owned by the site owner after the migration.
func TestChangesetsMigration_ExistingRowsAreOwner(t *testing.T) {
	ctx := context.Background()
	db, _ := scratchDB(t)
	p := provider(t, db)
	// The seed is a pre-1.09 snapshot: load it at the schema it was taken from.
	if _, err := p.UpTo(ctx, killLocalesVersion-1); err != nil {
		t.Fatalf("up to the seed's schema: %v", err)
	}
	seed, err := os.ReadFile("testdata/prodlike_pre_v1_09.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, string(seed)); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	for _, tbl := range originTables {
		var total, foreign int
		q := `SELECT count(*), count(*) FILTER (WHERE origin <> 'owner') FROM ` + tbl
		if err := db.QueryRowContext(ctx, q).Scan(&total, &foreign); err != nil {
			t.Fatalf("%s: %v", tbl, err)
		}
		if foreign != 0 {
			t.Errorf("%s: %d rows with origin <> owner", tbl, foreign)
		}
		t.Logf("%s: %d rows, all owner", tbl, total)
	}
}

// The previous release's binary inserts without origin: the default fills it.
func TestChangesetsMigration_InsertWithoutOriginIsOwner(t *testing.T) {
	ctx := context.Background()
	db, _ := scratchDB(t)
	if _, err := provider(t, db).Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	var origin string
	err := db.QueryRowContext(ctx,
		`INSERT INTO hatamoto_redirects (source_path, target_url, status_code)
		 VALUES ('/old-a', '/new-a', 301) RETURNING origin`).Scan(&origin)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if origin != "owner" {
		t.Errorf("origin = %q, want owner", origin)
	}
}

// The mapping is unique per (origin, kind, source_key), and the new tables exist.
func TestChangesetsMigration_SourceKeyUnique(t *testing.T) {
	ctx := context.Background()
	db, _ := scratchDB(t)
	if _, err := provider(t, db).Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	for _, tbl := range []string{"hatamoto_runs", "hatamoto_changesets", "hatamoto_changeset_items", "hatamoto_run_buffer", "hatamoto_changeset_source_keys"} {
		var reg sql.NullString
		if err := db.QueryRowContext(ctx, `SELECT to_regclass('public.' || $1)::text`, tbl).Scan(&reg); err != nil || !reg.Valid {
			t.Errorf("table %s missing (%v)", tbl, err)
		}
	}
	ins := `INSERT INTO hatamoto_changeset_source_keys (origin, kind, source_key, target_id) VALUES ($1, $2, $3, '1')`
	if _, err := db.ExecContext(ctx, ins, "plugin:a", "entity", "k"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, ins, "plugin:b", "entity", "k"); err != nil {
		t.Errorf("same key under another origin must be allowed: %v", err)
	}
	if _, err := db.ExecContext(ctx, ins, "plugin:a", "entity", "k"); err == nil || !strings.Contains(err.Error(), "duplicate key") {
		t.Errorf("duplicate (origin, kind, source_key) must fail, got %v", err)
	}
}

// Down removes every new column and table; Up again works.
func TestChangesetsMigration_Down(t *testing.T) {
	ctx := context.Background()
	db, _ := scratchDB(t)
	p := provider(t, db)
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	if _, err := p.DownTo(ctx, changesetsVersion-1); err != nil {
		t.Fatalf("down: %v", err)
	}
	for _, tbl := range originTables {
		if columnExists(t, db, tbl, "origin") {
			t.Errorf("%s.origin still exists after down", tbl)
		}
	}
	if columnExists(t, db, "hatamoto_media", "run_id") {
		t.Error("hatamoto_media.run_id still exists after down")
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM pg_tables WHERE tablename IN
		('hatamoto_runs','hatamoto_changesets','hatamoto_changeset_items','hatamoto_run_buffer','hatamoto_changeset_source_keys')`).Scan(&n); err != nil || n != 0 {
		t.Errorf("changeset tables left: %d (%v)", n, err)
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("up again: %v", err)
	}
}
