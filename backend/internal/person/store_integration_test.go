//go:build integration

package person

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Needs a database migrated to head (DATABASE_URL); every row is made inside a
// transaction that is rolled back.
func TestStore(t *testing.T) {
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
	s := NewStore(tx)

	var schemaID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM hatamoto_entity_schemas WHERE slug = 'person'`).Scan(&schemaID); err != nil {
		t.Fatalf("person schema (is the database migrated?): %v", err)
	}
	mk := func(slug, name, attrs string) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := tx.QueryRow(ctx, `INSERT INTO hatamoto_entities (schema_id, name, slug, status, attributes)
			VALUES ($1, $2, $3, 'published', $4::jsonb) RETURNING id`, schemaID, name, slug, attrs).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	a := mk("pp-ada", "Ada", `{"job_title":"Engineer","photo":"/m/a.jpg","public":true,"linkedin":"https://l.example/ada","expertise":["go"]}`)
	b := mk("pp-bob", "Bob", `{}`)
	var u1, u2 uuid.UUID = uuid.New(), uuid.New()
	for i, u := range []uuid.UUID{u1, u2} {
		if _, err := tx.Exec(ctx, `INSERT INTO hatamoto_users (id, email, name) VALUES ($1, $2, 'u')`, u, []string{"pp1@example.com", "pp2@example.com"}[i]); err != nil {
			t.Fatal(err)
		}
	}

	got, err := s.ByIDs(ctx, []uuid.UUID{a, b, uuid.New()})
	if err != nil || len(got) != 2 {
		t.Fatalf("ByIDs = %v, %v; want the two known people", got, err)
	}
	if p := got[a]; p.JobTitle != "Engineer" || p.Photo != "/m/a.jpg" || !p.Public || p.Links["linkedin"] == "" || len(p.Expertise) != 1 {
		t.Errorf("Ada decoded as %+v", p)
	}
	if got[b].Public {
		t.Error("a person without the public attribute is public")
	}
	if p, err := s.GetBySlug(ctx, "pp-bob"); err != nil || p.ID != b {
		t.Errorf("GetBySlug = %v, %v", p, err)
	}
	if _, err := s.Get(ctx, uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get unknown = %v, want ErrNotFound", err)
	}

	if _, err := s.ForUser(ctx, u1); !errors.Is(err, ErrNotFound) {
		t.Errorf("ForUser before Link = %v, want ErrNotFound", err)
	}
	if err := s.Link(ctx, u1, a); err != nil {
		t.Fatal(err)
	}
	if p, err := s.ForUser(ctx, u1); err != nil || p.ID != a {
		t.Errorf("ForUser = %v, %v", p, err)
	}
	if u, err := s.LinkedUser(ctx, a); err != nil || u != u1 {
		t.Errorf("LinkedUser = %v, %v", u, err)
	}
	// 1:1 in both directions. A failed insert aborts the transaction, so use savepoints.
	sp := func(name string, f func() error) error {
		_, _ = tx.Exec(ctx, "SAVEPOINT "+name)
		err := f()
		_, _ = tx.Exec(ctx, "ROLLBACK TO SAVEPOINT "+name)
		return err
	}
	if err := sp("s1", func() error { return s.Link(ctx, u1, b) }); !errors.Is(err, ErrAlreadyLinked) {
		t.Errorf("second person for a user = %v, want ErrAlreadyLinked", err)
	}
	if err := sp("s2", func() error { return s.Link(ctx, u2, a) }); !errors.Is(err, ErrAlreadyLinked) {
		t.Errorf("second user for a person = %v, want ErrAlreadyLinked", err)
	}
	if err := s.Link(ctx, u2, uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Errorf("Link to a non-person = %v, want ErrNotFound", err)
	}

	// post counts: published entities crediting a person
	if _, err := tx.Exec(ctx, `UPDATE hatamoto_entities SET byline_person_id = $1 WHERE id = $2`, a, b); err != nil {
		t.Fatal(err)
	}
	if n, err := s.PostCounts(ctx); err != nil || n[a] != 1 {
		t.Errorf("PostCounts = %v, %v", n, err)
	}
}
