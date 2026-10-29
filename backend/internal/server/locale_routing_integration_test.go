//go:build integration

package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
)

// Locale routing over the full server: /fr/... serves the fr rows, the default
// locale stays unprefixed, shared content is served everywhere, /fr is that
// locale's home, and with prefix-all the bare root is a 302 to the default home.
//
//	DATABASE_URL=... go test -tags integration ./internal/server -run LocaleRouting
func TestLocaleRouting_Integration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set - skipping locale routing integration test")
	}
	t.Setenv("JWT_SECRET", strings.Repeat("j", 40))
	t.Setenv("HATAMOTO_CANONICAL_SLUG", "locale-it")
	t.Setenv("HATAMOTO_SITE_NAME", "Locale IT")
	t.Setenv("PUBLIC_BASE_URL", "https://locale-it.example.test")
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
	schemaSlug, slug, sharedSlug := "lri-"+sfx, "about-"+sfx, "shared-"+sfx
	schemaID := uuid.New()
	exec(`INSERT INTO hatamoto_entity_schemas (id,name,slug,schema,settings) VALUES ($1,$2,$2,'{"type":"object"}',$3)`,
		schemaID, schemaSlug, `{"url_pattern":"/{slug}"}`)
	ins := func(id uuid.UUID, slug, locale, headline string) {
		t.Helper()
		exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,locale,status,published_at,attributes)
			VALUES ($1,$2,$3,$3,$4,'published',now(),$5::jsonb)`, id, schemaID, slug, locale,
			`{"page_sections":[{"id":"s1","type":"hero","sort_order":1,"content":{"headline":"`+headline+`"}}]}`)
	}
	enHome := uuid.New()
	exec(`DELETE FROM hatamoto_entities WHERE slug = 'home' AND locale IN ('en','fr')`)
	ins(enHome, "home", "en", "HEADLINE-EN-HOME")
	ins(uuid.New(), "home", "fr", "HEADLINE-FR-HOME")
	ins(uuid.New(), slug, "en", "HEADLINE-EN-ABOUT")
	ins(uuid.New(), slug, "fr", "HEADLINE-FR-ABOUT")
	ins(uuid.New(), sharedSlug, "und", "HEADLINE-SHARED")
	ins(uuid.New(), "only-fr-"+sfx, "fr", "HEADLINE-ONLY-FR")

	var prev struct {
		name, lang, xdef string
		locales          []byte
		prefixAll        bool
	}
	hadSite := db.Pool().QueryRow(ctx, `SELECT company_name, language, locales, prefix_all, x_default_locale FROM hatamoto_site_config`).
		Scan(&prev.name, &prev.lang, &prev.locales, &prev.prefixAll, &prev.xdef) == nil
	setLocales := func(prefixAll bool, locales string) {
		exec(`INSERT INTO hatamoto_site_config (company_name, language, locales, prefix_all) VALUES ('Locale IT', 'en', $1::jsonb, $2)
			ON CONFLICT (id) DO UPDATE SET language = 'en', locales = EXCLUDED.locales, prefix_all = EXCLUDED.prefix_all, homepage_entity_id = NULL`, locales, prefixAll)
	}
	t.Cleanup(func() {
		c := context.Background()
		if hadSite {
			_, _ = db.Pool().Exec(c, `UPDATE hatamoto_site_config SET locales=$1::jsonb, prefix_all=$2, x_default_locale=$3`, string(prev.locales), prev.prefixAll, prev.xdef)
		} else {
			_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_site_config`)
		}
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entities WHERE schema_id=$1 OR (slug='home' AND locale IN ('en','fr'))`, schemaID)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entity_schemas WHERE id=$1`, schemaID)
	})
	setLocales(false, `[{"tag":"en","segment":""},{"tag":"fr","segment":"fr"},{"tag":"nl","segment":"nl"}]`)

	srv := New(cfg, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	do := func(method, path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		srv.router.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
		return rec
	}
	expect := func(path string, code int, has ...string) {
		t.Helper()
		rec := do(http.MethodGet, path)
		if rec.Code != code {
			t.Errorf("GET %s = %d, want %d", path, rec.Code, code)
			return
		}
		for _, w := range has {
			if !strings.Contains(rec.Body.String(), w) {
				t.Errorf("GET %s lacks %q", path, w)
			}
		}
	}

	expect("/fr/"+slug, 200, "HEADLINE-FR-ABOUT")
	expect("/"+slug, 200, "HEADLINE-EN-ABOUT")
	expect("/"+sharedSlug, 200, "HEADLINE-SHARED")
	expect("/fr", 200, "HEADLINE-FR-HOME")
	expect("/", 200, "HEADLINE-EN-HOME")
	expect("/fr/only-fr-"+sfx, 200, "HEADLINE-ONLY-FR")
	expect("/only-fr-"+sfx, 404)
	expect("/nl/"+slug, 404) // no nl copy
	expect("/frx/"+slug, 404)
	expect("/fr/search?q=x", 200)
	if rec := do(http.MethodGet, "/fr/"+slug); !strings.Contains(rec.Body.String(), `https://locale-it.example.test/fr/`+slug) {
		t.Errorf("/fr/%s canonical is not prefixed", slug)
	}

	// Prefix-all: the default locale moves under its own segment.
	setLocales(true, `[{"tag":"en","segment":"en"},{"tag":"fr","segment":"fr"}]`)
	srv.siteConfig.Invalidate()
	srv.invalidateSiteSettings(ctx)
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		rec := do(method, "/?utm=1")
		if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/en?utm=1" || rec.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s / = %d -> %q (%q), want 302 -> /en?utm=1, no-store", method, rec.Code, rec.Header().Get("Location"), rec.Header().Get("Cache-Control"))
		}
	}
	expect("/en", 200, "HEADLINE-EN-HOME")
	expect("/en/"+slug, 200, "HEADLINE-EN-ABOUT")
	expect("/fr/"+slug, 200, "HEADLINE-FR-ABOUT")
	expect("/"+slug, 404)
	expect("/"+sharedSlug, 200, "HEADLINE-SHARED")
}
