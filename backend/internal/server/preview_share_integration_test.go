//go:build integration

package server

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/cache"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/previewlink"
)

type shareRig struct {
	t      *testing.T
	ctx    context.Context
	db     *database.DB
	srv    *Server
	logs   *bytes.Buffer
	schema uuid.UUID
	sfx    string
}

func newShareRig(t *testing.T) *shareRig {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set — skipping integration test")
	}
	t.Setenv("JWT_SECRET", strings.Repeat("j", 40))
	t.Setenv("HATAMOTO_CANONICAL_SLUG", "ps-it")
	t.Setenv("HATAMOTO_SITE_NAME", "PS IT")
	t.Setenv("SMTP_FROM", "it@example.test")
	t.Setenv("SMTP_FROM_NAME", "PS IT")
	t.Setenv("PUBLIC_BASE_URL", "https://ps-it.example.test")
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("ENVIRONMENT", "development")
	t.Setenv("HATAMOTO_PAGE_CACHE_TTL", "24h")
	t.Setenv("MEDIA_UPLOAD_DIR", t.TempDir())
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
	r := &shareRig{t: t, ctx: ctx, db: db, schema: uuid.New(), sfx: uuid.NewString()[:6], logs: &bytes.Buffer{}}
	var hadSite bool
	_ = db.Pool().QueryRow(ctx, `SELECT true FROM hatamoto_site_config LIMIT 1`).Scan(&hadSite)
	if !hadSite {
		r.exec(`INSERT INTO hatamoto_site_config (company_name) VALUES ('PS IT')`)
		t.Cleanup(func() { _, _ = db.Pool().Exec(context.Background(), `DELETE FROM hatamoto_site_config`) })
	}
	r.exec(`INSERT INTO hatamoto_entity_schemas (id,name,slug,schema,settings) VALUES ($1,$2,$2,'{"type":"object"}','{"url_pattern":"/{slug}"}')`,
		r.schema, "psit-"+r.sfx)
	t.Cleanup(func() {
		c := context.Background()
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entities WHERE schema_id=$1`, r.schema)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entity_schemas WHERE id=$1`, r.schema)
	})
	r.srv = New(cfg, db, slog.New(slog.NewTextHandler(r.logs, nil)))
	return r
}

func (r *shareRig) exec(q string, args ...any) {
	r.t.Helper()
	if _, err := r.db.Pool().Exec(r.ctx, q, args...); err != nil {
		r.t.Fatalf("%s: %v", q, err)
	}
}

