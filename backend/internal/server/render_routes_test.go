package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/config"

	"hatamoto.dev/core/internal/httpx"

	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/middleware"
	"hatamoto.dev/core/internal/render"
)

func stub(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }
}

// baseRouter mimics the API-only routing of setupRoutes.
func baseRouter() (*httpx.Group, http.Handler) {
	mux := http.NewServeMux()
	r := httpx.NewGroup(mux, "")
	mountOpsRoutes(r, stub("health"), stub("ready"), stub("media"))
	r.Group("/api/v1").HandleFunc("GET /x", stub("api"))
	return r, httpx.WithErrorBodies(mux, func(w http.ResponseWriter, status int) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"error":"not found"}`))
	})
}

func getPath(r http.Handler, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w
}

func TestRenderRoutesDisabledRegistersNoRootRoutes(t *testing.T) {
	r, h := baseRouter()
	(&Server{}).mountRenderRoutes(r) // renderEngine == nil: bare binary
	for _, p := range []string{"/", "/robots.txt", "/sitemap.xml", "/about", "/theme/assets/hm.js"} {
		if w := getPath(h, p); w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), `"error"`) {
			t.Errorf("%s: status %d body %q, want JSON 404", p, w.Code, w.Body)
		}
	}
	if w := getPath(h, "/api/v1/x"); w.Body.String() != "api" {
		t.Errorf("api broken: %q", w.Body)
	}
}

func TestRenderRoutesEnabledPrecedence(t *testing.T) {
	f := newRenderFixture(t)
	r, h := baseRouter()
	mountRenderRoutes(r, renderRoutes{
		pages: f.h, assets: stub("assets"), robots: stub("robots"), sitemap: stub("sitemap"),
		subSitemap: stub("sub"), llms: stub("llms"), feed: stub("feed"), xsl: stub("xsl"), formPost: stub("form"),
	})

	for path, want := range map[string]string{
		"/feed.xml": "feed", "/atom.xml": "feed",
		"/api/v1/x": "api", "/health": "health", "/media/a.png": "media",
		"/robots.txt": "robots", "/sitemap.xml": "sitemap", "/sitemap-en.xml": "sub",
		"/llms.txt": "llms", "/sitemap.xsl": "xsl", "/theme/assets/hm.js": "assets",
	} {
		if got := getPath(h, path).Body.String(); got != want {
			t.Errorf("%s -> %q, want %q", path, got, want)
		}
	}

	if w := getPath(h, "/"); !isHTML(w) {
		t.Errorf("/ not rendered by the theme: %d", w.Code)
	}
	f.next = func() (*routeResult, pageOutcome, error) {
		return &routeResult{NotFound: true}, pageOutcome{Status: 404, NotFound: true}, nil
	}
	if w := getPath(h, "/foo"); !isHTML(w) || w.Code != http.StatusNotFound {
		t.Errorf("/foo: status %d html %v", w.Code, isHTML(w))
	}
	// Unknown /api paths stay JSON, never an HTML page.
	if w := getPath(h, "/api/other"); isHTML(w) || !strings.Contains(w.Body.String(), `"error"`) {
		t.Errorf("/api/other: %d %q", w.Code, w.Body)
	}

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/forms/contact/submit", nil))
	if w.Body.String() != "form" {
		t.Errorf("form post not routed: %q", w.Body)
	}
}

func TestRenderRoutesServeRootIcons(t *testing.T) {
	f := newRenderFixture(t)
	engine, err := render.New(render.Options{})
	if err != nil {
		t.Fatal(err)
	}
	r, h := baseRouter()
	mountRenderRoutes(r, renderRoutes{
		pages: f.h, assets: engine.AssetHandler(), robots: stub("robots"), sitemap: stub("sitemap"),
		subSitemap: stub("sub"), llms: stub("llms"), xsl: stub("xsl"), formPost: stub("form"),
	})
	for _, name := range render.RootIcons {
		w := getPath(h, "/"+name)
		if w.Code != http.StatusOK || w.Body.Len() == 0 {
			t.Errorf("/%s: status %d, %d bytes", name, w.Code, w.Body.Len())
		}
		if cc := w.Header().Get("Cache-Control"); !strings.Contains(cc, "max-age=2592000") {
			t.Errorf("/%s: Cache-Control %q, want a long max-age", name, cc)
		}
		if isHTML(w) {
			t.Errorf("/%s rendered as a page", name)
		}
	}
	if ct := getPath(h, "/favicon.ico").Header().Get("Content-Type"); ct != "image/x-icon" {
		t.Errorf("favicon.ico Content-Type %q", ct)
	}
}

func TestRenderAdminRoutesRoleEnforcement(t *testing.T) {
	h := newPreviewHandlers(t)
	mux := http.NewServeMux()
	r := httpx.NewGroup(mux, "")
	{
		r := r.Group("/api/v1")
		{
			r := r.Group("", func(next http.Handler) http.Handler { // stand-in for AuthMiddleware
				return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					if role := req.Header.Get("X-Test-Role"); role != "" {
						req = req.WithContext(middleware.ContextWithClaims(req.Context(), &auth.Claims{Role: role}))
					}
					next.ServeHTTP(w, req)
				})
			})
			mountRenderAdmin(r, h)
		}
	}

	for _, tc := range []struct {
		role string
		want int
	}{{"", 401}, {"viewer", 403}, {"editor", 200}, {"owner", 200}} {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/render/preview", strings.NewReader(`{"attributes":{}}`))
		if tc.role != "" {
			req.Header.Set("X-Test-Role", tc.role)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != tc.want {
			t.Errorf("role %q: status %d, want %d", tc.role, w.Code, tc.want)
		}
	}
}

// HEAD must reach the same handlers as GET, not the page catch-all.
func TestRenderRoutesHeadHitsRealHandlers(t *testing.T) {
	f := newRenderFixture(t)
	r, h := baseRouter()
	mountRenderRoutes(r, renderRoutes{
		pages: f.h, assets: stub("assets"), robots: stub("robots"), sitemap: stub("sitemap"),
		subSitemap: stub("sub"), llms: stub("llms"), xsl: stub("xsl"), formPost: stub("form"),
	})
	f.next = func() (*routeResult, pageOutcome, error) {
		t.Error("HEAD fell through to the page catch-all")
		return &routeResult{NotFound: true}, pageOutcome{Status: 404, NotFound: true}, nil
	}
	for _, path := range []string{"/health", "/ready", "/media/a.png", "/robots.txt", "/sitemap.xml",
		"/sitemap-en.xml", "/llms.txt", "/sitemap.xsl"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodHead, path, nil))
		if w.Code != http.StatusOK || isHTML(w) {
			t.Errorf("HEAD %s: status %d html %v", path, w.Code, isHTML(w))
		}
	}
}

// A richtext field of the active theme schema that is not named "content"
// (split.lede) must be sanitised on save, not only at render time.
func TestSetupRenderWiresSaveTimeRichTextSanitising(t *testing.T) {
	s := &Server{cfg: &config.Config{}, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	s.setupRender()
	t.Cleanup(func() { entity.SetFieldKindResolver(nil) })
	if s.renderEngine == nil {
		t.Fatal("render engine not built")
	}
	in := json.RawMessage(`{"page_sections":[{"id":"s","type":"split","content":{"heading":"H","lede":"<p>ok</p><script>alert(1)</script>"}}]}`)
	out, err := entity.SanitizeAttributesAgainst(in, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "<script") || !strings.Contains(string(out), "ok") {
		t.Errorf("split.lede not sanitised on save: %s", out)
	}
}

// Wrong-shaped values (array / object where the schema says a richtext
// string) must not carry raw HTML past save-time sanitising. Uses the real
// render Engine.FieldKind, not a fake resolver.
func TestSetupRenderSaveTimeSanitisingCoversWrongShapes(t *testing.T) {
	s := &Server{cfg: &config.Config{}, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	s.setupRender()
	t.Cleanup(func() { entity.SetFieldKindResolver(nil) })
	if s.renderEngine == nil {
		t.Fatal("render engine not built")
	}
	in := json.RawMessage(`{"page_sections":[
	 {"id":"a","type":"split","content":{"lede":["<p>a</p><img src=x onerror=alert(1)>"],"footnote":{"k":"<img src=x onerror=alert(2)>"}}},
	 {"id":"b","type":"faq","content":{"items":{"answer":"<img src=x onerror=alert(3)>"}}}]}`)
	out, err := entity.SanitizeAttributesAgainst(in, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "onerror") {
		t.Errorf("raw HTML survived in wrong-shaped richtext value: %s", out)
	}
}

// isHTML reports a themed HTML page (the theme renderer's output).
func isHTML(w *httptest.ResponseRecorder) bool {
	return strings.HasPrefix(w.Header().Get("Content-Type"), "text/html")
}
