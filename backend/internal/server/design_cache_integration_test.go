//go:build integration

package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/cache"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/user"
)

// B5 A1: a design token edited in Settings reaches the next public response of
// a cached, unchanged page (and the canvas render) without saving the page.
//
//	DATABASE_URL=... go test -tags integration ./internal/server -run DesignTokenCache
func TestDesignTokenCache_Integration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set: skipping (needs a migrated Postgres)")
	}
	t.Setenv("JWT_SECRET", strings.Repeat("j", 40))
	t.Setenv("HATAMOTO_CANONICAL_SLUG", "b5-it")
	t.Setenv("HATAMOTO_SITE_NAME", "B5 IT")
	t.Setenv("PUBLIC_BASE_URL", "https://b5-it.example.test")
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
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Pool().Exec(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	srv := New(cfg, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if srv.renderErr != nil {
		t.Fatal(srv.renderErr)
	}
	// A theme layer that opts in to the design tokens.
	if err := srv.renderEngine.SetTheme(fstest.MapFS{"theme.json": {Data: []byte(`{"name":"b5","version":"1.0.0","builder":{"tokens":true}}`)}}); err != nil {
		t.Fatal(err)
	}

	sfx := uuid.NewString()[:6]
	hash, err := auth.HashPassword("a-long-enough-password")
	if err != nil {
		t.Fatal(err)
	}
	owner, err := srv.userRepo.Create(ctx, &user.CreateUserInput{Email: "b5-" + sfx + "@example.test", Role: user.RoleOwner, Name: "Owner"}, hash)
	if err != nil {
		t.Fatal(err)
	}
	completeProfile(t, srv, owner) // the profile gate would redirect the save otherwise
	token, err := srv.authService.IssueSession(ctx, owner.ID, "it", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	schemaID, pageID := uuid.New(), uuid.New()
	slug := "tok-" + sfx
	doc := map[string]any{"v": 2, "nodes": []any{
		map[string]any{"id": "box", "t": "container", "p": map[string]any{"tag": "section"},
			"s": map[string]any{"base": map[string]any{"pt": "space-4", "fs": "lg", "shadow": "md"}},
			"c": []any{map[string]any{"id": "hd", "t": "heading", "p": map[string]any{"text": "Tokens", "level": 2}}}}}}
	attrs, _ := json.Marshal(map[string]any{"page_doc": doc})
	exec(`INSERT INTO hatamoto_entity_schemas (id,name,slug,schema,settings) VALUES ($1,$2,$2,'{"type":"object"}',$3)`, schemaID, "b5it-"+sfx, `{"url_pattern":"/{slug}"}`)
	exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,status,published_at,attributes) VALUES ($1,$2,'Tokens',$3,'published',now(),$4::jsonb)`, pageID, schemaID, slug, string(attrs))
	hadSite := db.Pool().QueryRow(ctx, `SELECT 1 FROM hatamoto_site_config`).Scan(new(int)) == nil
	if !hadSite {
		exec(`INSERT INTO hatamoto_site_config (company_name) VALUES ('B5 IT')`)
	}
	t.Cleanup(func() {
		c := context.Background()
		if !hadSite {
			_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_site_config`)
		}
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entities WHERE schema_id=$1`, schemaID)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entity_schemas WHERE id=$1`, schemaID)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_users WHERE id=$1`, owner.ID)
	})

	csrf := adminToken(t, srv.router, token)
	do := func(method, path string, form url.Values) *httptest.ResponseRecorder {
		t.Helper()
		var body io.Reader
		if form != nil {
			body = strings.NewReader(form.Encode())
		}
		req := httptest.NewRequest(method, path, body)
		if form != nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		if strings.HasPrefix(path, "/admin") {
			adminReq(req, token, csrf)
		}
		rec := httptest.NewRecorder()
		srv.router.ServeHTTP(rec, req)
		return rec
	}
	canvas := func() string {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"entity_id": pageID.String(), "schema": "b5it-" + sfx, "canvas": true, "path": "/" + slug,
			"attributes": map[string]any{"page_doc": doc}})
		req := httptest.NewRequest("POST", "/api/v1/render/preview", strings.NewReader(string(body)))
		rec := httptest.NewRecorder()
		srv.newRenderAdminHandlers(srv.renderEngine).handleRenderPreview(rec, req)
		if rec.Code != 200 {
			t.Fatalf("canvas render: %d %s", rec.Code, rec.Body.String())
		}
		return rec.Body.String()
	}
	key := func() string { return cache.RenderedPageKey(srv.renderEngine.Version(), "/"+slug) }

	// First response: no token block yet, and the page lands in the cache.
	rec := do("GET", "/"+slug, nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `id="hm-builder-css"`) {
		t.Fatalf("page: %d %.300s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "--hm-space-4:") {
		t.Fatal("token block before any token was saved")
	}
	if !strings.Contains(rec.Body.String(), "font-size:var(--hm-fs-lg,1.25rem)") {
		t.Fatal("opted-in page must compile fs to var()")
	}
	version := srv.renderEngine.Version()
	if _, err := srv.infra.Cache.Get(ctx, key()); err != nil {
		t.Fatalf("page was not cached: %v", err)
	}
	var updatedBefore string
	if err := db.Pool().QueryRow(ctx, `SELECT updated_at::text FROM hatamoto_entities WHERE id=$1`, pageID).Scan(&updatedBefore); err != nil {
		t.Fatal(err)
	}

	save := func(space string) {
		t.Helper()
		rec := do("POST", "/admin/theme/design-system", url.Values{"space_4": {space}, "shadow_md": {"0 2px 6px rgba(0,0,0,.2)"}})
		if rec.Code != 303 {
			t.Fatalf("save tokens: %d %s", rec.Code, rec.Body.String())
		}
		if _, err := srv.infra.Cache.Get(ctx, key()); err == nil {
			t.Fatal("token save left the cached page behind")
		}
	}
	for _, space := range []string{"2rem", "3rem"} {
		save(space)
		page := do("GET", "/"+slug, nil).Body.String()
		want := ":root{--hm-shadow-md:0 2px 6px rgba(0,0,0,.2);--hm-space-4:" + space + ";}"
		if !strings.Contains(page, want) {
			t.Fatalf("public page lacks %s:\n%s", want, page)
		}
		if c := canvas(); !strings.Contains(c, `<style id="hm-builder-base">`+want) {
			t.Fatalf("canvas lacks %s", want)
		}
		if srv.renderEngine.Version() != version {
			t.Fatal("the engine version changed: the test would not prove the purge")
		}
		var updated string
		if err := db.Pool().QueryRow(ctx, `SELECT updated_at::text FROM hatamoto_entities WHERE id=$1`, pageID).Scan(&updated); err != nil || updated != updatedBefore {
			t.Fatalf("the page entity was touched: %v %s != %s", err, updated, updatedBefore)
		}
	}

	// Reset removes the block again.
	if rec := do("POST", "/admin/theme/design-system/reset", url.Values{}); rec.Code != 303 {
		t.Fatalf("reset: %d", rec.Code)
	}
	if page := do("GET", "/"+slug, nil).Body.String(); strings.Contains(page, "--hm-space-4:") {
		t.Fatal("token block survived a reset")
	}
}
