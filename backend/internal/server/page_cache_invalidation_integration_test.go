//go:build integration

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/cache"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/form"
	"hatamoto.dev/core/internal/media"
	"hatamoto.dev/core/internal/menu"
	"hatamoto.dev/core/internal/middleware"
	"hatamoto.dev/core/internal/redirect"
	"hatamoto.dev/core/internal/taxonomy"
	"hatamoto.dev/core/internal/worker"
)

// With a long HATAMOTO_PAGE_CACHE_TTL a stale page lives for hours, so every
// change that can alter public HTML must purge the page cache. This drives the
// real server wiring (entity service, hooks, worker, taxonomy, media, menus,
// redirects, ...) against a migrated Postgres:
//
//	DATABASE_URL=... go test -tags integration ./internal/server -run PageCacheInvalidation
func TestPageCacheInvalidation_Integration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set — skipping integration test")
	}
	t.Setenv("JWT_SECRET", strings.Repeat("j", 40))
	t.Setenv("HATAMOTO_CANONICAL_SLUG", "pc-it")
	t.Setenv("HATAMOTO_SITE_NAME", "PC IT")
	t.Setenv("SMTP_FROM", "it@example.test")
	t.Setenv("SMTP_FROM_NAME", "PC IT")
	t.Setenv("PUBLIC_BASE_URL", "https://pc-it.example.test")
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("ENVIRONMENT", "development")
	t.Setenv("HATAMOTO_PAGE_CACHE_TTL", "24h")
	t.Setenv("MEDIA_UPLOAD_DIR", t.TempDir())
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PageCacheTTL != 24*time.Hour {
		t.Fatalf("PageCacheTTL = %s", cfg.PageCacheTTL)
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
	schemaID, livePageID := uuid.New(), uuid.New()
	liveSlug, laterSlug := "live-"+sfx, "later-"+sfx
	exec(`INSERT INTO hatamoto_entity_schemas (id,name,slug,schema,settings) VALUES ($1,$2,$2,'{"type":"object"}',$3)`,
		schemaID, "pcit-"+sfx, `{"url_pattern":"/{slug}"}`)
	exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,status,published_at,attributes)
		VALUES ($1,$2,'Live',$3,'published',now(),'{"page_sections":[]}'::jsonb)`, livePageID, schemaID, liveSlug)
	exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,status,published_at,attributes)
		VALUES ($1,$2,'Later',$3,'scheduled',now() - interval '1 minute','{"page_sections":[]}'::jsonb)`, uuid.New(), schemaID, laterSlug)
	var prevName string
	var prevHome *uuid.UUID
	hadSite := db.Pool().QueryRow(ctx, `SELECT company_name, homepage_entity_id FROM hatamoto_site_config`).Scan(&prevName, &prevHome) == nil
	exec(`INSERT INTO hatamoto_site_config (company_name, homepage_entity_id) VALUES ('PC IT', $1)
		ON CONFLICT (id) DO UPDATE SET homepage_entity_id = EXCLUDED.homepage_entity_id`, livePageID)
	t.Cleanup(func() {
		c := context.Background()
		if hadSite {
			_, _ = db.Pool().Exec(c, `UPDATE hatamoto_site_config SET homepage_entity_id=$1`, prevHome)
		} else {
			_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_site_config`)
		}
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_redirects WHERE source_path LIKE $1`, "%"+sfx+"%")
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_terms WHERE slug LIKE $1`, "%"+sfx+"%")
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_forms WHERE slug LIKE $1`, "%"+sfx+"%")
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_menus WHERE slug LIKE $1`, "%"+sfx+"%")
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_media WHERE filename LIKE $1`, "%"+sfx+"%")
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entities WHERE schema_id=$1`, schemaID)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entity_schemas WHERE id=$1`, schemaID)
	})

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := New(cfg, db, log)
	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		srv.router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec
	}
	realKey := func() string {
		return cache.RenderedPageKey(srv.renderEngine.Version(), "/"+liveSlug)
	}

	// --- Scheduled publish by the worker, with the real page cache. ---------
	if rec := get("/" + liveSlug); rec.Code != 200 {
		t.Fatalf("live page: %d %.300s", rec.Code, rec.Body)
	}
	if _, err := srv.infra.Cache.Get(ctx, realKey()); err != nil {
		t.Fatalf("live page was not cached: %v", err)
	}
	if rec := get("/" + laterSlug); rec.Code != 404 {
		t.Fatalf("scheduled page before its time: %d", rec.Code)
	}
	pw := worker.NewPublishWorker(db, log)
	pw.SetEvents(scheduledPublishSink{s: srv, next: srv.entitySink})
	promoted, err := pw.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(promoted) == 0 {
		t.Fatal("worker promoted nothing")
	}
	// No hooks.Wait: the purge must not depend on the async plugin hooks.
	if _, err := srv.infra.Cache.Get(ctx, realKey()); err == nil {
		t.Error("worker scheduled publish left the cached page behind")
	}
	if rec := get("/" + laterSlug); rec.Code != 200 {
		t.Errorf("scheduled page after the worker ran: %d", rec.Code)
	}
	srv.hooks.Wait()

	// --- Every other path: swap in a probe and assert it is purged. ---------
	probe := newRenderProbe(log)
	srv.infra.CacheInvalidator = cache.NewCacheInvalidator(probe.c, log)
	expect := func(name string, fn func()) {
		t.Helper()
		probe.prime()
		fn()
		srv.hooks.Wait()
		if !probe.cleared() {
			t.Errorf("%s did not purge the page cache", name)
		}
	}
	svc := srv.entityService
	var created *entity.Entity
	expect("entity create (admin/API/MCP/plugin share the service)", func() {
		created, err = svc.CreateEntity(ctx, nil, &entity.CreateEntityInput{
			SchemaID: schemaID, Name: "New", Slug: "new-" + sfx, Attributes: json.RawMessage(`{}`)})
		if err != nil {
			t.Fatal(err)
		}
	})
	expect("entity update", func() {
		name := "Renamed"
		if _, err := svc.UpdateEntity(ctx, created.ID, nil, &entity.UpdateEntityInput{Name: &name}); err != nil {
			t.Fatal(err)
		}
	})
	expect("entity publish", func() {
		if _, err := svc.UpdateEntityStatus(ctx, created.ID, nil, entity.StatusPublished); err != nil {
			t.Fatal(err)
		}
	})
	expect("entity unpublish", func() {
		if _, err := svc.UpdateEntityStatus(ctx, created.ID, nil, entity.StatusDraft); err != nil {
			t.Fatal(err)
		}
	})
	expect("entity delete", func() {
		if err := svc.DeleteEntity(ctx, created.ID); err != nil {
			t.Fatal(err)
		}
	})
	expect("term create", func() {
		if _, err := srv.taxonomyService.CreateTerm(ctx, taxonomy.CreateTermInput{Kind: "tag", Name: "T " + sfx, Slug: "t-" + sfx}); err != nil {
			t.Fatal(err)
		}
	})
	expect("media update", func() {
		var buf bytes.Buffer
		_ = png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 4, 4)))
		m, err := srv.mediaService.Upload(ctx, nil, &buf, "pc-"+sfx+".png", "image/png", int64(buf.Len()))
		if err != nil {
			t.Fatal(err)
		}
		alt := "alt"
		if _, err := srv.mediaService.Update(ctx, m.ID, &media.UpdateMediaInput{AltText: &alt}); err != nil {
			t.Fatal(err)
		}
	})
	expect("menu create", func() {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"M `+sfx+`","slug":"m-`+sfx+`"}`))
		req = req.WithContext(middleware.ContextWithClaims(req.Context(), &auth.Claims{UserID: uuid.New(), Role: "owner"}))
		rec := httptest.NewRecorder()
		srv.handleCreateMenu(rec, req)
		if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
			t.Fatalf("create menu: %d %.200s", rec.Code, rec.Body)
		}
	})
	expect("plugin menus.* (service write without a handler)", func() {
		m, err := srv.menuService.CreateMenu(ctx, &menu.CreateMenuInput{Name: "P " + sfx, Slug: "p-" + sfx})
		if err != nil {
			t.Fatal(err)
		}
		probe.prime()
		if _, err := srv.menuService.CreateMenuItem(ctx, m.ID, &menu.CreateMenuItemInput{Label: "x", LinkType: menu.LinkTypeCustom}); err != nil {
			t.Fatal(err)
		}
		if !probe.cleared() {
			t.Error("menu item create did not purge")
		}
		probe.prime()
		if err := srv.menuService.DeleteMenu(ctx, m.ID); err != nil {
			t.Fatal(err)
		}
	})
	expect("form update", func() {
		f, err := srv.formService.CreateForm(ctx, &form.CreateFormInput{Name: "F " + sfx, Slug: "f-" + sfx})
		if err != nil {
			t.Fatal(err)
		}
		probe.prime()
		req := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"name":"F2 `+sfx+`"}`))
		req.SetPathValue("id", f.ID.String())
		rec := httptest.NewRecorder()
		srv.handleUpdateForm(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("update form: %d %.200s", rec.Code, rec.Body)
		}
	})
	expect("term merge", func() {
		a, err := srv.taxonomyService.CreateTerm(ctx, taxonomy.CreateTermInput{Kind: "tag", Name: "MA " + sfx, Slug: "ma-" + sfx})
		if err != nil {
			t.Fatal(err)
		}
		b, err := srv.taxonomyService.CreateTerm(ctx, taxonomy.CreateTermInput{Kind: "tag", Name: "MB " + sfx, Slug: "mb-" + sfx})
		if err != nil {
			t.Fatal(err)
		}
		probe.prime()
		if err := srv.taxonomyService.MergeTerms(ctx, a.ID, b.ID); err != nil {
			t.Fatal(err)
		}
	})
	// Content written by SQL outside the entity service (linking v2).
	srcID := uuid.New()
	exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,status,published_at,attributes)
		VALUES ($1,$2,'Src',$3,'published',now(),'{"content":"<p>read Live now</p>"}'::jsonb)`, srcID, schemaID, "src-"+sfx)
	expect("linking insert (admin suggestion panel)", func() {
		out, err := (linkingAdmin{s: srv}).Insert(ctx, srcID, livePageID, "Live")
		if err != nil {
			t.Fatal(err)
		}
		if !out.Inserted {
			t.Fatalf("link was not inserted: %+v", out)
		}
	})
	expect("link rewrite after a slug change", func() {
		a := &entityLinkSyncAdapter{db: db, changed: srv.invalidateRendered}
		if err := a.RewriteForEntity(ctx, livePageID, "/moved-"+sfx, "slug_change", nil); err != nil {
			t.Fatal(err)
		}
	})
	expect("site settings", func() { srv.invalidateSiteSettings(ctx) })
	expect("schema change", func() {
		raw := json.RawMessage(`{"url_pattern":"/{slug}"}`)
		if _, err := svc.UpdateSchema(ctx, schemaID, &entity.UpdateSchemaInput{Settings: &raw}); err != nil {
			t.Fatal(err)
		}
	})
	expect("author/byline change", func() { srv.authorHandlers.OnChange(httptest.NewRequest(http.MethodPost, "/", nil)) })
	if srv.packages != nil {
		expect("theme/plugin package change", func() { srv.packages.OnChange(ctx) })
	}

	// --- Redirects are read live: a redirect created over a cached page wins,
	// and removing it brings the page back, without any purge. --------------
	if rec := get("/" + liveSlug); rec.Code != 200 {
		t.Fatalf("live page: %d %.300s", rec.Code, rec.Body)
	}
	rd, err := srv.redirectService.CreateRedirect(ctx, &redirect.CreateRedirectInput{
		SourcePath: "/" + liveSlug, TargetURL: "/elsewhere-" + sfx, StatusCode: 301})
	if err != nil {
		t.Fatal(err)
	}
	if rec := get("/" + liveSlug); rec.Code != 301 {
		t.Errorf("redirect created over a cached page: %d, want 301", rec.Code)
	}
	if err := srv.redirectService.DeleteRedirect(ctx, rd.ID); err != nil {
		t.Fatal(err)
	}
	if rec := get("/" + liveSlug); rec.Code != 200 {
		t.Errorf("after the redirect was deleted: %d, want 200", rec.Code)
	}
}
