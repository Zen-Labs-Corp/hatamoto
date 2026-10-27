//go:build integration

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// A6: `builder convert` against a scratch, migrated database:
//
//	DATABASE_URL=... go test -tags integration ./app -run BuilderConvert
func TestBuilderConvert_Integration(t *testing.T) {
	dsn, pool := scratchDB(t)
	ctx := context.Background()
	if err := migrateUp(ctx, pool, quietLogger()); err != nil {
		t.Fatal(err)
	}
	schemaID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO hatamoto_entity_schemas (id,name,slug,schema,settings) VALUES ($1,'conv','conv','{"type":"object"}','{}')`, schemaID); err != nil {
		t.Fatal(err)
	}
	pages := map[string]string{
		// out of order sort_order, a hidden section, an html section
		"ooo": `{"page_sections":[
		  {"id":"b","type":"content","sort_order":3,"content":{"body":"<p>third</p>"}},
		  {"id":"a","type":"hero","title":"T","sort_order":1,"content":{"headline":"First"}},
		  {"id":"c","type":"html","sort_order":2,"content":{"body":"<p>second</p>"}},
		  {"id":"d","type":"content","isVisible":false,"sort_order":4,"content":{"body":"<p>hidden</p>"}}]}`,
		"plain": `{"title":"Plain","page_sections":[{"id":"x","type":"content","sort_order":0,"content":{"body":"<p>x</p>"}}]}`,
		"empty": `{"page_sections":[]}`,
	}
	ids := map[string]uuid.UUID{}
	for slug, attrs := range pages {
		id := uuid.New()
		ids[slug] = id
		if _, err := pool.Exec(ctx, `INSERT INTO hatamoto_entities (id,schema_id,name,slug,status,attributes) VALUES ($1,$2,$3,$3,'draft',$4::jsonb)`, id, schemaID, slug, attrs); err != nil {
			t.Fatal(err)
		}
	}
	// a page that is not a page at all (no sections) is never a candidate
	if _, err := pool.Exec(ctx, `INSERT INTO hatamoto_entities (id,schema_id,name,slug,status,attributes) VALUES ($1,$2,'post','post','draft','{"content":"<p>x</p>"}')`, uuid.New(), schemaID); err != nil {
		t.Fatal(err)
	}

	snapshot := func() string {
		var s string
		if err := pool.QueryRow(ctx, `SELECT COALESCE(string_agg(slug || attributes::text, '|' ORDER BY slug), '') FROM hatamoto_entities`).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	versionCount := func() int {
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM hatamoto_entity_versions`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	docCount := func() int {
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM hatamoto_entities WHERE attributes ? 'page_doc'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	run := func(args ...string) (string, error) {
		var out bytes.Buffer
		err := runBuilderConvert(ctx, dsn, args, &out)
		return out.String(), err
	}

	// ISC-10: dry run + verify writes nothing and reports no diff.
	base := snapshot()
	out, err := run("--dry-run", "--verify-render")
	if err != nil {
		t.Fatalf("dry run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "pages: 3 convertible: 3 render-identical: 3 diffs: 0 skipped (already v2): 0") {
		t.Errorf("dry-run summary:\n%s", out)
	}
	if snapshot() != base || versionCount() != 0 {
		t.Error("dry run wrote something")
	}

	// --entity limits the run to one page
	if out, err = run("--entity", ids["plain"].String()); err != nil || !strings.Contains(out, "pages: 1 convertible: 1") {
		t.Fatalf("--entity: %v\n%s", err, out)
	}
	if docCount() != 1 {
		t.Errorf("--entity converted %d pages, want 1", docCount())
	}

	// ISC-11: the real run: one version per converted page, summary set.
	if out, err = run(); err != nil {
		t.Fatalf("real run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "pages: 3 convertible: 2 render-identical: 2 diffs: 0 skipped (already v2): 1") {
		t.Errorf("real-run summary:\n%s", out)
	}
	if docCount() != 3 {
		t.Errorf("docs = %d, want 3", docCount())
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM hatamoto_entity_versions WHERE change_summary = 'Converted to builder doc'`).Scan(&n); err != nil || n != 3 {
		t.Errorf("versions with the convert summary = %d (%v), want 3", n, err)
	}
	if versionCount() != 3 {
		t.Errorf("versions = %d, want exactly one per page", versionCount())
	}
	// the mirror is kept and a doc is v1-shaped
	var attrs map[string]json.RawMessage
	var raw []byte
	if err := pool.QueryRow(ctx, `SELECT attributes FROM hatamoto_entities WHERE id = $1`, ids["ooo"]).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(raw, &attrs)
	if attrs["page_sections"] == nil || attrs["page_doc"] == nil {
		t.Fatalf("attrs = %s", raw)
	}

	// ISC-11: the second run is a no-op.
	after := snapshot()
	if out, err = run(); err != nil || !strings.Contains(out, "convertible: 0") || !strings.Contains(out, "skipped (already v2): 3") {
		t.Fatalf("second run: %v\n%s", err, out)
	}
	if snapshot() != after || versionCount() != 3 {
		t.Error("second run changed data or versions")
	}

	// --revert is gone: it would resurrect a frozen mirror.
	if _, err := run("--revert"); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Errorf("--revert: err = %v", err)
	}

	// Bad flags are errors (exit non-zero via Main). The forced render-diff
	// exit is covered by TestConvertPagesDiffFailsAndWritesNothing.
	if _, err := run("--entity", "nope"); err == nil {
		t.Error("--entity nope accepted")
	}
}
