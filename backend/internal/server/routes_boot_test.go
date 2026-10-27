package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/hooks"
)

// bootRoutes runs the real setupRoutes and returns the full handler plus the
// server. Booting must not panic.
func bootRoutes(t *testing.T) (http.Handler, *Server) {
	t.Helper()
	s, err := routeShell()
	if err != nil {
		t.Fatal(err)
	}
	s.cfg.EnableCookieAuth = true
	s.validateSession = fakeSession
	s.validateAPICookie = fakeAPISession
	s.hooks = hooks.New(s.logger)
	s.setupRoutes()
	return s.buildHandler(), s
}

// hit serves a request and returns status and body; a core handler that dies
// on the test shell's nil collaborators reports "core:panic" (it was reached).
func hit(h http.Handler, method, path string, hdr ...string) (code int, body string) {
	req := httptest.NewRequest(method, path, nil)
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	func() {
		defer func() {
			if recover() != nil {
				code, body = 0, "core:panic"
			}
		}()
		h.ServeHTTP(rec, req)
		code, body = rec.Code, rec.Body.String()
	}()
	return code, body
}

func ownerToken(t *testing.T, s *Server) string {
	t.Helper()
	return "Bearer test-session-owner"
}

// fakeSession accepts "test-session-<role>" tokens, so route tests need no database.
func fakeSession(_ context.Context, token string) (*auth.Claims, error) {
	role, ok := strings.CutPrefix(token, "test-session-")
	if !ok {
		return nil, auth.ErrInvalidToken
	}
	return &auth.Claims{UserID: uuid.New(), Email: role + "@example.com", Role: role}, nil
}

// fakeAPISession accepts "test-api-session-<role>" tokens, the API cookie
// counterpart of fakeSession.
func fakeAPISession(_ context.Context, token string) (*auth.Claims, error) {
	role, ok := strings.CutPrefix(token, "test-api-session-")
	if !ok {
		return nil, auth.ErrInvalidToken
	}
	return &auth.Claims{UserID: uuid.New(), Email: role + "@example.com", Role: role}, nil
}

// Unknown paths: JSON 404 for any method, 404 (not 405) for non-GET even when
// the page catch-all is on.
func TestUnknownPathsAre404(t *testing.T) {
	h, _ := bootRoutes(t)
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/v1/nope"}, {"POST", "/api/v1/nope"}, {"PATCH", "/api/v1/users/x/y/z"},
		{"POST", "/nope"}, {"DELETE", "/nope/deeper"}, {"PUT", "/api/v1/public/nope"},
	} {
		code, body := hit(h, c.method, c.path)
		if code != 404 || !strings.Contains(body, "not found") {
			t.Errorf("%s %s = %d %q, want JSON 404", c.method, c.path, code, body)
		}
	}
}

// A non-GET request for an un-normalised path used to get a 307 whose body said
// "not found". No redirect is issued for a body-carrying method.
func TestNonCleanNonGetPathIs404(t *testing.T) {
	h, _ := bootRoutes(t)
	req := httptest.NewRequest("DELETE", "/nope", nil)
	req.URL.Path = "//nope"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 404 || rec.Header().Get("Location") != "" {
		t.Errorf("DELETE //nope = %d Location=%q, want 404 without Location", rec.Code, rec.Header().Get("Location"))
	}
}
