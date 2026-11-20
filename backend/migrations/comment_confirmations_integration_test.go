//go:build integration

package migrations_test

import (
	"context"
	"database/sql"
	"testing"
)

const commentConfirmationsVersion = 202611202200

func regclass(t *testing.T, db *sql.DB, name string) *string {
	t.Helper()
	var reg *string
	if err := db.QueryRowContext(context.Background(), `SELECT to_regclass($1)::text`, name).Scan(&reg); err != nil {
		t.Fatal(err)
	}
	return reg
}

// A fresh install runs the full chain from zero: the new table sorts after the
// comments tables it references, and rolls back without touching them.
func TestCommentConfirmations_UpDownUp(t *testing.T) {
	ctx := context.Background()
	db, _ := scratchDB(t)
	p := provider(t, db)
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	if regclass(t, db, "hatamoto_comment_confirmations") == nil {
		t.Fatal("hatamoto_comment_confirmations missing after up")
	}
	if _, err := p.DownTo(ctx, commentConfirmationsVersion-1); err != nil {
		t.Fatalf("down: %v", err)
	}
	if regclass(t, db, "hatamoto_comment_confirmations") != nil {
		t.Error("hatamoto_comment_confirmations still exists after down")
	}
	if regclass(t, db, "hatamoto_comments") == nil {
		t.Error("hatamoto_comments gone after down of the confirmations migration")
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("second up: %v", err)
	}
	if regclass(t, db, "hatamoto_comment_confirmations") == nil {
		t.Error("hatamoto_comment_confirmations missing after second up")
	}
}

func TestCommentConfirmations_LeavesCommentsDisabled(t *testing.T) {
	ctx := context.Background()
	db, _ := scratchDB(t)
	if _, err := provider(t, db).Up(ctx); err != nil {
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
}

func TestCommentConfirmations_Cascades(t *testing.T) {
	ctx := context.Background()
	db, _ := scratchDB(t)
	if _, err := provider(t, db).Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	const commentID = "44444444-4444-4444-4444-444444444444"
	const commenterID = "55555555-5555-5555-5555-555555555555"
	exec(`INSERT INTO hatamoto_entity_schemas (id, name, slug, schema) VALUES ($1, 'Note', 'cmm-note', '{"type":"object"}')`, commentsSchemaID)
	exec(`INSERT INTO hatamoto_entities (id, schema_id, name, slug, status) VALUES ($1, $2, 'Hello', 'hello', 'published')`, commentsEntityID, commentsSchemaID)
	exec(`INSERT INTO hatamoto_commenters (id, email_hash, email_sealed) VALUES ($1, 'h', '\x00')`, commenterID)
	exec(`INSERT INTO hatamoto_comments (id, entity_id, commenter_id, path, depth, body_raw, body_html) VALUES ($1, $2, $3, '000000000001', 1, 'x', 'x')`, commentID, commentsEntityID, commenterID)
	exec(`INSERT INTO hatamoto_comment_confirmations (token_hash, comment_id, commenter_id, expires_at) VALUES (decode(repeat('ab', 32), 'hex'), $1, $2, now() + interval '1 day')`, commentID, commenterID)

	count := func() (n int, nullComment bool) {
		t.Helper()
		if err := db.QueryRowContext(ctx, `SELECT count(*), COALESCE(bool_and(comment_id IS NULL), false) FROM hatamoto_comment_confirmations`).Scan(&n, &nullComment); err != nil {
			t.Fatal(err)
		}
		return n, nullComment
	}
	exec(`DELETE FROM hatamoto_comments WHERE id = $1`, commentID)
	if n, null := count(); n != 1 || !null {
		t.Errorf("after comment delete: rows = %d, comment_id null = %v, want 1 row with NULL comment_id", n, null)
	}
	exec(`DELETE FROM hatamoto_commenters WHERE id = $1`, commenterID)
	if n, _ := count(); n != 0 {
		t.Errorf("after commenter delete: rows = %d, want 0", n)
	}
}
