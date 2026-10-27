//go:build integration

package migrations_test

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/person"
)

const backfillPeopleVersion = 202610231005

// stateOf is a stable text snapshot of every row the backfill may touch.
func stateOf(t *testing.T, db *sql.DB) string {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), `
		SELECT concat_ws('|', e.slug, s.slug, e.name, e.attributes::text, e.metadata::text,
		       COALESCE(e.byline_person_id::text, ''), e.byline_org::text, COALESCE(e.author_id::text, ''),
		       e.updated_at::text)
		FROM hatamoto_entities e JOIN hatamoto_entity_schemas s ON s.id = e.schema_id
		UNION ALL SELECT 'link|' || user_id::text || '|' || entity_id::text FROM hatamoto_user_people
		ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var b strings.Builder
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

// A site shaped like one with many authors, one of them the company itself
// and a duplicated human, and one person made before the backfill that
// carries page_sections.
func TestBackfillPeople_ManyAuthors(t *testing.T) {
	ctx := context.Background()
	db, _ := scratchDB(t)
	p := provider(t, db)
	if _, err := p.UpTo(ctx, backfillPeopleVersion-1); err != nil {
		t.Fatal(err)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO hatamoto_site_config (company_name) VALUES ('Acme Org')`)
	exec(`INSERT INTO hatamoto_entity_schemas (id, name, slug, schema) VALUES ('11111111-1111-1111-1111-111111111111', 'Post', 'plain-post', '{"type":"object"}')`)
	exec(`INSERT INTO hatamoto_media (id, filename, original_filename, mime_type, file_size, storage_path, public_url)
		VALUES ('33333333-3333-3333-3333-333333333333', 'a.jpg', 'a.jpg', 'image/jpeg', 1, 'a.jpg', '/media/a.jpg')`)

	names := []string{"Acme Org", "Acme Orgs", "Alex C", "Alex Carter", "Bea Smith", "Cy Jones", "Di Park", "Eve Ng", "Fay Lo"}
	authorIDs := map[string]string{}
	for i, n := range names {
		slug := strings.ToLower(strings.ReplaceAll(n, " ", "-"))
		var id string
		avatar := any(nil)
		if n == "Bea Smith" {
			avatar = "33333333-3333-3333-3333-333333333333"
		}
		if err := db.QueryRowContext(ctx, `INSERT INTO hatamoto_authors (slug, name, bio, avatar_media_id) VALUES ($1, $2, $3, $4) RETURNING id::text`,
			slug, n, fmt.Sprintf("Bio of %s & co", n), avatar).Scan(&id); err != nil {
			t.Fatal(err)
		}
		authorIDs[n] = id
		posts := 2
		if n == "Acme Org" {
			posts = 5
		}
		status := "published"
		if n == "Fay Lo" {
			status = "draft" // an author with no published entity stays hidden
		}
		for j := 0; j < posts; j++ {
			// author_entity_id is a site-specific attribute: core must not read it.
			exec(`INSERT INTO hatamoto_entities (schema_id, name, slug, status, attributes, author_id)
				VALUES ('11111111-1111-1111-1111-111111111111', $1, $2, $3, jsonb_build_object('author_entity_id', $4::text), $5::uuid)`,
				fmt.Sprintf("post %d-%d", i, j), fmt.Sprintf("post-%d-%d", i, j), status, authorIDs["Alex C"], id)
		}
	}
	exec(`INSERT INTO hatamoto_entities (schema_id, name, slug, status, attributes)
		SELECT id, 'Early Bird', 'early-bird', 'published', '{"page_sections":[{"type":"hero"}],"public":true}'::jsonb
		FROM hatamoto_entity_schemas WHERE slug = 'person'`)
	exec(`INSERT INTO hatamoto_users (id, email, password_hash, name) VALUES
		('22222222-2222-2222-2222-222222222221', 'bea@example.com', 'x', 'bea smith'),
		('22222222-2222-2222-2222-222222222222', 'other@example.com', 'x', 'No Match')`)

	if _, err := p.UpTo(ctx, backfillPeopleVersion); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	count := func(q string) int {
		t.Helper()
		var n int
		if err := db.QueryRowContext(ctx, q).Scan(&n); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return n
	}
	// 8 authors that are not the company (a near-match name is a person) + Early Bird.
	if n := count(`SELECT count(*) FROM hatamoto_entities e JOIN hatamoto_entity_schemas s ON s.id = e.schema_id AND s.slug = 'person' WHERE e.metadata ? 'import_key'`); n != 8 {
		t.Errorf("people made = %d, want 8", n)
	}
	if n := count(`SELECT count(*) FROM hatamoto_entities WHERE byline_org`); n != 5 {
		t.Errorf("org bylines = %d, want 5 (not following author_entity_id)", n)
	}
	if n := count(`SELECT count(*) FROM hatamoto_entities WHERE byline_org AND byline_person_id IS NOT NULL`); n != 0 {
		t.Errorf("%d entities are both org and person", n)
	}
	if n := count(`SELECT count(*) FROM hatamoto_entities WHERE author_id IS NOT NULL AND byline_person_id IS NULL AND NOT byline_org`); n != 0 {
		t.Errorf("%d authored entities have no byline", n)
	}
	if n := count(`SELECT count(*) FROM hatamoto_entities x WHERE x.author_id IS NOT NULL AND x.byline_person_id IS NOT NULL
		AND NOT EXISTS (SELECT 1 FROM hatamoto_entities p WHERE p.id = x.byline_person_id AND p.metadata->>'legacy_author_id' = x.author_id::text)`); n != 0 {
		t.Errorf("%d entities point at a person that does not mirror their author", n)
	}
	if n := count(`SELECT count(*) FROM hatamoto_entities WHERE name = 'Acme Org'`); n != 0 {
		t.Errorf("a person was made for the company name")
	}
	if n := count(`SELECT count(*) FROM hatamoto_entities WHERE attributes ? 'page_sections' AND slug = 'early-bird'`); n != 0 {
		t.Errorf("page_sections survived on a person")
	}
	if n := count(`SELECT count(*) FROM hatamoto_entities e JOIN hatamoto_entity_schemas s ON s.id = e.schema_id AND s.slug = 'person' WHERE e.attributes ? 'page_sections'`); n != 0 {
		t.Errorf("page_sections on a created person")
	}
	if n := count(`SELECT count(*) FROM hatamoto_entities WHERE slug = 'fay-lo' AND attributes->>'public' = 'false'`); n != 1 {
		t.Errorf("an author without published entities must not be public")
	}
	if n := count(`SELECT count(*) FROM hatamoto_entities WHERE slug = 'bea-smith' AND attributes->>'public' = 'true'
		AND attributes->>'photo' = '/media/a.jpg' AND attributes->>'bio' = '<p>Bio of Bea Smith &amp; co</p>'`); n != 1 {
		t.Errorf("bea's person lacks public/photo/bio")
	}
	// Name match links bea; "No Match" stays unlinked.
	if n := count(`SELECT count(*) FROM hatamoto_user_people l JOIN hatamoto_entities e ON e.id = l.entity_id
		WHERE l.user_id = '22222222-2222-2222-2222-222222222221' AND e.slug = 'bea-smith'`); n != 1 {
		t.Errorf("bea not linked to her person")
	}
	if n := count(`SELECT count(*) FROM hatamoto_user_people`); n != 1 {
		t.Errorf("links = %d, want 1", n)
	}

	// A second run (the migration's own function) changes nothing.
	before := stateOf(t, db)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := person.Backfill(ctx, tx); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if after := stateOf(t, db); after != before {
		t.Errorf("a second run changed rows:\n--- before\n%s--- after\n%s", before, after)
	}
}

