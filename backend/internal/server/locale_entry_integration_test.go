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

// The entry redirect over the full server: "/" and a configured entry path go
// to the visitor's locale (saved cookie, then legacy cookie, then country, then
// the default), a warm page cache cannot skip it, /locale sets the cookie, and
// with the setting off nothing changes.
//
//	DATABASE_URL=... go test -tags integration ./internal/server -run LocaleEntry
func TestLocaleEntry_Integration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set - skipping locale entry integration test")
	}
	t.Setenv("JWT_SECRET", strings.Repeat("j", 40))
	t.Setenv("HATAMOTO_CANONICAL_SLUG", "locale-entry-it")
	t.Setenv("HATAMOTO_SITE_NAME", "Locale Entry IT")
	t.Setenv("PUBLIC_BASE_URL", "https://locale-entry-it.example.test")
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

	schemaID := uuid.New()
	exec(`INSERT INTO hatamoto_entity_schemas (id,name,slug,schema,settings) VALUES ($1,$2,$2,'{"type":"object"}',$3)`,
		schemaID, "lei-"+schemaID.String()[:8], `{"url_pattern":"/{slug}"}`)
	exec(`DELETE FROM hatamoto_entities WHERE slug = 'home' AND locale IN ('en','fr','es')`)
	for _, loc := range []string{"en", "fr", "es"} {
		exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,locale,status,published_at,attributes)
			VALUES ($1,$2,'home','home',$3,'published',now(),'{"page_sections":[{"id":"s1","type":"hero","sort_order":1,"content":{"headline":"HOME"}}]}'::jsonb)`,
			uuid.New(), schemaID, loc)
	}

	var prev struct {
		locales, entry []byte
		prefixAll      bool
	}
	hadSite := db.Pool().QueryRow(ctx, `SELECT locales, visitor_entry, prefix_all FROM hatamoto_site_config`).
		Scan(&prev.locales, &prev.entry, &prev.prefixAll) == nil
	t.Cleanup(func() {
		c := context.Background()
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entities WHERE schema_id=$1`, schemaID)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entity_schemas WHERE id=$1`, schemaID)
		if hadSite {
			_, _ = db.Pool().Exec(c, `UPDATE hatamoto_site_config SET locales=$1::jsonb, visitor_entry=$2::jsonb, prefix_all=$3`,
				string(prev.locales), string(prev.entry), prev.prefixAll)
		} else {
			_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_site_config`)
		}
	})
	srv := New(cfg, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	configure := func(prefixAll bool, locales, entry string) {
		t.Helper()
		exec(`INSERT INTO hatamoto_site_config (company_name, language, locales, prefix_all, visitor_entry) VALUES ('Locale Entry IT', 'en', $1::jsonb, $2, $3::jsonb)
			ON CONFLICT (id) DO UPDATE SET language = 'en', locales = EXCLUDED.locales, prefix_all = EXCLUDED.prefix_all, visitor_entry = EXCLUDED.visitor_entry`,
			locales, prefixAll, entry)
		srv.siteConfig.Invalidate()
		srv.invalidateSiteSettings(ctx)
	}
	type req struct {
		path    string
		country string
		cookies map[string]string
	}
	do := func(q req) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, q.path, nil)
		if q.country != "" {
			r.Header.Set("CF-IPCountry", q.country)
		}
		for k, v := range q.cookies {
			r.AddCookie(&http.Cookie{Name: k, Value: v})
		}
		rec := httptest.NewRecorder()
		srv.router.ServeHTTP(rec, r)
		return rec
	}
	const vary = "Cookie, CF-IPCountry, CloudFront-Viewer-Country"
	redirectsTo := func(name string, q req, want string) {
		t.Helper()
		rec := do(q)
		if rec.Code != http.StatusFound || rec.Header().Get("Location") != want {
			t.Errorf("%s: %d -> %q, want 302 -> %q", name, rec.Code, rec.Header().Get("Location"), want)
			return
		}
		if rec.Header().Get("Cache-Control") != "private, no-store" || !strings.Contains(strings.Join(rec.Header().Values("Vary"), ","), vary) {
			t.Errorf("%s: Cache-Control %q Vary %q", name, rec.Header().Get("Cache-Control"), rec.Header().Values("Vary"))
		}
	}

	const locales = `[{"tag":"en","segment":""},{"tag":"fr","segment":"fr","countries":["FR"]},{"tag":"es","segment":"es","countries":["ES"]}]`
	const entry = `{"enabled":true,"paths":["/contact-form"],"legacy_cookie":"old_loc"}`
	configure(false, locales, entry)

	redirectsTo("country", req{path: "/", country: "FR"}, "/fr")
	redirectsTo("cookie beats country", req{path: "/", country: "FR", cookies: map[string]string{"hm_locale": "es"}}, "/es")
	redirectsTo("country beats default over a junk cookie", req{path: "/", country: "FR", cookies: map[string]string{"hm_locale": "junk"}}, "/fr")
	redirectsTo("legacy cookie", req{path: "/", cookies: map[string]string{"old_loc": "es"}}, "/es")
	redirectsTo("core cookie beats legacy", req{path: "/", cookies: map[string]string{"hm_locale": "fr", "old_loc": "es"}}, "/fr")
	redirectsTo("entry path keeps the query", req{path: "/contact-form?x=1", cookies: map[string]string{"hm_locale": "fr"}}, "/fr/contact-form?x=1")

	// The redirect target is the canonical locale home: following it answers 200.
	if rec := do(req{path: "/fr"}); rec.Code != http.StatusOK {
		t.Errorf("GET /fr = %d, want 200", rec.Code)
	}

	// A default-locale visitor is served the page, with the Vary tokens.
	rec := do(req{path: "/"})
	if rec.Code != http.StatusOK || !strings.Contains(strings.Join(rec.Header().Values("Vary"), ","), vary) {
		t.Errorf("default visitor: %d, Vary %q", rec.Code, rec.Header().Values("Vary"))
	}
	// A warm page cache cannot skip the redirect for the next visitor.
	do(req{path: "/"})
	redirectsTo("after a warm cache", req{path: "/", country: "FR"}, "/fr")

	// The switcher.
	rec = do(req{path: "/locale?l=fr&to=/fr/x"})
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/fr/x" || !strings.Contains(rec.Header().Get("Set-Cookie"), "hm_locale=fr") {
		t.Errorf("/locale: %d -> %q, Set-Cookie %q", rec.Code, rec.Header().Get("Location"), rec.Header().Get("Set-Cookie"))
	}
	if rec := do(req{path: "/locale?l=zz"}); rec.Code != http.StatusNotFound || rec.Header().Get("Set-Cookie") != "" {
		t.Errorf("/locale?l=zz: %d, Set-Cookie %q", rec.Code, rec.Header().Get("Set-Cookie"))
	}

	// Setting off: the home is served as before, with no Vary added.
	configure(false, locales, `{}`)
	rec = do(req{path: "/", country: "FR"})
	if rec.Code != http.StatusOK || strings.Contains(strings.Join(rec.Header().Values("Vary"), ","), "CF-IPCountry") || rec.Header().Get("Location") != "" {
		t.Errorf("setting off: %d, Vary %q, Location %q", rec.Code, rec.Header().Values("Vary"), rec.Header().Get("Location"))
	}

	// Prefix-all: the default locale has its own segment and is redirected to too.
	configure(true, `[{"tag":"en","segment":"en"},{"tag":"fr","segment":"fr","countries":["FR"]}]`, entry)
	redirectsTo("prefix-all default", req{path: "/"}, "/en")
	redirectsTo("prefix-all country", req{path: "/", country: "FR"}, "/fr")
	redirectsTo("prefix-all entry path", req{path: "/contact-form"}, "/en/contact-form")
}
