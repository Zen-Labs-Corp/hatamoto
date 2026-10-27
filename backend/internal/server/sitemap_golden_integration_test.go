//go:build integration

package server

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/siteconfig"
)

// TestSitemapGolden_Integration renders the sitemap handlers over a fixed
// database with EMPTY schema settings and compares the XML with the golden
// files in testdata/sitemap, which pin the sitemap layout. It needs a pristine migrated database (it asserts the whole
// document), so it only runs with SITEMAP_GOLDEN_DB=1:
//
//	SITEMAP_GOLDEN_DB=1 DATABASE_URL=... go test -tags=integration ./internal/server -run SitemapGolden
//
// SITEMAP_GOLDEN_UPDATE=1 rewrites the golden files.
func TestSitemapGolden_Integration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" || os.Getenv("SITEMAP_GOLDEN_DB") == "" {
		t.Skip("SITEMAP_GOLDEN_DB / DATABASE_URL not set — skipping")
	}
	ctx := context.Background()
	db, err := database.New(ctx, config.DatabaseConfig{URL: dsn, MaxConnections: 4, MinConnections: 1,
		MaxConnLifetime: time.Minute, MaxConnIdleTime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Pool().Exec(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}

	t.Cleanup(func() {
		for _, q := range []string{
			`DELETE FROM hatamoto_entities`,
			`DELETE FROM hatamoto_entity_schemas WHERE slug IN ('blog','note')`,
		} {
			_, _ = db.Pool().Exec(ctx, q)
		}
	})
	exec(`INSERT INTO hatamoto_entity_schemas (name,slug,schema) VALUES ('Blog','blog','{"type":"object"}'),('Note','note','{"type":"object"}')`)

	n := 0
	ins := func(schema, slug, attrs, meta string) {
		t.Helper()
		n++
		exec(`INSERT INTO hatamoto_entities (schema_id,name,slug,status,published_at,updated_at,attributes,metadata)
			SELECT s.id,$2,$2,'published',now(),$3::timestamptz,$4::jsonb,$5::jsonb FROM hatamoto_entity_schemas s WHERE s.slug=$1`,
			schema, slug, fmt.Sprintf("2025-03-%02dT10:00:00Z", n), attrs, meta)
	}
	ins("page", "about", `{}`, `{}`)
	ins("page", "contact", `{}`, `{}`)
	ins("page", "shared", `{}`, `{}`)
	ins("page", "hidden", `{}`, `{"no_index":"true"}`)
	ins("page", "home", `{}`, `{}`)
	ins("page", "blog", `{}`, `{}`)
	ins("page", "post", `{}`, `{}`)
	ins("page", "landing", `{}`, `{}`)
	ins("post", "hello", `{}`, `{}`)
	ins("post", "second", `{}`, `{}`)
	ins("blog", "legacy-post", `{}`, `{}`)
	ins("note", "n1", `{}`, `{}`)
	ins("note", "n2", `{}`, `{}`)

	exec(`INSERT INTO hatamoto_site_config (company_name, homepage_entity_id)
		VALUES ('Golden', (SELECT id FROM hatamoto_entities WHERE slug = 'landing'))
		ON CONFLICT (id) DO UPDATE SET homepage_entity_id = EXCLUDED.homepage_entity_id`)

	entitySvc := entity.NewService(entity.NewPostgresSchemaRepository(db), nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv := &Server{db: db, entityService: entitySvc, siteConfig: siteconfig.NewStore(db, slog.Default()),
		logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	today := time.Now().Format("2006-01-02")

	render := func(h func(http.ResponseWriter, *http.Request), sub string) string {
		req := httptest.NewRequest(http.MethodGet, "http://example.test/sitemap-"+sub+".xml", nil)
		req.SetPathValue("file", "sitemap-"+sub+".xml")
		rec := httptest.NewRecorder()
		h(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("sitemap %s: status %d: %s", sub, rec.Code, rec.Body.String())
		}
		return strings.ReplaceAll(rec.Body.String(), today, "{{today}}")
	}

	for name, sub := range map[string]string{"index": "", "blog": "blog", "misc": "misc"} {
		h := srv.handlePublicSubSitemap
		if sub == "" {
			h = srv.handlePublicSitemap
		}
		got := render(h, sub)
		path := filepath.Join("testdata", "sitemap", name+".xml")
		if os.Getenv("SITEMAP_GOLDEN_UPDATE") != "" {
			if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if got != string(want) {
			t.Errorf("sitemap %s differs from golden:\n--- got\n%s\n--- want\n%s", name, got, want)
		}
	}
}
