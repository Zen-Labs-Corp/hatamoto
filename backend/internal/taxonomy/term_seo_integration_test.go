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

// Term SEO survives a write and read through every scan path, and a save that
// carries no SEO leaves it alone.
func TestTermSEORoundTrip(t *testing.T) {
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

	slug := "seo-" + uuid.NewString()[:8]
	term, err := svc.CreateTerm(ctx, CreateTermInput{Kind: "tag", Name: slug, Slug: slug})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = db.Pool().Exec(ctx, `DELETE FROM hatamoto_terms WHERE id = $1`, term.ID) })
	if term.SEO != nil {
		t.Fatalf("new term has SEO %+v", term.SEO)
	}

	want := TermSEO{MetaTitle: "T", MetaDescription: "D", CanonicalURL: "/c", NoIndex: true}
	if _, err := svc.UpdateTerm(ctx, UpdateTermInput{ID: term.ID, Name: slug, Slug: slug, SEO: &want}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateTerm(ctx, UpdateTermInput{ID: term.ID, Name: slug, Slug: slug}); err != nil {
		t.Fatal(err)
	}
	byID, err := repo.GetTerm(ctx, term.ID)
	if err != nil || byID.SEO == nil || *byID.SEO != want {
		t.Fatalf("GetTerm: %+v, %v", byID, err)
	}
	bySlug, err := repo.GetTermBySlug(ctx, "tag", slug)
	if err != nil || bySlug.SEO == nil || *bySlug.SEO != want {
		t.Fatalf("GetTermBySlug: %+v, %v", bySlug, err)
	}
	list, err := repo.ListTerms(ctx, "tag", slug, 1, 10)
	if err != nil || len(list.Items) != 1 || list.Items[0].SEO == nil {
		t.Fatalf("ListTerms: %+v, %v", list, err)
	}
	chains, err := repo.Chains(ctx, []uuid.UUID{term.ID})
	if err != nil || len(chains[term.ID]) != 1 || chains[term.ID][0].SEO == nil {
		t.Fatalf("Chains: %+v, %v", chains, err)
	}

	if _, err := svc.UpdateTerm(ctx, UpdateTermInput{ID: term.ID, Name: slug, Slug: slug, SEO: &TermSEO{}}); err != nil {
		t.Fatal(err)
	}
	if cleared, _ := repo.GetTerm(ctx, term.ID); cleared == nil || cleared.SEO != nil {
		t.Fatalf("not cleared: %+v", cleared)
	}
}
