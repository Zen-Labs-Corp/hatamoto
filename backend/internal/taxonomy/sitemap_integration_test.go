//go:build integration

package taxonomy

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
)

// ListSitemapTerms lists terms with a published entity, with the nested path,
// and reports the term-level noindex; empty terms are left out.
func TestListSitemapTerms(t *testing.T) {
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

	kind := "smk" + uuid.NewString()[:6]
	mkTerm := func(slug string, parent *uuid.UUID, seo string) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := db.Pool().QueryRow(ctx, `
			INSERT INTO hatamoto_terms (kind, slug, name, parent_id, seo)
			VALUES ($1, $2, $2, $3, COALESCE(NULLIF($4, ''), '{}')::jsonb) RETURNING id`, kind, slug, parent, seo).Scan(&id); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = db.Pool().Exec(ctx, `DELETE FROM hatamoto_terms WHERE id = $1`, id) })
		return id
	}
	parent := mkTerm("parent", nil, "")
	child := mkTerm("child", &parent, "")
	empty := mkTerm("empty", nil, "")
	noidx := mkTerm("noidx", nil, `{"no_index": true}`)
	_ = empty

	mkEntity := func(slug, status string, terms ...uuid.UUID) {
		t.Helper()
		var id uuid.UUID
		if err := db.Pool().QueryRow(ctx, `
			INSERT INTO hatamoto_entities (schema_id, name, slug, status, published_at)
			SELECT id, $1, $1, $2, now() FROM hatamoto_entity_schemas WHERE slug = 'page'
			RETURNING id`, slug, status).Scan(&id); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = db.Pool().Exec(ctx, `DELETE FROM hatamoto_entities WHERE id = $1`, id) })
		for _, term := range terms {
			if _, err := db.Pool().Exec(ctx, `INSERT INTO hatamoto_entity_terms (entity_id, term_id) VALUES ($1, $2)`, id, term); err != nil {
				t.Fatal(err)
			}
		}
	}
	slug := "smk-" + uuid.NewString()[:8]
	mkEntity(slug+"-a", "published", child, noidx)
	mkEntity(slug+"-b", "draft", empty, parent)

	all, err := repo.ListSitemapTerms(ctx, 50000)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]SitemapTerm{}
	for _, st := range all {
		if st.Kind == kind {
			got[st.Path] = st
		}
	}
	if len(got) != 2 {
		t.Fatalf("terms of kind %s = %+v, want parent/child and noidx only", kind, got)
	}
	if st, ok := got["parent/child"]; !ok || st.NoIndex || st.UpdatedAt.IsZero() {
		t.Errorf("parent/child = %+v, ok=%v", st, ok)
	}
	if st, ok := got["noidx"]; !ok || !st.NoIndex {
		t.Errorf("noidx = %+v, ok=%v", st, ok)
	}

	one, err := repo.ListSitemapTerms(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, st := range one {
		if st.Kind == kind {
			n++
		}
	}
	if n != 1 {
		t.Errorf("limit 1 returned %d terms for the kind, want 1", n)
	}
}
