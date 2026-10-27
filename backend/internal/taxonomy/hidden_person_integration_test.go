//go:build integration

package taxonomy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
)

// A published person who is not public must not show in the public term API or
// in the term counts, whatever its status (the previous release publishes them).
func TestPublicTermsExcludeHiddenPeople(t *testing.T) {
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

	slug := "hp-" + uuid.NewString()[:8]
	term, err := svc.CreateTerm(ctx, CreateTermInput{Kind: "tag", Name: slug, Slug: slug})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = db.Pool().Exec(ctx, `DELETE FROM hatamoto_terms WHERE id = $1`, term.ID) })

	mk := func(name, attrs string) {
		t.Helper()
		var id uuid.UUID
		if err := db.Pool().QueryRow(ctx, `
			INSERT INTO hatamoto_entities (schema_id, name, slug, status, attributes, published_at)
			SELECT id, $1, $1, 'published', $2::jsonb, now() FROM hatamoto_entity_schemas WHERE slug = 'person'
			RETURNING id`, name, attrs).Scan(&id); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = db.Pool().Exec(ctx, `DELETE FROM hatamoto_entities WHERE id = $1`, id) })
		if _, err := db.Pool().Exec(ctx, `INSERT INTO hatamoto_entity_terms (entity_id, term_id) VALUES ($1, $2)`, id, term.ID); err != nil {
			t.Fatal(err)
		}
	}
	mk(slug+"-hidden", `{"public":false}`)
	mk(slug+"-shown", `{"public":true}`)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/public/terms/{kind}/{slug}/entities", NewHandlers(svc, nil).PublicTermArchive)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/public/terms/tag/"+slug+"/entities", nil))
	var res TermArchiveResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil || rec.Code != 200 {
		t.Fatalf("archive = %d %s (%v)", rec.Code, rec.Body, err)
	}
	if res.Total != 1 || len(res.Items) != 1 || res.Items[0].Slug != slug+"-shown" {
		t.Errorf("archive lists %d total, items %+v; want only the public person", res.Total, res.Items)
	}

	terms, err := repo.ListPublicTerms(ctx, "tag", 1000)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range terms {
		if tc.ID == term.ID && tc.EntityCount != 1 {
			t.Errorf("term count = %d, want 1 (the hidden person is not counted)", tc.EntityCount)
		}
	}
}
