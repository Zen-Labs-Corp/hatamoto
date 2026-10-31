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
	"hatamoto.dev/core/internal/entity"
)

// Hreflang over the full server: a page with copies in two of three locales
// carries lang, alternates and x-default; the missing copy is left out.
//
//	DATABASE_URL=... go test -tags integration ./internal/server -run Hreflang
func TestHreflang_Integration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set - skipping hreflang integration test")
	}
	t.Setenv("JWT_SECRET", strings.Repeat("j", 40))
	t.Setenv("HATAMOTO_CANONICAL_SLUG", "hreflang-it")
	t.Setenv("HATAMOTO_SITE_NAME", "Hreflang IT")
	t.Setenv("PUBLIC_BASE_URL", "https://hreflang-it.example.test")
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
	exec(`INSERT INTO hatamoto_site_config (company_name, language, locales, prefix_all) VALUES ('Hreflang IT', 'en', $1::jsonb, false)
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
	base := "https://hreflang-it.example.test"
	fr := get("/fr/" + slug + "-fr")
	for _, want := range []string{
		`<html lang="fr"`,
		`<link rel="alternate" hreflang="en" href="` + base + `/` + slug + `">`,
		`<link rel="alternate" hreflang="fr" href="` + base + `/fr/` + slug + `-fr">`,
		`<link rel="alternate" hreflang="x-default" href="` + base + `/` + slug + `">`,
	} {
		if !strings.Contains(fr, want) {
			t.Errorf("fr page lacks %s", want)
		}
	}
	if strings.Contains(fr, `hreflang="nl"`) {
		t.Error("the missing nl copy is in the head")
	}
	if en := get("/" + slug); !strings.Contains(en, `<html lang="en"`) || !strings.Contains(en, `hreflang="fr"`) {
		t.Error("en page: wrong lang or no fr alternate")
	}
	home := get("/fr")
	if !strings.Contains(home, `<html lang="fr"`) || strings.Count(home, `rel="alternate" hreflang=`) != 4 {
		t.Errorf("fr home: lang or alternates wrong:\n%s", home)
	}

	// With fr hidden, its copy is no alternate of the en page and no x-default
	// candidate, and its own address answers not found.
	exec(`UPDATE hatamoto_site_config SET locales = $1::jsonb`,
		`[{"tag":"en","segment":""},{"tag":"fr","segment":"fr","hidden":true},{"tag":"nl","segment":"nl"}]`)
	srv = New(cfg, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if en := get("/" + slug); strings.Contains(en, `hreflang="fr"`) || strings.Contains(en, "/fr/") {
		t.Errorf("en page lists the hidden fr copy:\n%s", en)
	}
	rec := httptest.NewRecorder()
	srv.router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/fr/"+slug+"-fr", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("hidden fr page = %d, want 404", rec.Code)
	}
}
