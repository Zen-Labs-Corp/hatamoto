package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/packages"
	"hatamoto.dev/core/internal/render"
)

// bootThemed404 is bootRoutes with the render handlers given the loaders a
// database would supply, so a random public path ends in the real themed 404.
func bootThemed404(t *testing.T) (http.Handler, *Server) {
	t.Helper()
	h, s := bootRoutes(t)
	publicThemeFixture(t, s)
	return h, s
}

// publicThemeFixture gives the route shell a themed, database-free public site.
func publicThemeFixture(t *testing.T, s *Server) {
	t.Helper()
	rh := s.renderHandlers
	if rh == nil {
		t.Fatal("route shell has no render handlers")
	}
	rh.baseURLPinned = true
	rh.loadHome = func(*http.Request) (*PublicPageResponse, pageOutcome, error) {
		return pageResponse("Home"), pageOutcome{Status: 200}, nil
	}
	rh.resolve = func(context.Context, string, *http.Request) (*routeResult, pageOutcome, error) {
		return &routeResult{NotFound: true}, pageOutcome{Status: 404, NotFound: true}, nil
	}
	rh.buildView = func(_ context.Context, r *http.Request, rr *routeResult) (*render.PageView, error) {
		v := pageViewFromRoute(rr, "https://x.io", requestPath(r))
		v.Site = render.SiteView{Name: "Fixture Site"}
		return v, nil
	}
	rh.log404 = nil
}

// refuseHandler is httpx.Refuse inside the same outer middleware the public
// flow has, as a real caller (the admin) runs it.
func refuseHandler(s *Server) http.Handler {
	return s.securityHeadersMiddleware(requestIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.writeStandardNotFound(w, r)
	})))
}

// dumpResponse is status, sorted headers (minus Date and the request id) and
// body: what must match byte for byte.
func dumpResponse(rec *httptest.ResponseRecorder) string {
	var keys []string
	for k := range rec.Header() {
		if k == "Date" || k == "X-Request-Id" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	fmt.Fprintf(&b, "%d\n", rec.Code)
	for _, k := range keys {
		fmt.Fprintf(&b, "%s: %s\n", k, strings.Join(rec.Header()[k], ", "))
	}
	b.WriteString("\n")
	b.WriteString(rec.Body.String())
	return b.String()
}

func serveDump(h http.Handler, method, target string) string {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return dumpResponse(rec)
}

func TestStandardNotFoundMatchesRandom404(t *testing.T) {
	full, s := bootThemed404(t)
	s.registerNotFound()
	for _, target := range []string{"/no-such-page-q7x2", "/wp-login.php", "/a/b/c?x=1"} {
		public := serveDump(full, "GET", target)
		if !strings.HasPrefix(public, "404\n") || !strings.Contains(public, "<html") {
			t.Fatalf("%s: public flow did not give a themed 404:\n%.300s", target, public)
		}
		if got := serveDump(refuseHandler(s), "GET", target); got != public {
			t.Fatalf("%s: responder differs from the public 404\n--- public ---\n%s\n--- responder ---\n%s", target, public, got)
		}
	}
}

func TestRefuseUsesRegisteredStandardResponder(t *testing.T) {
	_, s := bootThemed404(t)
	s.registerNotFound()
	rec := httptest.NewRecorder()
	httpx.Refuse(rec, httptest.NewRequest("GET", "/zz", nil))
	if rec.Code != 404 || !strings.Contains(rec.Body.String(), "Page not found") {
		t.Fatalf("Refuse after registration: %d %.200s", rec.Code, rec.Body.String())
	}
}

func TestNilEngineFallback(t *testing.T) {
	s := &Server{}
	rec := httptest.NewRecorder()
	s.writeStandardNotFound(rec, httptest.NewRequest("GET", "/x", nil))
	if rec.Code != 404 || !strings.Contains(rec.Body.String(), "not found") {
		t.Fatalf("got %d %q", rec.Code, rec.Body.String())
	}
}

// httpx.Refuse (sealed-admin refusals) answers as the public site does for the
// same method and query, not always with the themed HTML 404.
func TestRefuseMatchesPublicAnswerPerMethod(t *testing.T) {
	full, s := bootThemed404(t)
	s.registerNotFound()
	s.renderHandlers.preview = &themePreview{key: previewKey("k"), mgr: &packages.Manager{}}
	refuse := s.securityHeadersMiddleware(requestIDMiddleware(http.HandlerFunc(httpx.Refuse)))
	for _, m := range []string{"GET", "HEAD", "POST", "OPTIONS", "PUT", "DELETE", "PATCH"} {
		for _, q := range []string{"", "?hm_theme_preview=off"} {
			want := serveDump(full, m, "/no-such/page"+q)
			got := serveDump(refuse, m, "/no-such/page"+q)
			if got != want {
				t.Errorf("%s %s: Refuse differs from the public answer\n--- public ---\n%.400s\n--- Refuse ---\n%.400s", m, q, want, got)
			}
		}
	}
}

func TestUnroutableKeepsPathShape(t *testing.T) {
	for in, want := range map[string]string{
		"/wp-login.php":         "/hm-nf",
		"/a/b/":                 "/hm-nf/hm-nf/",
		"/a/../b":               "/hm-nf/../hm-nf",
		"//x":                   "//hm-nf",
		"/phpmyadmin/index.php": "/hm-nf/hm-nf",
	} {
		r := httptest.NewRequest("POST", "/x", nil)
		r.URL.Path = in
		if got := unroutable(r).URL.Path; got != want {
			t.Errorf("unroutable(%q) = %q, want %q", in, got, want)
		}
		if r.URL.Path != in {
			t.Errorf("unroutable changed the original request path to %q", r.URL.Path)
		}
	}
}
