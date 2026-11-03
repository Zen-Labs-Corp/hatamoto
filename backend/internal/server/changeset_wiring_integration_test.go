//go:build integration

package server

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/redirect"
)

// pathIsLivePage against a real database: what the changeset redirect writer
// may not shadow. Pages, locale-prefixed pages, the home pages and archives
// are live; an unknown path and a wildcard are not a page (a wildcard source is
// refused by redirect.SourceShadows instead).
func TestPathIsLivePage_Integration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	t.Setenv("JWT_SECRET", strings.Repeat("j", 40))
	t.Setenv("HATAMOTO_CANONICAL_SLUG", "live-it")
	t.Setenv("HATAMOTO_SITE_NAME", "Live IT")
	t.Setenv("PUBLIC_BASE_URL", "https://live-it.example.test")
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

	sfx := "x" + uuid.NewString()[:5]
	pageSchema, blogSchema, archiveSchema := "lv-page-"+sfx, "lv-blog-"+sfx, "lv-arch-"+sfx
	ids := map[string]uuid.UUID{pageSchema: uuid.New(), blogSchema: uuid.New(), archiveSchema: uuid.New()}
	exec(`INSERT INTO hatamoto_entity_schemas (id,name,slug,schema,settings) VALUES ($1,$2,$2,'{"type":"object"}',$3)`,
		ids[pageSchema], pageSchema, `{"url_pattern":"/{slug}"}`)
	exec(`INSERT INTO hatamoto_entity_schemas (id,name,slug,schema,settings) VALUES ($1,$2,$2,'{"type":"object"}',$3)`,
		ids[blogSchema], blogSchema, `{"url_pattern":"/blog-`+sfx+`/{slug}"}`)
	exec(`INSERT INTO hatamoto_entity_schemas (id,name,slug,schema,settings) VALUES ($1,$2,$2,'{"type":"object"}',$3)`,
		ids[archiveSchema], archiveSchema, `{"url_pattern":"/arch-`+sfx+`/{slug}","archive_path":"/archive-`+sfx+`"}`)
	ins := func(schema, slug, locale string) {
		t.Helper()
		exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,locale,status,published_at,attributes)
			VALUES ($1,$2,$3,$3,$4,'published',now(),'{}'::jsonb)`, uuid.New(), ids[schema], slug, locale)
	}
	exec(`DELETE FROM hatamoto_entities WHERE slug = 'home' AND locale IN ('en','fr')`)
	about, onlyFr := "about-"+sfx, "only-fr-"+sfx
	ins(pageSchema, about, "en")
	ins(pageSchema, onlyFr, "fr")
	ins(blogSchema, "post-"+sfx, "en")

	var prev struct {
		name, lang, xdef string
		locales          []byte
		prefixAll        bool
	}
	hadSite := db.Pool().QueryRow(ctx, `SELECT company_name, language, locales, prefix_all, x_default_locale FROM hatamoto_site_config`).
		Scan(&prev.name, &prev.lang, &prev.locales, &prev.prefixAll, &prev.xdef) == nil
	exec(`INSERT INTO hatamoto_site_config (company_name, language, locales, prefix_all) VALUES ('Live IT', 'en', $1::jsonb, false)
		ON CONFLICT (id) DO UPDATE SET language = 'en', locales = EXCLUDED.locales, prefix_all = false, homepage_entity_id = NULL`,
		`[{"tag":"en","segment":""},{"tag":"fr","segment":"fr"}]`)
	t.Cleanup(func() {
		c := context.Background()
		if hadSite {
			_, _ = db.Pool().Exec(c, `UPDATE hatamoto_site_config SET locales=$1::jsonb, prefix_all=$2, x_default_locale=$3`, string(prev.locales), prev.prefixAll, prev.xdef)
		} else {
			_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_site_config`)
		}
		for _, id := range ids {
			_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entities WHERE schema_id=$1`, id)
			_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entity_schemas WHERE id=$1`, id)
		}
	})

	srv := New(cfg, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	for _, tc := range []struct {
		path string
		live bool
	}{
		{"/" + about, true},
		{"/" + about + "/", true},
		{"/fr/" + onlyFr, true},
		{"/" + onlyFr, false}, // the fr page is not served unprefixed
		{"/", true},
		{"/fr", true},
		{"/blog-" + sfx + "/post-" + sfx, true},
		{"/blog-" + sfx + "/*", false}, // not a page: SourceShadows refuses the wildcard
		{"/archive-" + sfx, true},
		{"/nothing-" + sfx, false},
	} {
		got, err := srv.pathIsLivePage(ctx, tc.path)
		if err != nil {
			t.Fatalf("%s: %v", tc.path, err)
		}
		if got != tc.live {
			t.Errorf("pathIsLivePage(%q) = %v, want %v", tc.path, got, tc.live)
		}
	}

	// What a plugin may not put a redirect on, wired the way the writer does.
	for _, tc := range []struct {
		origin, source string
		shadows        bool
	}{
		{"plugin:acme", "/", true},
		{"plugin:acme", "/blog-" + sfx + "/*", true},
		{"plugin:acme", "/fr/" + onlyFr, true},
		{"plugin:acme", "/nothing-" + sfx, false},
		{"owner", "/" + about, false},
	} {
		got, err := redirect.SourceShadows(ctx, tc.origin, tc.source, srv.pathIsLivePage)
		if err != nil || got != tc.shadows {
			t.Errorf("SourceShadows(%s, %q) = %v, %v; want %v", tc.origin, tc.source, got, err, tc.shadows)
		}
	}
}
