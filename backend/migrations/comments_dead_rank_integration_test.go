//go:build integration

package migrations_test

import (
	"context"
	"strings"
	"testing"
)

const commentsDeadRankVersion = 202611251300

func TestCommentsDeadRankMigration(t *testing.T) {
	ctx := context.Background()
	db, _ := scratchDB(t)
	p := provider(t, db)
	if _, err := p.UpTo(ctx, commentsDeadRankVersion-1); err != nil {
		t.Fatal(err)
	}
	exec := func(q string, args ...any) error {
		_, err := db.ExecContext(ctx, q, args...)
		return err
	}
	must := func(q string, args ...any) {
		t.Helper()
		if err := exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	must(`INSERT INTO hatamoto_entity_schemas (id, name, slug, schema) VALUES ($1, 'Note', 'cmm-note', '{"type":"object"}')`, commentsSchemaID)
	must(`INSERT INTO hatamoto_entities (id, schema_id, name, slug, status) VALUES ($1, $2, 'Hello', 'hello', 'published')`, commentsEntityID, commentsSchemaID)
	const ins = `INSERT INTO hatamoto_comments (entity_id, parent_id, path, depth, status, body_raw, body_html)
		VALUES ($1, (SELECT id FROM hatamoto_comments WHERE path = $4), $2, $3, $5, 'x', 'x')`
	must(ins, commentsEntityID, "000000000001", 1, "", "approved")
	must(ins, commentsEntityID, "000000000001000000000002", 2, "000000000001", "approved")
	must(ins, commentsEntityID, "000000000003", 1, "", "spam")

	if _, err := p.UpTo(ctx, commentsDeadRankVersion); err != nil {
		t.Fatalf("up: %v", err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM hatamoto_comments WHERE rank = 0 AND dead_by IS NULL`).Scan(&n); err != nil || n != 3 {
		t.Fatalf("rows with rank 0 = %d (%v), want 3", n, err)
	}
	must(ins, commentsEntityID, "000000000004", 1, "", "dead")
	if err := exec(ins, commentsEntityID, "000000000005", 1, "", "bogus"); err == nil {
		t.Error("status 'bogus' accepted, want CHECK violation")
	}

	const roots = `SELECT id FROM hatamoto_comments WHERE entity_id=$1 AND parent_id IS NULL AND status='approved' ORDER BY created_at DESC, seq DESC LIMIT 10`
	rows, err := db.QueryContext(ctx, roots, commentsEntityID)
	if err != nil {
		t.Fatal(err)
	}
	_ = rows.Close()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `SET enable_seqscan = off`); err != nil {
		t.Fatal(err)
	}
	var plan strings.Builder
	prows, err := conn.QueryContext(ctx, "EXPLAIN "+roots, commentsEntityID)
	if err != nil {
		t.Fatal(err)
	}
	for prows.Next() {
		var line string
		if err := prows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		plan.WriteString(line + "\n")
	}
	_ = prows.Close()
	if !strings.Contains(plan.String(), "hatamoto_comments_roots_live_idx") {
		t.Errorf("roots query plan does not use hatamoto_comments_roots_live_idx:\n%s", plan.String())
	}

	must(`INSERT INTO hatamoto_comment_viewer_flags (plugin, commenter_key) VALUES ('example', 'k1')`)
	if err := exec(`INSERT INTO hatamoto_comment_viewer_flags (plugin, commenter_key) VALUES ('example', 'k1')`); err == nil {
		t.Error("duplicate viewer flag accepted, want primary key violation")
	}

	if _, err := p.DownTo(ctx, commentsDeadRankVersion-1); err != nil {
		t.Fatalf("down: %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM hatamoto_comments WHERE status = 'trash'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("trash rows after down = %d (%v), want the dead row demoted", n, err)
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("up again: %v", err)
	}
}
