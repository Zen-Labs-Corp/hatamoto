package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"hatamoto.dev/core/internal/redirect"
)

// Open-redirect regression: whatever a visitor puts in the path, a redirect
// Location is a single-slash relative path or an owner-configured absolute URL.
func TestRedirectsNeverLeaveTheSite(t *testing.T) {
	s := newRedirectTestServer(t,
		&redirect.Redirect{SourcePath: "/xx/**", TargetURL: "/$1"},
		&redirect.Redirect{SourcePath: "/old/*", TargetURL: "/$1"},
		&redirect.Redirect{SourcePath: "/exact", TargetURL: "//evil.com"},
		&redirect.Redirect{SourcePath: "/exact2", TargetURL: "/\\evil.com"},
		&redirect.Redirect{SourcePath: "/ok", TargetURL: "https://owner.example.com/x"},
	)
	h := &renderHandlers{checkRedirect: s.matchRedirect}

	paths := []string{
		"/xx/%5Cevil.com", "/xx/%5cevil.com", "/xx/%09/evil.com", "/xx/%2F%2Fevil.com", "/xx//evil.com",
		"/xx///evil.com", "/xx/%E2%81%84evil.com", "/xx/%EF%BC%8Fevil.com", "/xx/%EF%BC%BCevil.com",
		"/xx/%0d%0aSet-Cookie:%20a=b", "/xx/%0aevil", "/old/%5Cevil.com", "/old/%09", "/old/%2F%2Fevil.com",
		"/xx/%5C%5Cevil.com", "/xx/%2e%2e/%5Cevil.com", "/exact", "/exact2",
	}
	for _, p := range paths {
		u, err := url.Parse(p)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodGet, p, nil)
		rec := httptest.NewRecorder()
		if h.redirectIfMatched(rec, req, u.Path) {
			if loc := rec.Header().Get("Location"); !redirect.SafeLocation(loc, false) {
				t.Errorf("%s -> %d Location %q", p, rec.Code, loc)
			}
		}
	}

	// The reviewer's two payloads fall through to the 404 path.
	for _, p := range []string{"/xx/%5Cevil.com", "/xx/%09/evil.com", "/exact", "/exact2"} {
		u, _ := url.Parse(p)
		rec := httptest.NewRecorder()
		if h.redirectIfMatched(rec, httptest.NewRequest(http.MethodGet, p, nil), u.Path) {
			t.Errorf("%s redirected to %q", p, rec.Header().Get("Location"))
		}
	}

	// Legitimate rules keep working, query string included.
	rec := httptest.NewRecorder()
	if !h.redirectIfMatched(rec, httptest.NewRequest(http.MethodGet, "/xx/about?utm_source=x", nil), "/xx/about") ||
		rec.Header().Get("Location") != "/about?utm_source=x" {
		t.Errorf("legit prefix strip: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	rec = httptest.NewRecorder()
	if !h.redirectIfMatched(rec, httptest.NewRequest(http.MethodGet, "/ok", nil), "/ok") ||
		rec.Header().Get("Location") != "https://owner.example.com/x" {
		t.Errorf("owner absolute target: %d %q", rec.Code, rec.Header().Get("Location"))
	}
}

// The WordPress dated permalink copies a request-controlled slug into its
// target; a hostile slug is dropped by safeRedirect.
func TestLegacyWPRedirectsAreFiltered(t *testing.T) {
	p := redirect.NewWPPatternsProvider(nil)
	for _, path := range []string{"/2020/05/\\evil.com", "/2020/05/17/\tx", "/2020/05/\r\nx"} {
		m, _ := p.Resolve(t.Context(), path)
		if m == nil {
			t.Fatalf("%q: provider declined", path)
		}
		if safeRedirect(&redirect.Redirect{TargetURL: m.Target}) != nil {
			t.Errorf("%q -> %q passed the filter", path, m.Target)
		}
	}
	m, _ := p.Resolve(t.Context(), "/2020/05/hello")
	if safeRedirect(&redirect.Redirect{TargetURL: m.Target}) == nil {
		t.Errorf("benign permalink filtered: %q", m.Target)
	}
}