func (r *shareRig) entity(slug, status string) uuid.UUID {
	r.t.Helper()
	id := uuid.New()
	r.exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,status,attributes)
		VALUES ($1,$2,$3,$4,$5,'{"page_sections":[]}'::jsonb)`, id, r.schema, "Draft "+slug, slug, status)
	return id
}

func (r *shareRig) get(path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	r.srv.router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func (r *shareRig) link(id uuid.UUID) (string, *previewlink.Link) {
	r.t.Helper()
	tok, l, err := r.srv.previewLinks.Create(r.ctx, id, nil, "Ann", time.Hour)
	if err != nil {
		r.t.Fatal(err)
	}
	return tok, l
}

func TestPreviewShareServesTheDraft(t *testing.T) {
	r := newShareRig(t)
	slug := "draft-" + r.sfx
	id := r.entity(slug, "draft")
	tok, _ := r.link(id)

	if rec := r.get("/" + slug); rec.Code != 404 {
		t.Fatalf("the draft is public: %d", rec.Code)
	}
	for range 2 {
		rec := r.get("/preview/" + tok)
		if rec.Code != 200 {
			t.Fatalf("preview: %d %.300s\n%s", rec.Code, rec.Body, r.logs)
		}
		body := rec.Body.String()
		for _, want := range []string{"Preview of a draft", "Draft " + slug, "noindex"} {
			if !strings.Contains(body, want) {
				t.Errorf("body lacks %q", want)
			}
		}
		if strings.Contains(body, "data-hm-change") {
			t.Error("change marks in a draft preview")
		}
		h := rec.Header()
		if h.Get("X-Robots-Tag") != "noindex" || h.Get("Cache-Control") != "no-store" ||
			h.Get("Referrer-Policy") != "no-referrer" || h.Get("X-Frame-Options") != "DENY" {
			t.Errorf("headers = %v", h)
		}
		csps := h.Values("Content-Security-Policy")
		if len(csps) != 2 || !strings.Contains(csps[1], "sandbox") || !strings.Contains(csps[1], "script-src 'none'") {
			t.Errorf("CSP = %v", csps)
		}
		if strings.Contains(csps[0], "'unsafe-inline'") && strings.Contains(strings.SplitAfter(csps[0], "script-src")[1][:40], "'unsafe-inline'") {
			t.Errorf("script-src allows inline: %s", csps[0])
		}
	}
	// Never in the page cache, under the preview URL or the public one.
	for _, p := range []string{"/preview/" + tok, "/" + slug} {
		if _, err := r.srv.infra.Cache.Get(r.ctx, cache.RenderedPageKey(r.srv.renderEngine.Version(), p)); err == nil {
			t.Errorf("%s entered the page cache", p)
		}
	}
	// Not listed anywhere crawlers read.
	for _, p := range []string{"/sitemap.xml", "/llms.txt", "/feed.xml"} {
		if rec := r.get(p); strings.Contains(rec.Body.String(), slug) {
			t.Errorf("%s lists the draft", p)
		}
	}
	// The token never reaches the log.
	if strings.Contains(r.logs.String(), tok) {
		t.Error("the token was logged")
	}
}

func TestPreviewShareRefusals(t *testing.T) {
	r := newShareRig(t)
	slug := "gone-" + r.sfx
	id := r.entity(slug, "draft")
	assert404 := func(name, path string) {
		t.Helper()
		rec := r.get(path)
		if rec.Code != 404 || strings.TrimSpace(rec.Body.String()) != "404 page not found" {
			t.Errorf("%s: %d %.80q, want the plain 404", name, rec.Code, rec.Body)
		}
		if rec.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s: 404 is cacheable", name)
		}
	}

	tok, l := r.link(id)
	assert404("tampered", "/preview/"+tok[:len(tok)-1]+"x")
	assert404("short", "/preview/abc")
	assert404("unknown", "/preview/"+strings.Repeat("A", 43))

	if ok, err := r.srv.previewLinks.Revoke(r.ctx, id, l.ID); err != nil || !ok {
		t.Fatal(ok, err)
	}
	assert404("revoked", "/preview/"+tok)

	tok2, l2 := r.link(id)
	r.exec(`UPDATE hatamoto_preview_links SET expires_at = now() - interval '1 second' WHERE id = $1`, l2.ID)
	assert404("expired", "/preview/"+tok2)

	tok3, _ := r.link(id)
	r.exec(`DELETE FROM hatamoto_entities WHERE id = $1`, id)
	assert404("deleted entity", "/preview/"+tok3)
}

func TestPreviewSharePublishedRedirectsToThePublicPage(t *testing.T) {
	r := newShareRig(t)
	slug := "live-" + r.sfx
	id := r.entity(slug, "draft")
	tok, _ := r.link(id)
	r.exec(`UPDATE hatamoto_entities SET status='published', published_at=now() WHERE id=$1`, id)
	rec := r.get("/preview/" + tok)
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/"+slug {
		t.Fatalf("published: %d Location=%q", rec.Code, rec.Header().Get("Location"))
	}
	if strings.Contains(rec.Body.String(), "Preview of a draft") {
		t.Error("the draft was served after publishing")
	}
}

func TestPreviewShareRouteIsReserved(t *testing.T) {
	if got := redactPreviewToken("/preview/SECRET"); got != "/preview/<token>" {
		t.Errorf("redactPreviewToken = %q", got)
	}
	if got := redactPreviewToken("/previews/x"); got != "/previews/x" {
		t.Errorf("redactPreviewToken touched %q", got)
	}
	_ = io.Discard
}

// A dated post, a custom type without sections and a page in a locale that is
// not public all render as drafts through the same loaders.
func TestPreviewShareOtherKinds(t *testing.T) {
	r := newShareRig(t)
	postSchema, plainSchema := uuid.New(), uuid.New()
	r.exec(`INSERT INTO hatamoto_entity_schemas (id,name,slug,schema,settings) VALUES ($1,$2,$2,'{"type":"object"}','{"url_pattern":"/{year}/{month}/{slug}"}')`, postSchema, "psdp-"+r.sfx)
	r.exec(`INSERT INTO hatamoto_entity_schemas (id,name,slug,schema,settings) VALUES ($1,$2,$2,'{"type":"object"}','{"url_pattern":"/things/{slug}"}')`, plainSchema, "psth-"+r.sfx)
	t.Cleanup(func() {
		c := context.Background()
		_, _ = r.db.Pool().Exec(c, `DELETE FROM hatamoto_entities WHERE schema_id = ANY($1)`, []uuid.UUID{postSchema, plainSchema})
		_, _ = r.db.Pool().Exec(c, `DELETE FROM hatamoto_entity_schemas WHERE id = ANY($1)`, []uuid.UUID{postSchema, plainSchema})
	})
	mk := func(schema uuid.UUID, slug, locale string) uuid.UUID {
		id := uuid.New()
		r.exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,locale,status,published_at,attributes)
			VALUES ($1,$2,$3,$4,$5,'draft',now(),'{}'::jsonb)`, id, schema, "Title "+slug, slug, locale)
		return id
	}
	var site string
	if err := r.db.Pool().QueryRow(r.ctx, `SELECT public.hatamoto_site_language()`).Scan(&site); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		id    uuid.UUID
		title string
	}{
		"dated post":  {mk(postSchema, "post-"+r.sfx, site), "Title post-" + r.sfx},
		"custom type": {mk(plainSchema, "thing-"+r.sfx, site), "Title thing-" + r.sfx},
		"translation": {mk(plainSchema, "chose-"+r.sfx, "fr"), "Title chose-" + r.sfx},
	} {
		id := c.id
		tok, _ := r.link(id)
		rec := r.get("/preview/" + tok)
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Preview of a draft") || !strings.Contains(rec.Body.String(), c.title) {
			t.Errorf("%s: %d %.200s", name, rec.Code, rec.Body)
		}
	}
}
