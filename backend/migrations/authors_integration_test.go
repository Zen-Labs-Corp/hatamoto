//go:build integration

package migrations_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/author"
)

const nestedEntitiesVersion = 202610171200

// A database seeded with plain-text bylines gets author rows with the slugs
// /author/{slug} already used, entities point at them, and attributes (so
// JSON-LD and the byline) are untouched.
func TestAuthorsMigration_SeededBylines(t *testing.T) {
	ctx := context.Background()
	db, _ := scratchDB(t)
	p := provider(t, db)
	if _, err := p.UpTo(ctx, nestedEntitiesVersion-1); err != nil {
		t.Fatalf("up to the previous migration: %v", err)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO hatamoto_entity_schemas (id, name, slug, schema) VALUES ('11111111-1111-1111-1111-111111111111', 'Authored', 'authored', '{"type":"object"}')`)
	seed := []struct{ slug, attrs string }{
		{"a1", `{"author": "Jane Guest"}`},
		{"a2", `{"author": "Jane Guest"}`},
		{"a3", `{"author": "  jane guest "}`},          // same slug, other spelling: one author
		{"a4", `{"author_name": "Bob O'Brien-Smith"}`}, // author_name fallback, punctuation
		{"a5", `{"author": "Zoë Álvarez"}`},            // non-ASCII letters are dropped from the slug
		{"a6", `{"author": "李明"}`},                     // no ASCII letter: hash slug
		{"a7", `{"author": "", "author_name": "Bob O'Brien-Smith"}`},
		{"a8", `{"title": "no byline"}`},
		{"a9", `{"author": "Conan O’Brien"}`},    // curly apostrophe is punctuation
		{"a10", `{"author": "Jane—Doe"}`},        // em dash
		{"a11", `{"author": "Anne\u00a0Marie"}`}, // no-break space
		{"a12", `{"author": "Ελένη"}`},
		{"a13", `{"author": "🙂"}`},
		{"a14", `{"author": "Ñ"}`},
	}
	for _, s := range seed {
		exec(`INSERT INTO hatamoto_entities (schema_id, name, slug, status, attributes)
			VALUES ('11111111-1111-1111-1111-111111111111', $1, $1, 'published', $2::jsonb)`, s.slug, s.attrs)
	}
	before := map[string]string{}
	rows, err := db.QueryContext(ctx, `SELECT slug, attributes::text FROM hatamoto_entities`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var s, a string
		if err := rows.Scan(&s, &a); err != nil {
			t.Fatal(err)
		}
		before[s] = a
	}
	rows.Close()

	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}

	// Author rows: slug -> name.
	got := map[string]string{}
	rows, err = db.QueryContext(ctx, `SELECT slug, name FROM hatamoto_authors`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var s, n string
		if err := rows.Scan(&s, &n); err != nil {
			t.Fatal(err)
		}
		got[s] = n
	}
	rows.Close()
	want := map[string]string{
		"jane-guest":            "Jane Guest", // the first spelling in byte order ("Jane Guest" < "jane guest")
		"bob-o-brien-smith":     "Bob O'Brien-Smith",
		"zo-lvarez":             "Zoë Álvarez",
		"conan-o-brien":         "Conan O’Brien",
		"jane-doe":              "Jane—Doe",
		"anne-marie":            "Anne\u00a0Marie",
		"n":                     "Ñ",
		author.Slugify("李明"):    "李明",
		author.Slugify("Ελένη"): "Ελένη",
		author.Slugify("🙂"):     "🙂",
	}
	if len(got) != len(want) {
		t.Errorf("authors = %v, want %v", got, want)
	}
	for s, n := range want {
		if got[s] != n {
			t.Errorf("author %q = %q, want %q", s, got[s], n)
		}
	}

	// Entities reference them; only the missing byline stays unlinked. The slugs are
	// the ones author.Slugify gives, so a later save finds these rows.
	link := func(slug string) string {
		var s sql.NullString
		err := db.QueryRowContext(ctx, `SELECT a.slug FROM hatamoto_entities e LEFT JOIN hatamoto_authors a ON a.id = e.author_id WHERE e.slug = $1`, slug).Scan(&s)
		if err != nil {
			t.Fatal(err)
		}
		return s.String
	}
	for slug, author := range map[string]string{
		"a1": "jane-guest", "a2": "jane-guest", "a3": "jane-guest",
		"a4": "bob-o-brien-smith", "a5": "zo-lvarez", "a6": author.Slugify("李明"), "a7": "bob-o-brien-smith", "a8": "",
		"a9": "conan-o-brien", "a10": "jane-doe", "a11": "anne-marie", "a12": author.Slugify("Ελένη"), "a13": author.Slugify("🙂"), "a14": "n",
	} {
		if got := link(slug); got != author {
			t.Errorf("entity %s -> author %q, want %q", slug, got, author)
		}
	}

	// attributes are byte-for-byte what they were.
	rows, err = db.QueryContext(ctx, `SELECT slug, attributes::text FROM hatamoto_entities`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var s, a string
		if err := rows.Scan(&s, &a); err != nil {
			t.Fatal(err)
		}
		if want, existed := before[s]; existed && a != want { // people made by a later backfill are new rows
			t.Errorf("attributes of %s changed: %s -> %s", s, want, a)
		}
	}

	// Down then up again is structure-only and repeatable.
	if _, err := p.DownTo(ctx, nestedEntitiesVersion-1); err != nil {
		t.Fatalf("down: %v", err)
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("up again: %v", err)
	}
}

// Slug uniqueness follows the schema's URL pattern: siblings under a {path} or
// {parent} pattern, the whole schema otherwise. Changing the pattern moves the
// rows between the two rules, and a change that would collide fails.
func TestNestedEntitiesMigration_SlugScopeFollowsPattern(t *testing.T) {
	ctx := context.Background()
	db, _ := scratchDB(t)
	if _, err := provider(t, db).Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	exec := func(q string, args ...any) error {
		_, err := db.ExecContext(ctx, q, args...)
		return err
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	const nestedSchema, flatSchema = "22222222-2222-2222-2222-222222222222", "33333333-3333-3333-3333-333333333333"
	must(exec(`INSERT INTO hatamoto_entity_schemas (id, name, slug, schema, settings) VALUES ($1, 'N', 'nested', '{"type":"object"}', '{"url_pattern":"/{path}"}')`, nestedSchema))
	must(exec(`INSERT INTO hatamoto_entity_schemas (id, name, slug, schema, settings) VALUES ($1, 'F', 'flat', '{"type":"object"}', '{"url_pattern":"/notes/{slug}"}')`, flatSchema))
	ins := func(schema, id, slug, parent string) error {
		var p any
		if parent != "" {
			p = parent
		}
		return exec(`INSERT INTO hatamoto_entities (id, schema_id, name, slug, parent_id) VALUES ($1, $2, $3, $3, $4)`, id, schema, slug, p)
	}
	const (
		a1 = "aaaaaaaa-0000-0000-0000-000000000001"
		a2 = "aaaaaaaa-0000-0000-0000-000000000002"
		a3 = "aaaaaaaa-0000-0000-0000-000000000003"
	)
	// {path}: siblings only.
	must(ins(nestedSchema, a1, "services", ""))
	must(ins(nestedSchema, a2, "seo", a1))
	must(ins(nestedSchema, a3, "pt", a1))
	must(ins(nestedSchema, "aaaaaaaa-0000-0000-0000-000000000004", "local", a2))
	must(ins(nestedSchema, "aaaaaaaa-0000-0000-0000-000000000005", "local", a3))
	if ins(nestedSchema, "aaaaaaaa-0000-0000-0000-000000000006", "services", "") == nil {
		t.Error("two roots with one slug must conflict")
	}
	if ins(nestedSchema, "aaaaaaaa-0000-0000-0000-000000000007", "local", a2) == nil {
		t.Error("two siblings with one slug must conflict")
	}
	// A flat pattern: the slug is unique in the schema whatever the parent.
	must(ins(flatSchema, "bbbbbbbb-0000-0000-0000-000000000001", "p1", ""))
	must(ins(flatSchema, "bbbbbbbb-0000-0000-0000-000000000002", "p2", ""))
	must(ins(flatSchema, "bbbbbbbb-0000-0000-0000-000000000003", "dup", "bbbbbbbb-0000-0000-0000-000000000001"))
	if ins(flatSchema, "bbbbbbbb-0000-0000-0000-000000000004", "dup", "bbbbbbbb-0000-0000-0000-000000000002") == nil {
		t.Error("a flat schema must not accept one slug under two parents")
	}
	// The flag follows the schema's settings, overrides included.
	nested := func(schema string) bool {
		var b bool
		must(db.QueryRowContext(ctx, `SELECT bool_and(slug_nested) FROM hatamoto_entities WHERE schema_id = $1`, schema).Scan(&b))
		return b
	}
	if !nested(nestedSchema) || nested(flatSchema) {
		t.Fatal("slug_nested does not match the schemas")
	}
	// A flat schema becomes nested: the rows move to the sibling rule without
	// touching updated_at, and the duplicate under another parent now fits.
	var before string
	must(db.QueryRowContext(ctx, `SELECT updated_at::text FROM hatamoto_entities WHERE id = 'bbbbbbbb-0000-0000-0000-000000000003'`).Scan(&before))
	must(exec(`UPDATE hatamoto_entity_schemas SET settings = '{"overrides":[{"attribute":"k","equals":"v","url_pattern":"/x/{parent}/{slug}"}]}' WHERE id = $1`, flatSchema))
	if !nested(flatSchema) {
		t.Error("an override with {parent} must make the schema nested")
	}
	var after string
	must(db.QueryRowContext(ctx, `SELECT updated_at::text FROM hatamoto_entities WHERE id = 'bbbbbbbb-0000-0000-0000-000000000003'`).Scan(&after))
	if before != after {
		t.Errorf("a schema settings change touched updated_at: %s -> %s", before, after)
	}
	must(ins(flatSchema, "bbbbbbbb-0000-0000-0000-000000000004", "dup", "bbbbbbbb-0000-0000-0000-000000000002"))
	// Going back to a flat pattern with the duplicate in place must fail, and
	// leave the schema as it was.
	if exec(`UPDATE hatamoto_entity_schemas SET settings = '{"url_pattern":"/notes/{slug}"}' WHERE id = $1`, flatSchema) == nil {
		t.Error("a pattern change that makes slugs collide must fail")
	}
	if !nested(flatSchema) {
		t.Error("the failed change must roll back")
	}
}

// Public slug lookups keep an index that leads with the slug.
func TestNestedEntitiesMigration_SlugLookupIndex(t *testing.T) {
	ctx := context.Background()
	db, _ := scratchDB(t)
	if _, err := provider(t, db).Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	var def string
	if err := db.QueryRowContext(ctx, `SELECT indexdef FROM pg_indexes WHERE tablename = 'hatamoto_entities' AND indexname = 'idx_entities_slug'`).Scan(&def); err != nil {
		t.Fatalf("slug lookup index missing: %v", err)
	}
	// With enough rows for the planner to prefer it, both resolver queries seek on the slug.
	if _, err := db.ExecContext(ctx, `
		INSERT INTO hatamoto_entities (schema_id, name, slug)
		SELECT s.id, 'e' || g, 'slug-' || g FROM hatamoto_entity_schemas s, generate_series(1, 20000) g WHERE s.slug = 'post'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `ANALYZE hatamoto_entities`); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`SELECT e.id FROM hatamoto_entities e JOIN hatamoto_entity_schemas s ON s.id = e.schema_id
			WHERE e.slug = 'slug-777' AND s.slug = ANY(ARRAY['post']) AND e.status = 'published' AND e.variant_key IS NULL`,
		`SELECT e.id FROM hatamoto_entities e WHERE e.parent_id IS NULL AND e.variant_key IS NULL AND e.slug = 'slug-777'`,
	} {
		rows, err := db.QueryContext(ctx, `EXPLAIN `+q)
		if err != nil {
			t.Fatal(err)
		}
		var plan string
		for rows.Next() {
			var l string
			if err := rows.Scan(&l); err != nil {
				t.Fatal(err)
			}
			plan += l + "\n"
		}
		rows.Close()
		if !strings.Contains(plan, "idx_entities_slug") || strings.Contains(plan, "Seq Scan on hatamoto_entities") {
			t.Errorf("slug lookup does not use idx_entities_slug:\n%s", plan)
		}
	}
}
