package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Through the real middleware stack the admin's strict CSP wins over the
// global lax one, and unowned /admin paths do not fall to the public renderer.
func TestAdminHeadersOverrideGlobalSecurityHeaders(t *testing.T) {
	h, _ := bootRoutes(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/admin/entities", nil)
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Dest", "document")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther || !strings.HasPrefix(rec.Header().Get("Location"), "/admin/login") {
		t.Fatalf("unauthenticated /admin/entities: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "'nonce-") || strings.Contains(csp, "unsafe-inline") {
		t.Fatalf("csp %q", csp)
	}
	if rec.Header().Get("Content-Security-Policy-Report-Only") != "" {
		t.Error("global report-only CSP leaked onto an admin response")
	}
	if got := rec.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("X-Frame-Options = %q", got)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/admin/_/tokens.css", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "--color-sumi") {
		t.Fatalf("tokens.css: %d", rec.Code)
	}
}

// Through the real router an encoded slash is refused before the signed-in
// fallback sees it, instead of redirecting to sign-in as an unowned asset path.
func TestAdminEncodedSlashIsRefusedThroughRouter(t *testing.T) {
	h, _ := bootRoutes(t)
	for _, path := range []string{"/admin/assets%2Fx", "/admin/assets%2F..%2Fentities", "/admin/entities/x/edit%2Fpreview/y"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set("Sec-Fetch-Mode", "cors")
		req.Header.Set("Sec-Fetch-Dest", "empty")
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound || rec.Header().Get("Location") != "" {
			t.Errorf("%s: %d %q, want the 404 refusal", path, rec.Code, rec.Header().Get("Location"))
		}
	}
}
