package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/auth"
)

// healthServer has a session validator that accepts only "good-session" and an
// API key check that accepts only "good-key".
func healthServer(pingErr error) *Server {
	return &Server{
		pingDB: func(context.Context) error { return pingErr },
		validateSession: func(_ context.Context, tok string) (*auth.Claims, error) {
			if tok == "good-session" {
				return &auth.Claims{}, nil
			}
			return nil, auth.ErrInvalidToken
		},
		keyValid: func(_ context.Context, raw string) bool { return raw == "good-key" },
	}
}

func probe(h http.HandlerFunc, mutate func(*http.Request)) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	if mutate != nil {
		mutate(req)
	}
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func TestHealthPublicIsPlainOK(t *testing.T) {
	t.Parallel()
	s := healthServer(nil)
	for name, h := range map[string]http.HandlerFunc{"health": s.handleHealth, "ready": s.handleReady} {
		rec := probe(h, nil)
		if rec.Code != 200 || rec.Body.String() != "ok" || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/plain") {
			t.Errorf("%s: %d %q %q", name, rec.Code, rec.Header().Get("Content-Type"), rec.Body)
		}
	}
}

func TestReadyDatabaseDown(t *testing.T) {
	t.Parallel()
	s := healthServer(errors.New("down"))
	rec := probe(s.handleReady, nil)
	if rec.Code != http.StatusServiceUnavailable || rec.Body.String() != "not ready" || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/plain") {
		t.Errorf("anonymous: %d %q %q", rec.Code, rec.Header().Get("Content-Type"), rec.Body)
	}
	rec = probe(s.handleReady, func(r *http.Request) { r.Header.Set("X-API-Key", "good-key") })
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"database":"unavailable"`) {
		t.Errorf("authed: %d %q", rec.Code, rec.Body)
	}
}

func TestHealthDetailForCredentials(t *testing.T) {
	t.Parallel()
	s := healthServer(nil)
	creds := map[string]func(*http.Request){
		"session cookie": func(r *http.Request) { r.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "good-session"}) },
		"bearer":         func(r *http.Request) { r.Header.Set("Authorization", "Bearer good-session") },
		"api key":        func(r *http.Request) { r.Header.Set("X-API-Key", "good-key") },
	}
	for name, mutate := range creds {
		if rec := probe(s.handleHealth, mutate); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"status":"healthy"`) ||
			!strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
			t.Errorf("health %s: %d %q", name, rec.Code, rec.Body)
		}
		if rec := probe(s.handleReady, mutate); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"status":"ready"`) {
			t.Errorf("ready %s: %d %q", name, rec.Code, rec.Body)
		}
	}
}

func TestHealthBadCredentialsStayPlain(t *testing.T) {
	t.Parallel()
	s := healthServer(nil)
	bad := []func(*http.Request){
		func(r *http.Request) { r.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "nope"}) },
		func(r *http.Request) { r.Header.Set("Authorization", "Bearer nope") },
		func(r *http.Request) { r.Header.Set("Authorization", "Garbage") },
		func(r *http.Request) { r.Header.Set("X-API-Key", "wrong") },
	}
	for i, mutate := range bad {
		if rec := probe(s.handleHealth, mutate); rec.Code != 200 || rec.Body.String() != "ok" {
			t.Errorf("case %d: %d %q", i, rec.Code, rec.Body)
		}
	}
}
