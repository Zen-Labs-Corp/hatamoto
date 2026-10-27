//go:build integration

package server

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
)

// A freshly migrated database with the first-boot seed: the smoke checks must pass
// through the real router (seeded home or setup redirect).
func TestSmokeOnFreshInstall_Integration(t *testing.T) {
	base := os.Getenv("DATABASE_URL")
	if base == "" {
		t.Skip("DATABASE_URL not set")
	}
	dsn := scratchMigratedDB(t, base)
	t.Setenv("JWT_SECRET", strings.Repeat("j", 40))
	t.Setenv("HATAMOTO_CANONICAL_SLUG", "smoke-it")
	t.Setenv("HATAMOTO_SITE_NAME", "Smoke IT")
	t.Setenv("SMTP_FROM", "it@example.test")
	t.Setenv("SMTP_FROM_NAME", "Smoke IT")
	t.Setenv("PUBLIC_BASE_URL", "https://smoke-it.example.test")
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("ENVIRONMENT", "development")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	db, err := database.New(context.Background(), cfg.Database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)

	// What app.firstBoot seeds before the server starts: site config and a home page.
	for _, q := range []string{
		`INSERT INTO hatamoto_site_config (company_name) VALUES ('Smoke IT') ON CONFLICT (id) DO NOTHING`,
		`INSERT INTO hatamoto_entities (schema_id, name, slug, status, attributes, published_at)
		 SELECT s.id, 'Smoke IT', 'home', 'published',
		   jsonb_build_object('page_sections', jsonb_build_array(jsonb_build_object('id','w','type','hero','sort_order',1,
		     'content', jsonb_build_object('headline','Smoke IT','subheadline','up')))), now()
		 FROM hatamoto_entity_schemas s WHERE s.slug = 'page'`,
	} {
		if _, err := db.Pool().Exec(context.Background(), q); err != nil {
			t.Fatal(err)
		}
	}

	s := New(cfg, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	res := s.runSmoke()
	if !res.Pass {
		t.Fatalf("fresh install fails its own smoke: %+v", res)
	}
}
