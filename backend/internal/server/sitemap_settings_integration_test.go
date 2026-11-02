//go:build integration

package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/seo"
	"hatamoto.dev/core/internal/siteconfig"
)

// The sitemap handlers honour the saved SEO > Sitemap settings: path
// exclusion, the per-entry hide flag, taxonomy files, images, omitted
// priority/changefreq and the per-file URL limit. The settings row is removed
// afterwards, so the default-output golden test still sees a pristine site.
func TestSitemapSettings_Integration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
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

	tag := uuid.NewString()[:6]
	slug := func(s string) string { return "smx" + tag + "-" + s }
	t.Cleanup(func() {
		_, _ = db.Pool().Exec(ctx, `DELETE FROM hatamoto_entities WHERE slug LIKE $1`, "smx"+tag+"-%")
		_, _ = db.Pool().Exec(ctx, `DELETE FROM hatamoto_terms WHERE kind = $1`, "smk"+tag)
		_, _ = db.Pool().Exec(ctx, `DELETE FROM hatamoto_seo_settings`)
	})
	var entityIDs = map[string]uuid.UUID{}
	ins := func(schema, name, attrs string) {
		t.Helper()
		var id uuid.UUID
		if err := db.Pool().QueryRow(ctx, `
			INSERT INTO hatamoto_entities (schema_id, name, slug, status, published_at, attributes)
			SELECT s.id, $1, $1, 'published', now(), $2::jsonb FROM hatamoto_entity_schemas s WHERE s.slug = $3
			RETURNING id`, slug(name), attrs, schema).Scan(&id); err != nil {
			t.Fatal(err)
		}
		entityIDs[name] = id
	}
	// Without a configured homepage the first published page is served as "/",
	// so a dedicated entity takes that role while the test runs.
	ins("page", "start", `{}`)
	var prevHome *uuid.UUID
	var hadConfig bool
	if err := db.Pool().QueryRow(ctx, `SELECT true, homepage_entity_id FROM hatamoto_site_config LIMIT 1`).Scan(&hadConfig, &prevHome); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if hadConfig {
			_, _ = db.Pool().Exec(ctx, `UPDATE hatamoto_site_config SET homepage_entity_id = $1`, prevHome)
		} else {
			_, _ = db.Pool().Exec(ctx, `DELETE FROM hatamoto_site_config`)
		}
	})
	exec(`INSERT INTO hatamoto_site_config (company_name, homepage_entity_id) VALUES ('Sitemap settings', $1)
		ON CONFLICT (id) DO UPDATE SET homepage_entity_id = EXCLUDED.homepage_entity_id`, entityIDs["start"])
	ins("post", "gallery", `{"og_image":"/media/smx.png","featured_image":"/media/smx.png"}`)
	ins("page", "legal-terms", `{}`)
	ins("page", "legal-privacy", `{}`)
	ins("page", "secret", `{"hide_from_sitemap":"true"}`)

	kind := "smk" + tag
	var termID uuid.UUID
	if err := db.Pool().QueryRow(ctx, `INSERT INTO hatamoto_terms (kind, slug, name) VALUES ($1, 'news', 'News') RETURNING id`, kind).Scan(&termID); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO hatamoto_entity_terms (entity_id, term_id) VALUES ($1, $2)`, entityIDs["gallery"], termID)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := &Server{db: db, entityService: entity.NewService(entity.NewPostgresSchemaRepository(db), nil, logger),
		siteConfig: siteconfig.NewStore(db, slog.Default()), seoStore: seo.NewStore(db.Pool()), logger: logger}
	save := func(fn func(*seo.Settings)) {
		t.Helper()
		if err := srv.seoStore.Merge(ctx, fn); err != nil {
			t.Fatal(err)
		}
	}
	fetch := func(sub string) (int, string) {
		h, name := srv.handlePublicSubSitemap, "sitemap-"+sub+".xml"
		if sub == "" {
			h, name = srv.handlePublicSitemap, "sitemap.xml"
		}
		req := httptest.NewRequest(http.MethodGet, "http://example.test/"+name, nil)
		req.SetPathValue("file", name)
		rec := httptest.NewRecorder()
		h(rec, req)
		return rec.Code, rec.Body.String()
	}
	body := func(sub string) string {
		t.Helper()
		code, b := fetch(sub)
		if code != http.StatusOK {
			t.Fatalf("sitemap %q: status %d: %s", sub, code, b)
		}
		return b
	}
	loc := func(s string) string { return "<loc>http://example.test" + s + "</loc>" }
	has := func(doc, want string, present bool) {
		t.Helper()
		if strings.Contains(doc, want) != present {
			t.Errorf("contains %q = %v, want %v in:\n%s", want, !present, present, doc)
		}
	}

	// Nothing saved: hidden entries are left out, the rest is listed in full.
	misc := body("misc")
	has(misc, loc("/"+slug("legal-terms")), true)
	has(misc, loc("/"+slug("secret")), false)
	has(misc, "<priority>", true)
	has(misc, "image:", false)
	blog := body("blog")
	has(blog, "/"+slug("gallery")+"</loc>", true)
	has(blog, "image:", false)
	has(body(""), "sitemap-taxonomy-"+kind, false)

	// Path exclusion drops the matching URLs only.
	save(func(s *seo.Settings) {
		s.Sitemap.ExcludePaths = []string{"/" + slug("legal") + "-*"}
	})
	misc = body("misc")
	has(misc, loc("/"+slug("legal-terms")), false)
	has(misc, loc("/"+slug("legal-privacy")), false)
	has(body("blog"), "/"+slug("gallery")+"</loc>", true)

	// Taxonomy files, images, and no priority/changefreq.
	save(func(s *seo.Settings) {
		s.Sitemap = seo.Sitemap{Taxonomy: true, Images: true, NoStats: true}
	})
	has(body(""), "/sitemap-taxonomy-"+kind+".xml", true)
	has(body("taxonomy-"+kind), loc("/"+kind+"/news"), true)
	blog = body("blog")
	has(blog, `xmlns:image="`+sitemapImageNS+`"`, true)
	has(blog, "<image:loc>http://example.test/media/smx.png</image:loc>", true)
	if n := strings.Count(blog, "/media/smx.png"); n != 1 {
		t.Errorf("image listed %d times, want once", n)
	}
	has(blog, "<priority>", false)
	has(blog, "<changefreq>", false)
	has(body("misc"), "<priority>", false)

	// A one-URL limit moves the rest of misc into further files.
	save(func(s *seo.Settings) { s.Sitemap = seo.Sitemap{MaxURLs: 1} })
	has(body(""), "/sitemap-misc-2.xml", true)
	body("misc-2")
	if code, _ := fetch("misc-9999"); code != http.StatusNotFound {
		t.Errorf("unknown child status = %d, want 404", code)
	}

	// The hide flag is not noindex: the entry stays out of the sitemap only.
	var noIndex bool
	if err := db.Pool().QueryRow(ctx, `SELECT `+entity.NoIndexSQL+` FROM hatamoto_entities e WHERE e.id = $1`, entityIDs["secret"]).Scan(&noIndex); err != nil {
		t.Fatal(err)
	}
	if noIndex {
		t.Error("hide_from_sitemap made the entry noindex")
	}
}
