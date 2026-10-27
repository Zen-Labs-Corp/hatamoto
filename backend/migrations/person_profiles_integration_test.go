//go:build integration

package migrations_test

import (
	"context"
	"testing"
	"time"
)

const personProfilesVersion = 202610231000

func TestPersonProfilesMigration(t *testing.T) {
	ctx := context.Background()
	db, _ := scratchDB(t)
	p := provider(t, db)
	if _, err := p.UpTo(ctx, personProfilesVersion-1); err != nil {
		t.Fatalf("up to the previous migration: %v", err)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	// A row written the way the previous binary writes it (no new columns).
	exec(`INSERT INTO hatamoto_entity_schemas (id, name, slug, schema) VALUES ('11111111-1111-1111-1111-111111111111', 'Authored', 'authored', '{"type":"object"}')`)
	exec(`INSERT INTO hatamoto_entities (schema_id, name, slug, status, attributes)
		VALUES ('11111111-1111-1111-1111-111111111111', 'old', 'old', 'published', '{}'::jsonb)`)
	// An author with a published post and one without: the backfill makes both
	// people, and the status follows public (a rollback shows published only).
	exec(`INSERT INTO hatamoto_authors (id, slug, name) VALUES
		('33333333-3333-3333-3333-333333333331', 'with-post', 'With Post'),
		('33333333-3333-3333-3333-333333333332', 'no-post', 'No Post')`)
	exec(`INSERT INTO hatamoto_entities (schema_id, name, slug, status, attributes, author_id)
		VALUES ('11111111-1111-1111-1111-111111111111', 'p', 'p', 'published', '{}'::jsonb, '33333333-3333-3333-3333-333333333331')`)

	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}

	for slug, want := range map[string]string{"with-post": "published", "no-post": "draft"} {
		var status string
		var pub *time.Time
		if err := db.QueryRowContext(ctx, `SELECT status, published_at FROM hatamoto_entities e JOIN hatamoto_entity_schemas s ON s.id = e.schema_id AND s.slug = 'person' WHERE e.slug = $1`, slug).Scan(&status, &pub); err != nil {
			t.Fatalf("backfilled person %s: %v", slug, err)
		}
		if status != want || (want == "draft") != (pub == nil) {
			t.Errorf("backfilled %s: status=%s published_at=%v, want %s", slug, status, pub, want)
		}
	}

	var builtin bool
	var pattern string
	if err := db.QueryRowContext(ctx, `SELECT is_builtin, settings->>'url_pattern' FROM hatamoto_entity_schemas WHERE slug = 'person'`).Scan(&builtin, &pattern); err != nil {
		t.Fatalf("person schema: %v", err)
	}
	if !builtin || pattern != "/author/{slug}" {
		t.Errorf("person schema builtin=%v url_pattern=%q", builtin, pattern)
	}

	var person *string
	var org bool
	if err := db.QueryRowContext(ctx, `SELECT byline_person_id::text, byline_org FROM hatamoto_entities WHERE slug = 'old'`).Scan(&person, &org); err != nil {
		t.Fatalf("new columns: %v", err)
	}
	if person != nil || org {
		t.Errorf("existing row: byline_person_id=%v byline_org=%v, want NULL/false", person, org)
	}

	// The previous binary's insert shape still works after the migration.
	exec(`INSERT INTO hatamoto_entities (schema_id, name, slug, status, attributes)
		VALUES ('11111111-1111-1111-1111-111111111111', 'again', 'again', 'published', '{}'::jsonb)`)

	// Link table: one person per user, one user per person.
	exec(`INSERT INTO hatamoto_users (id, email, password_hash, name) VALUES
		('22222222-2222-2222-2222-222222222221', 'a@example.com', 'x', 'A'),
		('22222222-2222-2222-2222-222222222222', 'b@example.com', 'x', 'B')`)
	var e1, e2 string
	if err := db.QueryRowContext(ctx, `SELECT id::text FROM hatamoto_entities WHERE slug = 'old'`).Scan(&e1); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT id::text FROM hatamoto_entities WHERE slug = 'again'`).Scan(&e2); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO hatamoto_user_people (user_id, entity_id) VALUES ('22222222-2222-2222-2222-222222222221', $1)`, e1)
	if _, err := db.ExecContext(ctx, `INSERT INTO hatamoto_user_people (user_id, entity_id) VALUES ('22222222-2222-2222-2222-222222222221', $1)`, e2); err == nil {
		t.Error("a second person for the same user was accepted")
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO hatamoto_user_people (user_id, entity_id) VALUES ('22222222-2222-2222-2222-222222222222', $1)`, e1); err == nil {
		t.Error("a second user for the same person was accepted")
	}
	exec(`DELETE FROM hatamoto_entities WHERE id = $1`, e1)
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM hatamoto_user_people`).Scan(&n); err != nil || n != 0 {
		t.Errorf("link rows after entity delete = %d, %v; want cascade to 0", n, err)
	}
}

const personSchemaOrderVersion = 202610231010

func TestPersonSchemaOrderMigration(t *testing.T) {
	ctx := context.Background()
	db, _ := scratchDB(t)
	p := provider(t, db)
	if _, err := p.UpTo(ctx, personSchemaOrderVersion-1); err != nil {
		t.Fatalf("up to the previous migration: %v", err)
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	query := func(q string, dst ...any) {
		t.Helper()
		if err := db.QueryRowContext(ctx, q).Scan(dst...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	var order, group, jobGroup, onTeam, onTeamDefault string
	query(`SELECT schema->>'x-hm-order', schema->'properties'->'github'->>'x-hm-group', schema->'properties'->'job_title'->>'x-hm-group',
		schema->'properties'->'on_team'->>'type', schema->'properties'->'on_team'->>'default' FROM hatamoto_entity_schemas WHERE slug = 'person'`,
		&order, &group, &jobGroup, &onTeam, &onTeamDefault)
	if order == "" || group != "Contact and links" || jobGroup != "Profile" || onTeam != "boolean" || onTeamDefault != "false" {
		t.Errorf("order=%q github group=%q job_title group=%q on_team=%q default=%q", order, group, jobGroup, onTeam, onTeamDefault)
	}
	// Core fields keep their original definition next to the new keys.
	var maxLen string
	query(`SELECT schema->'properties'->'job_title'->>'maxLength' FROM hatamoto_entity_schemas WHERE slug = 'person'`, &maxLen)
	if maxLen != "120" {
		t.Errorf("job_title maxLength = %q, want 120 kept", maxLen)
	}
}

func TestPersonSchemaOrderMigrationSkipsEditedSchema(t *testing.T) {
	ctx := context.Background()
	db, _ := scratchDB(t)
	p := provider(t, db)
	if _, err := p.UpTo(ctx, personSchemaOrderVersion-1); err != nil {
		t.Fatalf("up to the previous migration: %v", err)
	}
	// A site edited the schema and already has its own order.
	if _, err := db.ExecContext(ctx, `UPDATE hatamoto_entity_schemas SET "schema" = "schema" || '{"x-hm-order":["github"]}'::jsonb WHERE slug = 'person'`); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	var order string
	var hasOnTeam bool
	if err := db.QueryRowContext(ctx, `SELECT schema->>'x-hm-order', schema->'properties' ? 'on_team' FROM hatamoto_entity_schemas WHERE slug = 'person'`).Scan(&order, &hasOnTeam); err != nil {
		t.Fatal(err)
	}
	if order != `["github"]` || hasOnTeam {
		t.Errorf("edited schema changed: order=%s on_team=%v", order, hasOnTeam)
	}
}
