//go:build integration

package migrations_test

import (
	"context"
	"testing"
)

const entitySearchCJKVersion = 202611251600

// The bigram search vector rebuilds existing rows, goes down to the plain
// vector and comes back (#412).
func TestEntitySearchCJK_UpDownUp(t *testing.T) {
	ctx := context.Background()
	db, _ := scratchDB(t)
	p := provider(t, db)
	if _, err := p.UpTo(ctx, entitySearchCJKVersion-1); err != nil {
		t.Fatalf("up to %d: %v", entitySearchCJKVersion-1, err)
	}
	var schema string
	if err := db.QueryRowContext(ctx, `INSERT INTO hatamoto_entity_schemas (name, slug, schema, settings)
		VALUES ('E', 'event', '{"type":"object"}', '{}') RETURNING id::text`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO hatamoto_entities (schema_id, name, slug, attributes)
		VALUES ($1, '南房総ジャズフェスティバル 2026', 'jazz', '{"content":"<p>Live music</p>"}')`, schema); err != nil {
		t.Fatal(err)
	}
	count := func(q string) int {
		t.Helper()
		var n int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM hatamoto_entities WHERE search_vector @@ to_tsquery('simple', $1)`, q).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	const jazz = `'ジャ' <-> 'ャズ' <-> 'ズ':*`
	if count(jazz) != 0 || count("music") != 1 {
		t.Fatalf("before: the plain vector already splits the run or lost the Latin words")
	}
	if _, err := p.UpTo(ctx, entitySearchCJKVersion); err != nil {
		t.Fatalf("up: %v", err)
	}
	if count(jazz) != 1 || count("music") != 1 || count("'2026'") != 1 {
		t.Errorf("after up: the existing row was not rebuilt with bigrams")
	}
	if _, err := p.DownTo(ctx, entitySearchCJKVersion-1); err != nil {
		t.Fatalf("down: %v", err)
	}
	var fn *string
	if err := db.QueryRowContext(ctx, `SELECT to_regprocedure('hatamoto_search_text(text, boolean)')::text`).Scan(&fn); err != nil {
		t.Fatal(err)
	}
	if fn != nil || count(jazz) != 0 || count("music") != 1 {
		t.Errorf("after down: function %v, or the vector is not the plain one", fn)
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("second up: %v", err)
	}
	if count(jazz) != 1 {
		t.Error("after the second up: no bigrams")
	}
}
