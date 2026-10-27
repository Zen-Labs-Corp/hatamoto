package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Public responses must never send Cross-Origin-Opener-Policy: same-origin.
// That header belongs to admin documents only (it severs window handles);
// on a public page it would break embeds and OAuth popups.
func TestPublicResponsesSendNoCOOP(t *testing.T) {
	h, _ := bootRoutes(t)
	for _, p := range []string{"/", "/health", "/no-such-page-xyz"} {
		rec := httptest.NewRecorder()
		func() {
			// The route shell has nil collaborators: a handler that dies
			// after the middleware stack still proves the stack's headers.
			defer func() { _ = recover() }()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		}()
		if v := rec.Header().Get("Cross-Origin-Opener-Policy"); v != "" {
			t.Errorf("%s: public response sends COOP %q", p, v)
		}
	}
}

// Positive control: the harness can see COOP when it is there (an admin response).
func TestAdminResponsesSendCOOP(t *testing.T) {
	h, _ := bootRoutes(t)
	req := httptest.NewRequest(http.MethodGet, "/admin/entities", nil)
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Dest", "document")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if v := rec.Header().Get("Cross-Origin-Opener-Policy"); v != "same-origin" {
		t.Errorf("admin response COOP %q, want same-origin", v)
	}
}

// The guard wraps the admin only: the JSON API and MCP never get its 404 or its headers.
func TestAPIAndMCPNotGuarded(t *testing.T) {
	h, _ := bootRoutes(t)
	for _, p := range []string{"/api/v1/public/site", "/mcp"} {
		rec := httptest.NewRecorder()
		func() {
			defer func() { _ = recover() }()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		}()
		if v := rec.Header().Get("Cross-Origin-Opener-Policy"); v != "" {
			t.Errorf("%s: COOP %q", p, v)
		}
		if strings.Contains(rec.Body.String(), "does not exist in the admin") {
			t.Errorf("%s: answered by the admin refusal", p)
		}
	}
}
