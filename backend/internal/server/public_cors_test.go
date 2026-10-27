package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/spam"
)

// corsMux mounts the three public routes the way the real mounters do.
func corsMux(srv *Server) http.Handler {
	mux := http.NewServeMux()
	g := httpx.NewGroup(mux, "/api/v1/public")
	handleCORS(g, http.MethodGet, "/forms/{slug}/token", srv.handleFormToken)
	handleCORS(g, http.MethodPost, "/forms/{slug}/submit", srv.handlePublicFormSubmit)
	handleCORS(httpx.NewGroup(mux, "/api/v1"), http.MethodPost, "/track", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	handleCORS(httpx.NewGroup(mux, ""), http.MethodPost, "/forms/{slug}/submit", srv.handlePublicFormPost)
	return mux
}

func corsDo(h http.Handler, method, path, origin, ctype, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func assertNoCredentials(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if v := rec.Header().Get("Access-Control-Allow-Credentials"); v != "" {
		t.Errorf("Allow-Credentials = %q", v)
	}
}

func TestPublicCORSPreflight(t *testing.T) {
	t.Parallel()
	srv, _ := newSpamServer(t, spam.Defaults(), nil)
	h := corsMux(srv)
	for _, path := range []string{"/api/v1/public/forms/contact/token", "/api/v1/public/forms/contact/submit", "/api/v1/track", "/forms/contact/submit"} {
		rec := corsDo(h, http.MethodOptions, path, "null", "", "")
		if rec.Code != http.StatusNoContent || rec.Header().Get("Access-Control-Allow-Origin") != "null" {
			t.Errorf("%s null: %d ACAO=%q", path, rec.Code, rec.Header().Get("Access-Control-Allow-Origin"))
		}
		if !strings.Contains(rec.Header().Get("Access-Control-Allow-Headers"), "Content-Type") {
			t.Errorf("%s: Allow-Headers = %q", path, rec.Header().Get("Access-Control-Allow-Headers"))
		}
		assertNoCredentials(t, rec)

		rec = corsDo(h, http.MethodOptions, path, "https://evil.example", "", "")
		if rec.Code != http.StatusNoContent || rec.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Errorf("%s other origin: %d ACAO=%q", path, rec.Code, rec.Header().Get("Access-Control-Allow-Origin"))
		}
	}
}

func TestPublicCORSToken(t *testing.T) {
	t.Parallel()
	srv, _ := newSpamServer(t, spam.Defaults(), nil)
	h := corsMux(srv)
	rec := corsDo(h, http.MethodGet, "/api/v1/public/forms/contact/token", "null", "", "")
	if rec.Code != 200 || rec.Header().Get("Access-Control-Allow-Origin") != "null" || rec.Header().Get("Vary") != "Origin" {
		t.Fatalf("null: %d %v", rec.Code, rec.Header())
	}
	assertNoCredentials(t, rec)
	for _, origin := range []string{"", "https://evil.example"} {
		rec = corsDo(h, http.MethodGet, "/api/v1/public/forms/contact/token", origin, "", "")
		if rec.Code != 200 || rec.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Errorf("origin %q: %d ACAO=%q", origin, rec.Code, rec.Header().Get("Access-Control-Allow-Origin"))
		}
	}
}

func TestPublicCORSTrack(t *testing.T) {
	t.Parallel()
	srv, _ := newSpamServer(t, spam.Defaults(), nil)
	rec := corsDo(corsMux(srv), http.MethodPost, "/api/v1/track", "null", "application/json", "{}")
	if rec.Code != http.StatusNoContent || rec.Header().Get("Access-Control-Allow-Origin") != "null" {
		t.Errorf("track: %d %v", rec.Code, rec.Header())
	}
	assertNoCredentials(t, rec)
}

func TestPublicCORSSubmitJSON(t *testing.T) {
	t.Parallel()
	srv, repo := newSpamServer(t, spam.Defaults(), nil)
	h := corsMux(srv)
	const path = "/api/v1/public/forms/contact/submit"

	// Opaque origin without a valid token is refused, with the CORS answer so
	// the page can read the refusal.
	for _, body := range []string{`{"name":"Ada"}`, `{"name":"Ada","hm_ts":"v1.1.forged"}`} {
		rec := corsDo(h, http.MethodPost, path, "null", "application/json", body)
		if rec.Code != http.StatusForbidden || rec.Header().Get("Access-Control-Allow-Origin") != "null" {
			t.Errorf("no token %s: %d %v", body, rec.Code, rec.Header())
		}
	}
	if len(repo.subs) != 0 {
		t.Fatalf("stored %d submissions without a token", len(repo.subs))
	}

	tok := tokenAgo(srv, time.Minute)
	rec := corsDo(h, http.MethodPost, path, "null", "application/json", `{"name":"Ada","hm_ts":"`+tok+`"}`)
	if rec.Code != http.StatusCreated || rec.Header().Get("Access-Control-Allow-Origin") != "null" {
		t.Fatalf("with token: %d %s", rec.Code, rec.Body)
	}
	assertNoCredentials(t, rec)
	if len(repo.subs) != 1 {
		t.Errorf("stored %d submissions, want 1", len(repo.subs))
	}

	// Same-origin and other-origin callers need no token and get no CORS.
	for _, origin := range []string{"", "https://evil.example"} {
		rec := corsDo(h, http.MethodPost, path, origin, "application/json", `{"name":"Ada"}`)
		if rec.Code != http.StatusCreated || rec.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Errorf("origin %q: %d ACAO=%q", origin, rec.Code, rec.Header().Get("Access-Control-Allow-Origin"))
		}
	}
}

func TestPublicCORSSubmitNative(t *testing.T) {
	t.Parallel()
	srv, repo := newSpamServer(t, spam.Defaults(), nil)
	h := corsMux(srv)
	const form = "application/x-www-form-urlencoded"

	rec := corsDo(h, http.MethodPost, "/forms/contact/submit", "null", form, url.Values{"name": {"Ada"}}.Encode())
	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "form_error=1") || len(repo.subs) != 0 {
		t.Fatalf("no token: %d %q, stored %d", rec.Code, rec.Header().Get("Location"), len(repo.subs))
	}

	rec = corsDo(h, http.MethodPost, "/forms/contact/submit", "null", form,
		url.Values{"name": {"Ada"}, "hm_ts": {tokenAgo(srv, time.Minute)}}.Encode())
	if rec.Code != http.StatusSeeOther || len(repo.subs) != 1 {
		t.Fatalf("with token: %d, stored %d", rec.Code, len(repo.subs))
	}

	// The urlencoded variant on the JSON endpoint is covered by the same check.
	rec = corsDo(h, http.MethodPost, "/api/v1/public/forms/contact/submit", "null", form, url.Values{"name": {"Ada"}}.Encode())
	if rec.Code != http.StatusForbidden {
		t.Errorf("urlencoded JSON endpoint no token: %d", rec.Code)
	}
}
