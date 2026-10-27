package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/middleware"
)

// cookieHit serves a request carrying one cookie and extra headers; a handler
// that dies on the test shell's nil collaborators reports code 0 (it was reached).
func cookieHit(h http.Handler, method, path, cookieName, cookieValue string, hdr ...string) (code int) {
	req := httptest.NewRequest(method, path, nil)
	if cookieName != "" {
		req.AddCookie(&http.Cookie{Name: cookieName, Value: cookieValue})
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	func() {
		defer func() {
			if recover() != nil {
				code = 0
			}
		}()
		h.ServeHTTP(rec, req)
		code = rec.Code
	}()
	return code
}

// The API cookie, and only it, authenticates /api/v1 by cookie: an admin form
// session cookie is not an API credential, and a cookie-authenticated mutation
// needs the token bound to that cookie plus a same-origin request.
func TestAPICookieWiring(t *testing.T) {
	h, s := bootRoutes(t)
	const apiTok = "test-api-session-owner"

	t.Run("admin cookie is refused", func(t *testing.T) {
		if got := cookieHit(h, "GET", "/api/v1/me", auth.SessionCookieName, "test-session-owner"); got != 401 {
			t.Errorf("GET /me with the admin cookie = %d, want 401", got)
		}
	})
	t.Run("api cookie reaches the handler", func(t *testing.T) {
		if got := cookieHit(h, "GET", "/api/v1/me", auth.APISessionCookieName, apiTok); got == 401 || got == 403 {
			t.Errorf("GET /me with the API cookie = %d, want it past auth", got)
		}
	})
	t.Run("invalid api cookie is refused", func(t *testing.T) {
		if got := cookieHit(h, "GET", "/api/v1/me", auth.APISessionCookieName, "test-session-owner"); got != 401 {
			t.Errorf("GET /me with a non-API token in the API cookie = %d, want 401", got)
		}
	})
	t.Run("bearer is unchanged", func(t *testing.T) {
		if got := cookieHit(h, "GET", "/api/v1/me", "", "", "Authorization", "Bearer test-session-owner"); got == 401 || got == 403 {
			t.Errorf("GET /me with a bearer token = %d, want it past auth", got)
		}
	})
	t.Run("mutation without a csrf token is refused", func(t *testing.T) {
		if got := cookieHit(h, "POST", "/api/v1/auth/logout", auth.APISessionCookieName, apiTok, "Sec-Fetch-Site", "same-origin"); got != 403 {
			t.Errorf("POST with the API cookie and no token = %d, want 403", got)
		}
	})
	t.Run("mutation with the bound token passes csrf", func(t *testing.T) {
		tok := middleware.APICSRFToken(s.csrfKey(), apiTok)
		got := cookieHit(h, "POST", "/api/v1/auth/logout", auth.APISessionCookieName, apiTok,
			middleware.CSRFHeaderName, tok, "Sec-Fetch-Site", "same-origin")
		if got == 403 || got == 401 {
			t.Errorf("POST with the bound token = %d, want it past CSRF", got)
		}
	})
	t.Run("mutation without same-origin is refused", func(t *testing.T) {
		tok := middleware.APICSRFToken(s.csrfKey(), apiTok)
		got := cookieHit(h, "POST", "/api/v1/auth/logout", auth.APISessionCookieName, apiTok, middleware.CSRFHeaderName, tok)
		if got != 403 {
			t.Errorf("POST without Sec-Fetch-Site = %d, want 403", got)
		}
	})
}
