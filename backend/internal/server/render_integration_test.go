//go:build integration

package server

import (
	"context"
	"encoding/json"
	"html"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/cache"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/middleware"
	"hatamoto.dev/core/internal/user"
)

// End-to-end smoke of the Go-rendered site against a migrated Postgres:
//
//	DATABASE_URL=... go test -tags integration ./internal/server -run RenderIntegration
//
// It builds the full server (all routes and middleware)
// and drives it through its router.
func TestRenderIntegration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set — skipping render integration test (needs a migrated Postgres)")
	}
	t.Setenv("JWT_SECRET", strings.Repeat("j", 40))
	t.Setenv("HATAMOTO_CANONICAL_SLUG", "render-it")
	t.Setenv("HATAMOTO_SITE_NAME", "Render IT")
	t.Setenv("SMTP_FROM", "it@example.test")
	t.Setenv("SMTP_FROM_NAME", "Render IT")
	t.Setenv("PUBLIC_BASE_URL", "https://render-it.example.test")
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

	// ---- fixtures: an isolated schema routed at /{slug}, pages, a form, a redirect.
	sfx := uuid.NewString()[:6]
	schemaSlug, formSlug := "rit-"+sfx, "rit-form-"+sfx
	schemaID, homeID, pageID, formID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	pageSlug, redirSrc := "about-"+sfx, "/old-"+sfx

	exec(`INSERT INTO hatamoto_entity_schemas (id,name,slug,schema,settings) VALUES ($1,$2,$2,'{"type":"object"}',$3)`,
		schemaID, schemaSlug, `{"url_pattern":"/{slug}"}`)
	sections := `[
	  {"id":"s1","type":"hero","sort_order":1,"content":{"headline":"Hello render","subheadline":"Served by Go"}},
	  {"id":"s2","type":"html","sort_order":2,"content":{"body":"<p>Raw body</p><script>alert(1)</script>"}},
	  {"id":"s3","type":"faq","sort_order":3,"content":{"heading":"FAQ","faqs":[{"question":"Why?","answer":"Because."}]}},
	  {"id":"s4","type":"cta","sort_order":4,"content":{"heading":"Go","buttonText":"Contact","buttonUrl":"/` + pageSlug + `"}},
	  {"id":"s5","type":"cta","sort_order":5,"isVisible":false,"content":{"heading":"Hidden"}}]`
	insEntity := func(id uuid.UUID, slug, name string, attrs string) {
		exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,status,published_at,attributes)
			VALUES ($1,$2,$3,$4,'published',now(),$5::jsonb)`, id, schemaID, name, slug, attrs)
	}
	insEntity(homeID, "home-"+sfx, "Home IT", `{"meta_description":"Home desc","page_sections":`+sections+`}`)
	insEntity(pageID, pageSlug, "About IT", `{"meta_title":"About meta title","page_sections":[
	  {"id":"a1","type":"contact_form","sort_order":1,"content":{"formSlug":"`+formSlug+`","heading":"Write us"}}]}`)
	exec(`INSERT INTO hatamoto_forms (id,name,slug,fields,settings,status) VALUES ($1,'IT form',$2,$3::jsonb,'{}','active')`, formID, formSlug,
		`[{"name":"email","label":"Email","type":"email","required":true,"position":1}]`)
	exec(`INSERT INTO hatamoto_redirects (source_path,target_url,status_code) VALUES ($1,$2,301)`, redirSrc, "/"+pageSlug)

	var prevName string
	var prevHome *uuid.UUID
	hadSite := db.Pool().QueryRow(ctx, `SELECT company_name, homepage_entity_id FROM hatamoto_site_config`).Scan(&prevName, &prevHome) == nil
	exec(`INSERT INTO hatamoto_site_config (company_name, homepage_entity_id) VALUES ('Render IT', $1)
		ON CONFLICT (id) DO UPDATE SET homepage_entity_id = EXCLUDED.homepage_entity_id`, homeID)
	t.Cleanup(func() {
		c := context.Background()
		if hadSite {
			_, _ = db.Pool().Exec(c, `UPDATE hatamoto_site_config SET homepage_entity_id=$1`, prevHome)
		} else {
			_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_site_config`)
		}
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_form_submissions WHERE form_id=$1`, formID)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_forms WHERE id=$1`, formID)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_redirects WHERE source_path=$1`, redirSrc)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entities WHERE schema_id=$1`, schemaID)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entity_schemas WHERE id=$1`, schemaID)
	})

	srv := New(cfg, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	do := func(method, path string, body string, hdr map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		srv.router.ServeHTTP(rec, req)
		return rec
	}

	// ISC-1: homepage rendered by the Go theme, one marker per visible section.
	home := do(http.MethodGet, "/", "", nil)
	if home.Code != 200 || !strings.Contains(home.Body.String(), "data-hm-section") {
		t.Fatalf("/: status %d, not rendered by the theme", home.Code)
	}
	body := home.Body.String()
	if got := strings.Count(body, "data-hm-section="); got != 4 {
		t.Errorf("/: %d section markers, want 4 (hidden section must not render)\n%s", got, body)
	}
	for _, want := range []string{`data-hm-contract="1"`, `data-hm-type="hero"`, `data-hm-field="headline"`, "<title>Home IT", "Hello render"} {
		if !strings.Contains(body, want) {
			t.Errorf("/: missing %q", want)
		}
	}
	if strings.Contains(body, "alert(1)") || strings.Contains(body, "Hidden") {
		t.Error("/: unsanitised script or hidden section leaked")
	}

	// ISC-2: page 200 with its meta title, redirect 301, unknown 404 as themed HTML.
	page := do(http.MethodGet, "/"+pageSlug, "", nil)
	if page.Code != 200 || !strings.Contains(page.Body.String(), "<title>About meta title") ||
		!strings.Contains(page.Body.String(), `data-hm-form="`+formSlug+`"`) {
		t.Errorf("page: status %d\n%s", page.Code, page.Body)
	}
	if r := do(http.MethodGet, redirSrc, "", nil); r.Code != 301 || r.Header().Get("Location") != "/"+pageSlug {
		t.Errorf("redirect: %d %q", r.Code, r.Header().Get("Location"))
	}
	if nf := do(http.MethodGet, "/nope-"+sfx, "", nil); nf.Code != 404 || !strings.Contains(nf.Header().Get("Content-Type"), "text/html") ||
		!strings.Contains(nf.Body.String(), "<html") {
		t.Errorf("unknown: %d %q", nf.Code, nf.Header().Get("Content-Type"))
	}

	// ISC-11: no-JS form post -> 303 ?submitted=1 and exactly one submission.
	post := do(http.MethodPost, "/forms/"+formSlug+"/submit",
		url.Values{"email": {"a@b.test"}, "_redirect": {"/" + pageSlug}}.Encode(),
		map[string]string{"Content-Type": "application/x-www-form-urlencoded"})
	if post.Code != http.StatusSeeOther || post.Header().Get("Location") != "/"+pageSlug+"?submitted=1" {
		t.Errorf("form post: %d %q", post.Code, post.Header().Get("Location"))
	}
	var n int
	if err := db.Pool().QueryRow(ctx, `SELECT count(*) FROM hatamoto_form_submissions WHERE form_id=$1`, formID).Scan(&n); err != nil || n != 1 {
		t.Errorf("submissions = %d (%v), want 1", n, err)
	}

	// Root documents.
	if r := do(http.MethodGet, "/robots.txt", "", nil); r.Code != 200 || !strings.Contains(r.Body.String(), "Sitemap: https://render-it.example.test/sitemap.xml") ||
		!strings.Contains(r.Body.String(), "Disallow: /api") {
		t.Errorf("robots: %d\n%s", r.Code, r.Body)
	}
	if r := do(http.MethodGet, "/sitemap.xml", "", nil); r.Code != 200 || !strings.Contains(r.Body.String(), "<sitemapindex") {
		t.Errorf("sitemap.xml: %d", r.Code)
	}
	if r := do(http.MethodGet, "/theme/assets/hm.js", "", nil); r.Code != 200 {
		t.Errorf("hm.js: %d", r.Code)
	}

	// Preview: 401 anonymous, 200 editor (viewers are refused: routes_boot_test covers the role gate); never persisted.
	previewBody := `{"schema":"page","attributes":{"meta_title":"Draft","page_sections":[{"id":"p1","type":"html","sort_order":1,"content":{"body":"<p>Unsaved draft</p><script>x()</script>"}}]}}`
	if r := do(http.MethodPost, "/api/v1/render/preview", previewBody, nil); r.Code != http.StatusUnauthorized {
		t.Errorf("preview anon: %d", r.Code)
	}
	token := func(role string) map[string]string {
		u, err := srv.userRepo.Create(ctx, &user.CreateUserInput{Email: role + "-" + uuid.NewString()[:6] + "@example.test", Role: user.Role(role), Name: role}, "x")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = db.Pool().Exec(context.Background(), `DELETE FROM hatamoto_users WHERE id=$1`, u.ID) })
		tok, err := srv.authService.IssueSession(ctx, u.ID, "it", "127.0.0.1")
		if err != nil {
			t.Fatal(err)
		}
		return map[string]string{"Authorization": "Bearer " + tok}
	}
	prev := do(http.MethodPost, "/api/v1/render/preview", previewBody, token("editor"))
	if prev.Code != 200 || !strings.Contains(prev.Body.String(), "Unsaved draft") || !strings.Contains(prev.Body.String(), "data-hm-preview") ||
		strings.Contains(prev.Body.String(), "x()") || prev.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("preview editor: %d cache %q\n%s", prev.Code, prev.Header().Get("Cache-Control"), prev.Body)
	}
	theme := do(http.MethodGet, "/api/v1/render/theme", "", token("editor"))
	var info struct {
		Name     string `json:"name"`
		Sections []struct {
			Type string `json:"type"`
		} `json:"sections"`
	}
	if err := json.Unmarshal(theme.Body.Bytes(), &info); err != nil || theme.Code != 200 || info.Name == "" || len(info.Sections) == 0 {
		t.Errorf("theme: %d %v %s", theme.Code, err, theme.Body)
	}

	// ISC-16: JSON API and infrastructure routes are untouched by rendering.
	if r := do(http.MethodGet, "/api/v1/public/knowledge-graph", "", nil); r.Code != 200 || !strings.HasPrefix(r.Header().Get("Content-Type"), "application/json") {
		t.Errorf("knowledge-graph: %d %q", r.Code, r.Header().Get("Content-Type"))
	}
	if r := do(http.MethodGet, "/health", "", nil); r.Code != 200 || r.Body.String() != "ok" || !strings.HasPrefix(r.Header().Get("Content-Type"), "text/plain") {
		t.Errorf("/health: %d %q %q", r.Code, r.Header().Get("Content-Type"), r.Body)
	}
	if r := do(http.MethodGet, "/api/v1/nope", "", nil); r.Code != 404 || !strings.HasPrefix(r.Header().Get("Content-Type"), "application/json") {
		t.Errorf("/api/v1/nope: %d %q", r.Code, r.Header().Get("Content-Type"))
	}

	// An Accept: application/ld+json request must still get the HTML page
	// (the render path strips Accept before resolving; otherwise the loader
	// answers with a bare JSON-LD outcome and the page blanks).
	ld := do(http.MethodGet, "/"+pageSlug, "", map[string]string{"Accept": "application/ld+json"})
	if ld.Code != 200 || !strings.Contains(ld.Body.String(), "<title>About meta title") {
		t.Errorf("ld+json Accept: %d\n%s", ld.Code, ld.Body)
	}

	// Every URL a sitemap lists must render 200: a crawler is told these pages
	// exist.
	var sitemapURLs []string
	locRe := regexp.MustCompile(`<loc>([^<]+)</loc>`)
	locs := func(path string) []string {
		rec := do(http.MethodGet, path, "", nil)
		if rec.Code != 200 {
			t.Fatalf("%s: status %d", path, rec.Code)
		}
		var out []string
		for _, m := range locRe.FindAllStringSubmatch(rec.Body.String(), -1) {
			u, err := url.Parse(html.UnescapeString(m[1]))
			if err != nil {
				t.Fatalf("%s: bad loc %q", path, m[1])
			}
			out = append(out, u.RequestURI())
		}
		return out
	}
	for _, child := range locs("/sitemap.xml") {
		sitemapURLs = append(sitemapURLs, locs(child)...)
	}
	if !slices.Contains(sitemapURLs, "/"+pageSlug) {
		t.Errorf("sitemap does not list /%s: %v", pageSlug, sitemapURLs)
	}
	for _, u := range sitemapURLs {
		if rec := do(http.MethodGet, u, "", nil); rec.Code != 200 {
			t.Errorf("sitemap lists %s, which answers %d", u, rec.Code)
		}
	}

	// Cache invalidation is wired in server init: entity events and menu
	// handlers clear render:*. The invalidator is swapped for a recorder.
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	probe := newRenderProbe(log)
	srv.infra.CacheInvalidator = cache.NewCacheInvalidator(probe.c, log)
	cleared := probe.cleared
	srv.entitySink.Emit(ctx, entity.Event{Kind: entity.EventUpdated, ID: uuid.New(), Slug: "x"})
	srv.hooks.Wait()
	if !cleared() {
		t.Error("entity event did not clear render:* (registerRenderInvalidation not wired in server init?)")
	}
	probe.prime()
	menuReq := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"IT menu `+sfx+`","slug":"it-menu-`+sfx+`"}`))
	menuReq = menuReq.WithContext(middleware.ContextWithClaims(menuReq.Context(), &auth.Claims{UserID: uuid.New(), Role: "owner"}))
	srv.handleCreateMenu(httptest.NewRecorder(), menuReq)
	t.Cleanup(func() {
		_, _ = db.Pool().Exec(context.Background(), `DELETE FROM hatamoto_menus WHERE slug=$1`, "it-menu-"+sfx)
	})
	if !cleared() {
		t.Error("menu handler did not clear render:*")
	}
}
