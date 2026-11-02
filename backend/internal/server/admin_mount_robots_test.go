package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/seo"
)

// The admin preview is the body /robots.txt serves for the same saved lines.
func TestRobotsPreviewEqualsRobotsTxt(t *testing.T) {
	s := &Server{seoStore: seo.NewStore(&seoQuerier{row: seoRow{raw: []byte(`{"robots":{"extra_lines":["User-agent: Foo","Disallow: /private"]}}`)}})}
	req := httptest.NewRequest(http.MethodGet, "http://example.test/robots.txt", nil)
	w := httptest.NewRecorder()
	s.handleRobots(w, req)

	got := s.robotsPreview(req, s.seoSettings(req.Context()).Robots)
	if got != w.Body.String() {
		t.Fatalf("preview differs from /robots.txt:\n%s\n---\n%s", got, w.Body.String())
	}
	if want := "Disallow: /private\n"; !strings.Contains(got, want) {
		t.Fatalf("preview lacks the saved line:\n%s", got)
	}
	// Typed values the owner has not saved yet show up too.
	if p := s.robotsPreview(req, seo.Robots{ExtraLines: []string{"Disallow: /draft"}}); !strings.Contains(p, "Disallow: /draft\n") || strings.Contains(p, "/private") {
		t.Fatalf("preview ignores the submitted lines:\n%s", p)
	}
}
