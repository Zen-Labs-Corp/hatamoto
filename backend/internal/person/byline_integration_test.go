//go:build integration

package person

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"hatamoto.dev/core/internal/entity"
)

// Needs a database migrated to head (DATABASE_URL); everything runs in a
// transaction that is rolled back.
func TestBylineResolver(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	b := NewByline(tx)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	count := func(q string, args ...any) (n int) {
		t.Helper()
		must(tx.QueryRow(ctx, q, args...).Scan(&n))
		return n
	}

	// Organisation: the company name (any case) is the org and makes no person.
	must(func() error {
		_, err := tx.Exec(ctx, `DELETE FROM hatamoto_site_config`)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO hatamoto_site_config (company_name) VALUES ('Acme Org')`)
		return err
	}())
	before := count(`SELECT count(*) FROM hatamoto_entities`)
	org, err := b.Resolve(ctx, "  acme ORG ")
	if err != nil || !org.Org || org.PersonID != nil || org.Name != "Acme Org" {
		t.Fatalf("org byline = %+v, %v", org, err)
	}
	if count(`SELECT count(*) FROM hatamoto_entities`) != before {
		t.Error("the organisation byline created an entity")
	}

	// Unknown name: a non-public person without a login. The same name again is that person.
	p1, err := b.Resolve(ctx, "Ada Lovelace")
	if err != nil || p1.PersonID == nil || p1.Org {
		t.Fatalf("new person = %+v, %v", p1, err)
	}
	got, err := NewStore(tx).Get(ctx, *p1.PersonID)
	if err != nil || got.Public || got.Slug != "ada-lovelace" {
		t.Fatalf("created person = %+v, %v", got, err)
	}
	if n := count(`SELECT count(*) FROM hatamoto_entities WHERE id = $1 AND status = 'draft' AND published_at IS NULL`, got.ID); n != 1 {
		t.Error("an auto-created hidden person must be a draft (a rollback shows published entities)")
	}
	if _, err := NewStore(tx).LinkedUser(ctx, got.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("auto-created person has a login: %v", err)
	}
	p2, err := b.Resolve(ctx, "ada lovelace")
	if err != nil || p2.PersonID == nil || *p2.PersonID != *p1.PersonID {
		t.Errorf("existing person by name = %+v, %v", p2, err)
	}
	if err := b.Check(ctx, uuid.New()); !errors.Is(err, entity.ErrAuthorNotFound) {
		t.Errorf("Check unknown = %v", err)
	}
	must(b.Check(ctx, *p1.PersonID))

	// Mirror: made once, kept on rename, posts follow the name.
	m1, err := b.Mirror(ctx, *p1.PersonID)
	must(err)
	m2, err := b.Mirror(ctx, *p1.PersonID)
	must(err)
	if *m1 != *m2 {
		t.Error("Mirror is not stable")
	}
	var postID uuid.UUID
	must(tx.QueryRow(ctx, `
		INSERT INTO hatamoto_entities (schema_id, name, slug, status, attributes, byline_person_id, author_id)
		SELECT id, 'post', 'pb-post', 'published', '{"author":"Ada Lovelace"}', $1, $2 FROM hatamoto_entity_schemas WHERE slug = 'post'
		RETURNING id`, *p1.PersonID, *m1).Scan(&postID))
	_, err = tx.Exec(ctx, `UPDATE hatamoto_entities SET name = 'Ada King' WHERE id = $1`, *p1.PersonID)
	must(err)
	must(b.PersonChanged(ctx, *p1.PersonID))
	var an, byline string
	must(tx.QueryRow(ctx, `SELECT name FROM hatamoto_authors WHERE id = $1`, *m1).Scan(&an))
	must(tx.QueryRow(ctx, `SELECT attributes->>'author' FROM hatamoto_entities WHERE id = $1`, postID).Scan(&byline))
	if an != "Ada King" || byline != "Ada King" {
		t.Errorf("after rename: mirror %q, post byline %q", an, byline)
	}

	// Delete: posts lose the reference, keep the text, mirror row stays.
	must(b.PersonDeleting(ctx, *p1.PersonID))
	if n := count(`SELECT count(*) FROM hatamoto_entities WHERE id = $1 AND byline_person_id IS NULL AND attributes->>'author' = 'Ada King'`, postID); n != 1 {
		t.Error("post not detached from the deleted person")
	}
	if n := count(`SELECT count(*) FROM hatamoto_authors WHERE id = $1`, *m1); n != 1 {
		t.Error("mirror author row removed")
	}
}
