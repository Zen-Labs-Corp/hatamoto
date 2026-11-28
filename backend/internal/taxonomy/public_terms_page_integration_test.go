//go:build integration

package taxonomy

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
)

// ListPublicTermsPage lists only terms with a published entity, in a stable
// name order, and reports the total even for a page past the end.
func TestListPublicTermsPage(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	db, err := database.New(ctx, config.DatabaseConfig{URL: dsn, MaxConnections: 4, MinConnections: 1, MaxConnLifetime: time.Minute, MaxConnIdleTime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	repo := NewPostgresRepository(db)
	svc := NewService(repo, nil)
	kind := "zp" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	t.Cleanup(func() { _, _ = db.Pool().Exec(ctx, `DELETE FROM hatamoto_terms WHERE kind = $1`, kind) })

	use := func(name, status string) {
		t.Helper()
		term, err := svc.CreateTerm(ctx, CreateTermInput{Kind: kind, Name: name, Slug: strings.ToLower(name)})
		if err != nil {
			t.Fatal(err)
		}
		if status == "" {
			return
		}
		var id uuid.UUID
		if err := db.Pool().QueryRow(ctx, `
			INSERT INTO hatamoto_entities (schema_id, name, slug, status, attributes, published_at)
			SELECT id, $1, $1, $2, '{"public":true}'::jsonb, now() FROM hatamoto_entity_schemas WHERE slug = 'person'
			RETURNING id`, kind+name, status).Scan(&id); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = db.Pool().Exec(ctx, `DELETE FROM hatamoto_entities WHERE id = $1`, id) })
		if _, err := db.Pool().Exec(ctx, `INSERT INTO hatamoto_entity_terms (entity_id, term_id) VALUES ($1, $2)`, id, term.ID); err != nil {
			t.Fatal(err)
		}
	}
	use("Cc", "published")
	use("Aa", "published")
	use("Bb", "published")
	use("Unused", "")
	use("Drafty", "draft")

	names := func(page, per int) (string, int) {
		t.Helper()
		items, total, err := repo.ListPublicTermsPage(ctx, kind, page, per)
		if err != nil {
			t.Fatal(err)
		}
		var ns []string
		for _, it := range items {
			if it.EntityCount != 1 {
				t.Errorf("%s count = %d, want 1", it.Name, it.EntityCount)
			}
			ns = append(ns, it.Name)
		}
		return strings.Join(ns, ","), total
	}
	if got, total := names(1, 2); got != "Aa,Bb" || total != 3 {
		t.Errorf("page 1 = %q (%d), want Aa,Bb (3)", got, total)
	}
	if got, total := names(2, 2); got != "Cc" || total != 3 {
		t.Errorf("page 2 = %q (%d), want Cc (3)", got, total)
	}
	if got, total := names(9, 2); got != "" || total != 3 {
		t.Errorf("page 9 = %q (%d), want empty (3)", got, total)
	}
}
