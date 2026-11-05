package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/redirect"
	"hatamoto.dev/core/internal/seo"
)

func adsServer(raw string) *Server {
	return &Server{seoStore: seo.NewStore(&seoQuerier{row: seoRow{raw: []byte(raw)}})}
}

func serveAds(s *Server, app bool, method string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	s.handleAdsTxt(app)(w, httptest.NewRequest(method, "http://example.test/ads.txt", nil))
	return w
}

func TestAdsTxtServesSavedText(t *testing.T) {
	s := adsServer(`{"ads_txt":"google.com, pub-1, DIRECT, f08c47fec0942fa0\n","app_ads_txt":"<b>x</b>, a, DIRECT\n"}`)
	w := serveAds(s, false, http.MethodGet)
	if w.Code != 200 || w.Body.String() != "google.com, pub-1, DIRECT, f08c47fec0942fa0\n" {
		t.Fatalf("ads: %d %q", w.Code, w.Body)
	}
	h := w.Header()
	if h.Get("Content-Type") != "text/plain; charset=utf-8" || h.Get("X-Content-Type-Options") != "nosniff" ||
		h.Get("Cache-Control") != "public, max-age=60, s-maxage=60" {
		t.Fatalf("headers: %v", h)
	}
	if w := serveAds(s, true, http.MethodGet); w.Code != 200 || w.Body.String() != "<b>x</b>, a, DIRECT\n" {
		t.Fatalf("app-ads: %d %q", w.Code, w.Body)
	}
	if w := serveAds(s, false, http.MethodHead); w.Code != 200 {
		t.Fatalf("head: %d %q", w.Code, w.Body)
	}
}

func TestAdsTxtEmptyIsPlain404(t *testing.T) {
	for _, s := range []*Server{adsServer(`{}`), adsServer(`{"ads_txt":" \n"}`), {}} {
		w := serveAds(s, false, http.MethodGet)
		if w.Code != 404 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/plain") || w.Body.String() != "404 page not found\n" {
			t.Fatalf("%d %q %q", w.Code, w.Header().Get("Content-Type"), w.Body)
		}
	}
	if w := serveAds(adsServer(`{"ads_txt":"a.com, 1, DIRECT\n"}`), true, http.MethodGet); w.Code != 404 {
		t.Fatalf("app-ads must be separate, got %d", w.Code)
	}
}

func TestAdsTxtRoutesAreMounted(t *testing.T) {
	f := newRenderFixture(t)
	r, h := baseRouter()
	s := adsServer(`{"ads_txt":"a.com, 1, DIRECT\n"}`)
	mountRenderRoutes(r, renderRoutes{
		pages: f.h, assets: stub("assets"), robots: stub("robots"), sitemap: stub("sitemap"),
		subSitemap: stub("sub"), llms: stub("llms"), feed: stub("feed"), xsl: stub("xsl"), formPost: stub("form"),
		adsTxt: s.handleAdsTxt(false), appAdsTxt: s.handleAdsTxt(true),
	})
	if w := getPath(h, "/ads.txt"); w.Code != 200 || w.Body.String() != "a.com, 1, DIRECT\n" {
		t.Fatalf("/ads.txt: %d %q", w.Code, w.Body)
	}
	if w := getPath(h, "/app-ads.txt"); w.Code != 404 || w.Body.String() != "404 page not found\n" {
		t.Fatalf("/app-ads.txt: %d %q", w.Code, w.Body)
	}
}

func TestAdsTxtEmptyHonoursRedirectRule(t *testing.T) {
	s := newRedirectTestServer(t,
		&redirect.Redirect{SourcePath: "/ads.txt", TargetURL: "https://hosted.example.com/ads.txt"},
	)
	s.seoStore = seo.NewStore(&seoQuerier{row: seoRow{raw: []byte(`{}`)}})
	w := serveAds(s, false, http.MethodGet)
	if w.Code != 301 || w.Header().Get("Location") != "https://hosted.example.com/ads.txt" {
		t.Fatalf("empty file with a redirect rule: %d %v", w.Code, w.Header())
	}
	app := httptest.NewRecorder()
	s.handleAdsTxt(true)(app, httptest.NewRequest(http.MethodGet, "http://example.test/app-ads.txt", nil))
	if app.Code != 404 {
		t.Fatalf("app-ads has no rule, got %d", app.Code)
	}
	s.seoStore = seo.NewStore(&seoQuerier{row: seoRow{raw: []byte(`{"ads_txt":"a.com, 1, DIRECT\n"}`)}})
	if w := serveAds(s, false, http.MethodGet); w.Code != 200 {
		t.Fatalf("a saved file must win over the rule, got %d", w.Code)
	}
}