// hatamoto.dev shape: one user, one author with a different spelling of the
// name. They are the same human.
func TestBackfillPeople_SoleUserAndAuthor(t *testing.T) {
	ctx := context.Background()
	db, _ := scratchDB(t)
	p := provider(t, db)
	if _, err := p.UpTo(ctx, backfillPeopleVersion-1); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO hatamoto_authors (slug, name) VALUES ('chintan', 'Chintan')`,
		`INSERT INTO hatamoto_users (id, email, password_hash, name) VALUES ('22222222-2222-2222-2222-222222222221', 'c@example.com', 'x', 'Chintan Shah')`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := p.UpTo(ctx, backfillPeopleVersion); err != nil {
		t.Fatal(err)
	}
	var slug string
	err := db.QueryRowContext(ctx, `SELECT e.slug FROM hatamoto_user_people l JOIN hatamoto_entities e ON e.id = l.entity_id`).Scan(&slug)
	if err != nil || slug != "chintan" {
		t.Errorf("linked person = %q, %v; want chintan", slug, err)
	}
}

// A site that made its own `person` content type is told so, not written into.
func TestBackfillPeople_RefusesForeignPersonSchema(t *testing.T) {
	ctx := context.Background()
	db, _ := scratchDB(t)
	p := provider(t, db)
	if _, err := p.UpTo(ctx, backfillPeopleVersion-1); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE hatamoto_entity_schemas SET is_builtin = false WHERE slug = 'person'`); err != nil {
		t.Fatal(err)
	}
	_, err := p.UpTo(ctx, backfillPeopleVersion)
	if err == nil || !strings.Contains(err.Error(), "rename that content type") {
		t.Fatalf("err = %v, want the rename message", err)
	}
}
