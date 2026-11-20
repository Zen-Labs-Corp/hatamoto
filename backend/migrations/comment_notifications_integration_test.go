//go:build integration

package migrations_test

import (
	"context"
	"testing"
)

const commentNotificationsVersion = 202611211200

var commentNotificationTables = []string{
	"hatamoto_comment_moderator_notify",
	"hatamoto_comment_notify_queue",
	"hatamoto_comment_reply_queue",
}

// A comment stored before the migration gets notify_replies = false, comments
// stay disabled, and the migration rolls back and applies again.
func TestCommentNotifications_UpDownUp(t *testing.T) {
	ctx := context.Background()
	db, _ := scratchDB(t)
	p := provider(t, db)
	if _, err := p.UpTo(ctx, commentNotificationsVersion-1); err != nil {
		t.Fatalf("up to previous: %v", err)
	}
	const commentID = "44444444-4444-4444-4444-444444444444"
	for _, q := range []string{
		`INSERT INTO hatamoto_entity_schemas (id, name, slug, schema) VALUES ('` + commentsSchemaID + `', 'Note', 'cmn-note', '{"type":"object"}')`,
		`INSERT INTO hatamoto_entities (id, schema_id, name, slug, status) VALUES ('` + commentsEntityID + `', '` + commentsSchemaID + `', 'Hello', 'hello', 'published')`,
		`INSERT INTO hatamoto_comments (id, entity_id, path, depth, body_raw, body_html) VALUES ('` + commentID + `', '` + commentsEntityID + `', '000000000001', 1, 'x', 'x')`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	var notify, enabled bool
	if err := db.QueryRowContext(ctx, `SELECT notify_replies FROM hatamoto_comments WHERE id = $1`, commentID).Scan(&notify); err != nil {
		t.Fatal(err)
	}
	if notify {
		t.Error("existing comment notify_replies = true, want false")
	}
	if err := db.QueryRowContext(ctx, `SELECT bool_or(enabled) FROM hatamoto_comment_settings`).Scan(&enabled); err != nil || enabled {
		t.Errorf("comment settings enabled = %v (err %v), want false", enabled, err)
	}
	for _, tbl := range commentNotificationTables {
		if regclass(t, db, tbl) == nil {
			t.Errorf("%s missing after up", tbl)
		}
	}

	if _, err := p.DownTo(ctx, commentNotificationsVersion-1); err != nil {
		t.Fatalf("down: %v", err)
	}
	for _, tbl := range commentNotificationTables {
		if regclass(t, db, tbl) != nil {
			t.Errorf("%s still exists after down", tbl)
		}
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("second up: %v", err)
	}
	for _, tbl := range commentNotificationTables {
		if regclass(t, db, tbl) == nil {
			t.Errorf("%s missing after second up", tbl)
		}
	}
}

func TestCommentNotifications_Cascades(t *testing.T) {
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
	count := func(table string) (n int) {
		t.Helper()
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM `+table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	const (
		commentID = "44444444-4444-4444-4444-444444444444"
		userID    = "66666666-6666-6666-6666-666666666666"
	)
	exec(`INSERT INTO hatamoto_entity_schemas (id, name, slug, schema) VALUES ($1, 'Note', 'cmn-note', '{"type":"object"}')`, commentsSchemaID)
	exec(`INSERT INTO hatamoto_entities (id, schema_id, name, slug, status) VALUES ($1, $2, 'Hello', 'hello', 'published')`, commentsEntityID, commentsSchemaID)
	exec(`INSERT INTO hatamoto_users (id, email, password_hash, name) VALUES ($1, 'mod@example.test', 'x', 'Mod')`, userID)
	exec(`INSERT INTO hatamoto_comments (id, entity_id, path, depth, body_raw, body_html) VALUES ($1, $2, '000000000001', 1, 'x', 'x')`, commentID, commentsEntityID)
	seed := func() {
		exec(`INSERT INTO hatamoto_comment_moderator_notify (user_id) VALUES ($1) ON CONFLICT DO NOTHING`, userID)
		exec(`INSERT INTO hatamoto_comment_notify_queue (user_id, comment_id) VALUES ($1, $2)`, userID, commentID)
		exec(`INSERT INTO hatamoto_comment_reply_queue (comment_id) VALUES ($1)`, commentID)
	}

	seed()
	exec(`DELETE FROM hatamoto_comments WHERE id = $1`, commentID)
	for _, tbl := range []string{"hatamoto_comment_notify_queue", "hatamoto_comment_reply_queue"} {
		if n := count(tbl); n != 0 {
			t.Errorf("after comment delete: %s rows = %d, want 0", tbl, n)
		}
	}
	if n := count("hatamoto_comment_moderator_notify"); n != 1 {
		t.Errorf("after comment delete: moderator_notify rows = %d, want 1", n)
	}

	exec(`INSERT INTO hatamoto_comments (id, entity_id, path, depth, body_raw, body_html) VALUES ($1, $2, '000000000002', 1, 'x', 'x')`, commentID, commentsEntityID)
	seed()
	exec(`DELETE FROM hatamoto_users WHERE id = $1`, userID)
	for _, tbl := range []string{"hatamoto_comment_moderator_notify", "hatamoto_comment_notify_queue"} {
		if n := count(tbl); n != 0 {
			t.Errorf("after user delete: %s rows = %d, want 0", tbl, n)
		}
	}
	if n := count("hatamoto_comment_reply_queue"); n != 1 {
		t.Errorf("after user delete: reply_queue rows = %d, want 1 (not tied to a user)", n)
	}
}
