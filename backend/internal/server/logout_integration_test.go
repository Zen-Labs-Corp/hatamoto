//go:build integration

package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/middleware"
)

// Logout revokes the server-side session (a 30-day admin session), for a bearer
// client and for the cookie client, including a stale bearer next to a valid
// cookie:
//
//	DATABASE_URL=... go test -tags integration ./internal/server -run LogoutIntegration
func TestLogoutIntegration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; needs a migrated Postgres")
	}
	t.Setenv("JWT_SECRET", strings.Repeat("l", 40))
	t.Setenv("ENABLE_COOKIE_AUTH", "true")
	t.Setenv("HATAMOTO_CANONICAL_SLUG", "logout-it")
	t.Setenv("HATAMOTO_SITE_NAME", "Logout IT")
	t.Setenv("SMTP_FROM", "it@example.test")
	t.Setenv("SMTP_FROM_NAME", "Logout IT")
	t.Setenv("PUBLIC_BASE_URL", "https://logout-it.example.test")
	t.Setenv("ENVIRONMENT", "development")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	db, err := database.New(ctx, cfg.Database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)

	email := "logout-" + uuid.NewString()[:6] + "@example.test"
	const password = "correct horse battery staple 9"
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	userID := uuid.New()
	if _, err := db.Pool().Exec(ctx, `INSERT INTO hatamoto_users (id,email,password_hash,role) VALUES ($1,$2,$3,'owner')`, userID, email, hash); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_sessions WHERE user_id=$1`, userID)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_users WHERE id=$1`, userID)
	})

	cfg.Admin.Resolved = "/admin"
	srv := New(cfg, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ts := httptest.NewServer(srv.router)
	t.Cleanup(ts.Close)

	do := func(method, path, body string, hdr map[string]string, cookies ...*http.Cookie) (*http.Response, []byte) {
		t.Helper()
		req, _ := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		for _, c := range cookies {
			req.AddCookie(c)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp, b
	}
	login := func() (token, csrf string, cookies []*http.Cookie) {
		t.Helper()
		resp, b := do("POST", "/api/v1/auth/login", `{"email":"`+email+`","password":"`+password+`"}`, nil)
		if resp.StatusCode != 200 {
			t.Fatalf("login: %d %s", resp.StatusCode, b)
		}
		var out struct {
			SessionToken string `json:"session_token"`
			CSRFToken    string `json:"csrf_token"`
		}
		if err := json.Unmarshal(b, &out); err != nil || out.SessionToken == "" || out.CSRFToken == "" {
			t.Fatalf("login body %s: %v", b, err)
		}
		return out.SessionToken, out.CSRFToken, resp.Cookies()
	}
	sameOrigin := func(csrf string) map[string]string {
		return map[string]string{middleware.CSRFHeaderName: csrf, "Sec-Fetch-Site": "same-origin"}
	}
	me := func(hdr map[string]string, cookies ...*http.Cookie) int {
		resp, _ := do("GET", "/api/v1/me", "", hdr, cookies...)
		return resp.StatusCode
	}
	find := func(cs []*http.Cookie, name string) *http.Cookie {
		for _, c := range cs {
			if c.Name == name {
				return c
			}
		}
		t.Fatalf("no %s cookie", name)
		return nil
	}
	bearerOf := func(tok string) map[string]string { return map[string]string{"Authorization": "Bearer " + tok} }

	t.Run("bearer", func(t *testing.T) {
		tok, _, _ := login()
		if got := me(bearerOf(tok)); got != 200 {
			t.Fatalf("/me before logout = %d", got)
		}
		if resp, b := do("POST", "/api/v1/auth/logout", "", bearerOf(tok)); resp.StatusCode != 200 {
			t.Fatalf("logout = %d %s", resp.StatusCode, b)
		}
		if got := me(bearerOf(tok)); got != 401 {
			t.Errorf("/me after logout = %d, want 401", got)
		}
	})

	t.Run("login sets only the API cookie", func(t *testing.T) {
		tok, csrf, cs := login()
		if len(cs) != 1 || cs[0].Name != auth.APISessionCookieName {
			t.Fatalf("login cookies = %v, want only %s", cs, auth.APISessionCookieName)
		}
		if want := middleware.APICSRFToken(srv.csrfKey(), cs[0].Value); csrf != want {
			t.Errorf("body csrf_token is not bound to the API cookie")
		}
		var kind string
		if err := db.Pool().QueryRow(ctx, `SELECT kind FROM hatamoto_sessions WHERE user_id=$1 ORDER BY created_at DESC LIMIT 1`, userID).Scan(&kind); err != nil {
			t.Fatal(err)
		}
		if kind != "api" {
			t.Errorf("session kind = %q, want api", kind)
		}
		if tok != cs[0].Value {
			t.Errorf("cookie value differs from the body session token")
		}
	})

	t.Run("cookie", func(t *testing.T) {
		_, csrf, cs := login()
		sess := find(cs, auth.APISessionCookieName)
		if got := me(nil, sess); got != 200 {
			t.Fatalf("/me by cookie = %d", got)
		}
		resp, b := do("POST", "/api/v1/auth/logout", "", sameOrigin(csrf), sess)
		if resp.StatusCode != 200 {
			t.Fatalf("logout = %d %s", resp.StatusCode, b)
		}
		if got := me(nil, sess); got != 401 {
			t.Errorf("/me by cookie after logout = %d, want 401", got)
		}
	})

	t.Run("stale bearer with a valid cookie", func(t *testing.T) {
		_, csrf, cs := login()
		sess := find(cs, auth.APISessionCookieName)
		hdr := bearerOf("nxs_stale")
		for k, v := range sameOrigin(csrf) {
			hdr[k] = v
		}
		if resp, b := do("POST", "/api/v1/auth/logout", "", hdr, sess); resp.StatusCode != 200 {
			t.Fatalf("logout = %d %s", resp.StatusCode, b)
		}
		if got := me(nil, sess); got != 401 {
			t.Errorf("cookie session survived a logout carrying a stale bearer: /me = %d", got)
		}
	})

	t.Run("logout leaves an admin session alone", func(t *testing.T) {
		adminTok, err := srv.authService.IssueSession(ctx, userID, "it", "192.0.2.1")
		if err != nil {
			t.Fatal(err)
		}
		admin := &http.Cookie{Name: auth.SessionCookieName, Value: adminTok}
		_, csrf, cs := login()
		api := find(cs, auth.APISessionCookieName)
		if resp, b := do("POST", "/api/v1/auth/logout", "", sameOrigin(csrf), api, admin); resp.StatusCode != 200 {
			t.Fatalf("logout = %d %s", resp.StatusCode, b)
		}
		if got := me(nil, api); got != 401 {
			t.Errorf("API session survived logout: /me = %d", got)
		}
		if _, err := srv.authService.ValidateSession(ctx, adminTok); err != nil {
			t.Errorf("admin session was revoked by an API logout: %v", err)
		}
	})

	t.Run("the API cookie does not sign in the admin", func(t *testing.T) {
		adminTok, err := srv.authService.IssueSession(ctx, userID, "it", "192.0.2.1")
		if err != nil {
			t.Fatal(err)
		}
		_, _, cs := login()
		page := func(c *http.Cookie) string {
			t.Helper()
			req, _ := http.NewRequest("GET", ts.URL+"/admin/", nil)
			req.Header.Set("Sec-Fetch-Site", "same-origin")
			req.Header.Set("Sec-Fetch-Mode", "navigate")
			req.Header.Set("Sec-Fetch-Dest", "document")
			req.AddCookie(c)
			resp, err := (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			return resp.Header.Get("Location")
		}
		// A signed-in admin is sent on (here to finish the profile), a stranger to the sign-in page.
		const loginPage = "/admin/login"
		if got := page(&http.Cookie{Name: auth.SessionCookieName, Value: adminTok}); strings.HasPrefix(got, loginPage) || got == "" {
			t.Fatalf("control: /admin/ with an admin cookie redirects to %q, want a signed-in destination", got)
		}
		// The API token under either cookie name must not open the admin.
		for _, name := range []string{auth.APISessionCookieName, auth.SessionCookieName} {
			if got := page(&http.Cookie{Name: name, Value: find(cs, auth.APISessionCookieName).Value}); !strings.HasPrefix(got, loginPage) {
				t.Errorf("/admin/ with the API token in cookie %s redirects to %q, want %s", name, got, loginPage)
			}
		}
	})
	t.Run("an API login bearer token gets the health detail", func(t *testing.T) {
		tok, _, _ := login()
		resp, b := do("GET", "/health", "", bearerOf(tok))
		if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") || !strings.Contains(string(b), `"status":"healthy"`) {
			t.Errorf("/health with an API bearer token: %d %q %s", resp.StatusCode, resp.Header.Get("Content-Type"), b)
		}
		if resp, b := do("GET", "/health", "", nil); string(b) != "ok" {
			t.Errorf("anonymous /health: %d %q, want the plain ok", resp.StatusCode, b)
		}
	})
	t.Run("production validators keep admin tokens off the JSON API cookie", func(t *testing.T) {
		adminTok, err := srv.authService.IssueSession(ctx, userID, "it", "192.0.2.1")
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{auth.APISessionCookieName, auth.SessionCookieName} {
			if got := me(nil, &http.Cookie{Name: name, Value: adminTok}); got != 401 {
				t.Errorf("/api/v1/me with an admin session in cookie %s = %d, want 401", name, got)
			}
		}
		apiTok, _, _ := login()
		if got := me(nil, &http.Cookie{Name: auth.APISessionCookieName, Value: apiTok}); got != 200 {
			t.Errorf("/api/v1/me with an API session in the API cookie = %d, want 200", got)
		}
	})
}
