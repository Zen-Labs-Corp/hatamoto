//go:build integration

package migrations_test

import (
	"context"
	"regexp"
	"testing"
)

const commentsVersion = 202611202100

const (
	commentsSchemaID = "22222222-2222-2222-2222-222222222222"
	commentsEntityID = "33333333-3333-3333-3333-333333333333"
)

// An existing site (schema and entity present) upgraded to comments stays OFF.
func TestComments_ExistingSiteStaysDisabled(t *testing.T) {
	ctx := context.Background()
	db, _ := scratchDB(t)
	p := provider(t, db)
	if _, err := p.UpTo(ctx, commentsVersion-1); err != nil {
		t.Fatal(err)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO hatamoto_entity_schemas (id, name, slug, schema) VALUES ($1, 'Note', 'cmm-note', '{"type":"object"}')`, commentsSchemaID)
	exec(`INSERT INTO hatamoto_entities (id, schema_id, name, slug, status) VALUES ($1, $2, 'Hello', 'hello', 'published')`, commentsEntityID, commentsSchemaID)

	snapshot := func() (string, string) {
		t.Helper()
		var updated, settings string
		if err := db.QueryRowContext(ctx, `SELECT e.updated_at::text, s.settings::text FROM hatamoto_entities e, hatamoto_entity_schemas s WHERE e.id = $1 AND s.id = $2`, commentsEntityID, commentsSchemaID).Scan(&updated, &settings); err != nil {
			t.Fatal(err)
		}
		return updated, settings
	}
	u0, s0 := snapshot()

	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	var n int
	var enabled bool
	if err := db.QueryRowContext(ctx, `SELECT count(*), bool_or(enabled) FROM hatamoto_comment_settings`).Scan(&n, &enabled); err != nil {
		t.Fatal(err)
	}
	if n != 1 || enabled {
		t.Errorf("settings rows = %d enabled = %v, want 1 row, false", n, enabled)
	}
	if u1, s1 := snapshot(); u1 != u0 || s1 != s0 {
		t.Errorf("existing rows changed: updated_at %q -> %q, settings %q -> %q", u0, u1, s0, s1)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM hatamoto_comment_entities`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("hatamoto_comment_entities has %d rows, want none backfilled", n)
	}
}

func TestComments_NoAuthorURLOrRawIPColumn(t *testing.T) {
	ctx := context.Background()
	db, _ := scratchDB(t)
	if _, err := provider(t, db).Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	banned := regexp.MustCompile(`(?i)url|website|^ip$|ip_address|remote_addr`)
	rows, err := db.QueryContext(ctx, `SELECT table_name, column_name FROM information_schema.columns WHERE table_name LIKE 'hatamoto_comment%'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	cols := map[string]bool{}
	for rows.Next() {
		var table, col string
		if err := rows.Scan(&table, &col); err != nil {
			t.Fatal(err)
		}
		cols[table+"."+col] = true
		if banned.MatchString(col) {
			t.Errorf("%s.%s: comments store no URL and no raw IP", table, col)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"hatamoto_comments.ip_hash", "hatamoto_comments.net_hash"} {
		if !cols[want] {
			t.Errorf("column %s missing", want)
		}
	}
	var coll string
	if err := db.QueryRowContext(ctx, `SELECT collation_name FROM information_schema.columns WHERE table_name = 'hatamoto_comments' AND column_name = 'path'`).Scan(&coll); err != nil {
		t.Fatal(err)
	}
	if coll != "C" {
		t.Errorf("path collation = %q, want C (byte order is render order)", coll)
	}
}

func TestComments_SeedAndCascade(t *testing.T) {
	ctx := context.Background()
	db, _ := scratchDB(t)
	if _, err := provider(t, db).Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM hatamoto_comment_blocks WHERE kind = 'email_domain' AND source = 'seed'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n < 100 {
		t.Errorf("seeded disposable domains = %d, want >= 100", n)
	}

	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO hatamoto_entity_schemas (id, name, slug, schema) VALUES ($1, 'Note', 'cmm-note', '{"type":"object"}')`, commentsSchemaID)
	exec(`INSERT INTO hatamoto_entities (id, schema_id, name, slug, status) VALUES ($1, $2, 'Hello', 'hello', 'published')`, commentsEntityID, commentsSchemaID)
	exec(`INSERT INTO hatamoto_comments (entity_id, path, depth, body_raw, body_html) VALUES ($1, '000000000001', 1, 'hi', '<p>hi</p>')`, commentsEntityID)
	exec(`INSERT INTO hatamoto_comment_entities (entity_id, comment_count) VALUES ($1, 1)`, commentsEntityID)

	exec(`DELETE FROM hatamoto_entities WHERE id = $1`, commentsEntityID)
	for _, table := range []string{"hatamoto_comments", "hatamoto_comment_entities"} {
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM `+table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("%s has %d rows after entity delete, want 0", table, n)
		}
	}
}

func TestComments_UpDownUp(t *testing.T) {
	ctx := context.Background()
	db, _ := scratchDB(t)
	p := provider(t, db)
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	if _, err := p.DownTo(ctx, commentsVersion-1); err != nil {
		t.Fatalf("down: %v", err)
	}
	var reg *string
	if err := db.QueryRowContext(ctx, `SELECT to_regclass('hatamoto_comments')::text`).Scan(&reg); err != nil {
		t.Fatal(err)
	}
	if reg != nil {
		t.Errorf("hatamoto_comments still exists after down")
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("second up: %v", err)
	}
	var enabled bool
	if err := db.QueryRowContext(ctx, `SELECT enabled FROM hatamoto_comment_settings`).Scan(&enabled); err != nil {
		t.Fatal(err)
	}
	if enabled {
		t.Error("enabled = true after re-up, want false")
	}
}
