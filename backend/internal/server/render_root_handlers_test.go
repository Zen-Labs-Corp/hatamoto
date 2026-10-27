package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/agents"
)

func TestRenderRootRobots(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodGet, "http://example.test/robots.txt", nil)
	req.RemoteAddr = "127.0.0.1:1234" // a trusted proxy, so its forwarded scheme counts
	req.Header.Set("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()
	s.handleRobots(w, req)
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/plain") {
		t.Fatalf("status %d ct %q", w.Code, w.Header().Get("Content-Type"))
	}
	body := w.Body.String()
	if !strings.Contains(body, "Disallow: /api\n") || !strings.Contains(body, "Sitemap: https://example.test/sitemap.xml\n") {
		t.Fatalf("body:\n%s", body)
	}
	if w.Header().Get("Cache-Control") != "public, max-age=300, s-maxage=300" {
		t.Fatalf("cache-control %q", w.Header().Get("Cache-Control"))
	}
}

type fakeRobotsStore struct {
	denied []agents.Purpose
	err    error
}

func (f fakeRobotsStore) DeniedPurposes(context.Context) ([]agents.Purpose, error) {
	return f.denied, f.err
}
func (f fakeRobotsStore) SetDenied(context.Context, []agents.Purpose) error { return nil }

func robotsBody(s *Server) string {
	req := httptest.NewRequest(http.MethodGet, "http://example.test/robots.txt", nil)
	req.RemoteAddr = "127.0.0.1:1234" // a trusted proxy, so its forwarded scheme counts
	req.Header.Set("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()
	s.handleRobots(w, req)
	return w.Body.String()
}

func TestRenderRootRobotsPolicy(t *testing.T) {
	base := robotsBody(&Server{})
	if got := robotsBody(&Server{robotsPolicy: fakeRobotsStore{}}); got != base {
		t.Fatalf("empty policy changed output:\n%s", got)
	}
	if got := robotsBody(&Server{robotsPolicy: fakeRobotsStore{err: errors.New("db down")}}); got != base {
		t.Fatalf("store error must fall back to default:\n%s", got)
	}
	got := robotsBody(&Server{robotsPolicy: fakeRobotsStore{denied: []agents.Purpose{agents.PurposeTraining}}})
	for _, want := range []string{"User-agent: GPTBot\nDisallow: /\n\n", "User-agent: CCBot\nDisallow: /\n\n"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Index(got, "User-agent: CCBot") > strings.Index(got, "Sitemap:") {
		t.Fatalf("deny groups must precede Sitemap:\n%s", got)
	}
}

func TestRenderRootSitemapXSL(t *testing.T) {
	w := httptest.NewRecorder()
	(&Server{}).handleSitemapXSL(w, httptest.NewRequest(http.MethodGet, "/sitemap.xsl", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "xsl:stylesheet") {
		t.Fatalf("status %d", w.Code)
	}
}

func TestDetermineBaseURLHonoursForwardedHeadersOnlyFromTrustedProxies(t *testing.T) {
	for _, tc := range []struct{ remote, want string }{
		{"203.0.113.9:4000", "http://site.test"},
		{"127.0.0.1:4000", "https://front.example"},
		{"10.1.2.3:4000", "http://site.test"}, // private, but not trusted unless listed in HATAMOTO_TRUSTED_PROXIES
	} {
		req := httptest.NewRequest(http.MethodGet, "http://site.test/", nil)
		req.RemoteAddr = tc.remote
		req.Header.Set("X-Forwarded-Host", "front.example")
		req.Header.Set("X-Forwarded-Proto", "https")
		if got := determineBaseURL(req); got != tc.want {
			t.Errorf("%s: got %s want %s", tc.remote, got, tc.want)
		}
	}
}
