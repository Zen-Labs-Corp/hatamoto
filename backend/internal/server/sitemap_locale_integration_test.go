//go:build integration

package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/entity"
)

// Per-locale sitemaps over the full server: the index lists a file per locale,
// every listed file answers 200 and linked copies carry xhtml alternates.
//
//	DATABASE_URL=... go test -tags integration ./internal/server -run SitemapLocale
func TestSitemapLocale_Integration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set - skipping hreflang integration test")
	}
	t.Setenv("JWT_SECRET", strings.Repeat("j", 40))
	t.Setenv("HATAMOTO_CANONICAL_SLUG", "sitemap-it")
	t.Setenv("HATAMOTO_SITE_NAME", "Sitemap IT")
	t.Setenv("PUBLIC_BASE_URL", "https://sitemap-it.example.test")
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("ENVIRONMENT", "development")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	db, err := database.New(ctx, cfg.Database)
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

	sfx := uuid.NewString()[:6]
	schemaSlug, slug := "hl-"+sfx, "about-"+sfx
	schemaID := uuid.New()
	exec(`INSERT INTO hatamoto_entity_schemas (id,name,slug,schema,settings) VALUES ($1,$2,$2,'{"type":"object"}',$3)`,
		schemaID, schemaSlug, `{"url_pattern":"/{slug}"}`)
	ins := func(id uuid.UUID, slug, locale string) {
		t.Helper()
		exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,locale,status,published_at,attributes)
			VALUES ($1,$2,$3,$3,$4,'published',now(),'{}'::jsonb)`, id, schemaID, slug, locale)
	}
	exec(`DELETE FROM hatamoto_entities WHERE slug = 'home' AND locale IN ('en','fr')`)
	ins(uuid.New(), "home", "en")
	ins(uuid.New(), "home", "fr")
	enID, frID := uuid.New(), uuid.New()
	ins(enID, slug, "en")
	ins(frID, slug+"-fr", "fr")
	if _, err := entity.NewGroupStore(db.Pool()).Link(ctx, enID, frID); err != nil {
		t.Fatal(err)
	}
	var prev struct {
		locales   []byte
		prefixAll bool
		xdef      string
	}
	hadSite := db.Pool().QueryRow(ctx, `SELECT locales, prefix_all, x_default_locale FROM hatamoto_site_config`).Scan(&prev.locales, &prev.prefixAll, &prev.xdef) == nil
	exec(`INSERT INTO hatamoto_site_config (company_name, language, locales, prefix_all) VALUES ('Sitemap IT', 'en', $1::jsonb, false)
		ON CONFLICT (id) DO UPDATE SET language = 'en', locales = EXCLUDED.locales, prefix_all = false, homepage_entity_id = NULL, x_default_locale = ''`,
		`[{"tag":"en","segment":""},{"tag":"fr","segment":"fr"},{"tag":"nl","segment":"nl"}]`)
	t.Cleanup(func() {
		c := context.Background()
		if hadSite {
			_, _ = db.Pool().Exec(c, `UPDATE hatamoto_site_config SET locales=$1::jsonb, prefix_all=$2, x_default_locale=$3`, string(prev.locales), prev.prefixAll, prev.xdef)
		} else {
			_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_site_config`)
		}
		_, _ = db.Pool().Exec(c, `UPDATE hatamoto_entities SET translation_group=NULL WHERE schema_id=$1`, schemaID)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entities WHERE schema_id=$1 OR (slug='home' AND locale IN ('en','fr'))`, schemaID)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entity_schemas WHERE id=$1`, schemaID)
	})

	srv := New(cfg, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	get := func(path string) string {
		rec := httptest.NewRecorder()
		srv.router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != 200 {
			t.Fatalf("GET %s = %d", path, rec.Code)
		}
		return rec.Body.String()
	}
	base := "https://sitemap-it.example.test"
	index := get("/sitemap.xml")
	locRe := regexp.MustCompile(`<loc>` + regexp.QuoteMeta(base) + `(/sitemap-[^<]+\.xml)</loc>`)
	files := locRe.FindAllStringSubmatch(index, -1)
	var all strings.Builder
	var names []string
	for _, m := range files {
		names = append(names, m[1])
		all.WriteString(get(m[1]))
	}
	for _, want := range []string{"/sitemap-misc.xml", "/sitemap-fr-misc.xml", "/sitemap-nl-misc.xml"} {
		if !slices.Contains(names, want) {
			t.Errorf("index lacks %s: %v", want, names)
		}
	}
	out := all.String()
	for _, want := range []string{
		`<loc>` + base + `/` + slug + `</loc>`,
		`<loc>` + base + `/fr/` + slug + `-fr</loc>`,
		`hreflang="fr" href="` + base + `/fr/` + slug + `-fr"`,
		`hreflang="x-default" href="` + base + `/` + slug + `"`,
		`xmlns:xhtml=`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("sitemaps lack %s", want)
		}
	}
}
