package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/middleware"
	"hatamoto.dev/core/internal/session"
)

// Session tokens are 43 characters of base64url; the middleware looks up no other length.
const (
	liveLegacyToken  = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	bogusLegacyToken = "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"
)

// fakeLegacySessions knows one live token and records what the middleware did.
type fakeLegacySessions struct {
	live      string
	kind      session.Kind
	validated int
	revoked   []uuid.UUID
}

func (f *fakeLegacySessions) Validate(_ context.Context, token string) (*session.Session, error) {
	f.validated++
	if token != f.live {
		return nil, session.ErrInvalid
	}
	uid := uuid.New()
	return &session.Session{ID: uuid.New(), Kind: f.kind, UserID: &uid}, nil
}

func (f *fakeLegacySessions) Revoke(_ context.Context, id uuid.UUID) error {
	f.revoked = append(f.revoked, id)
	return nil
}

// runLegacyCookies sends one request carrying name=value cookies through the
// middleware; the inner handler records whether it ran.
func runLegacyCookies(t *testing.T, f *fakeLegacySessions, cookies map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", "/", nil)
	for n, v := range cookies {
		req.AddCookie(&http.Cookie{Name: n, Value: v})
	}
	rec := httptest.NewRecorder()
	expireLegacyCookies(f, slog.New(slog.NewTextHandler(io.Discard, nil)))(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).ServeHTTP(rec, req)
	return rec
}

func useNeutralNames(t *testing.T) {
	t.Helper()
	oldS, oldA, oldC := auth.SessionCookieName, auth.APISessionCookieName, middleware.CSRFCookieName
	t.Cleanup(func() {
		auth.SessionCookieName, auth.APISessionCookieName, middleware.CSRFCookieName = oldS, oldA, oldC
	})
	auth.UseNeutralCookieNames(true)
	middleware.UseNeutralCSRFCookieName(true)
}

func TestExpireLegacyCookies_LiveSessionRevokedAndExpired(t *testing.T) {
	useNeutralNames(t)
	f := &fakeLegacySessions{live: liveLegacyToken, kind: session.KindAdmin}
	rec := runLegacyCookies(t, f, map[string]string{"hatamoto_session": liveLegacyToken, "hatamoto_csrf": "v", "other": "v"})
	if len(f.revoked) != 1 {
		t.Fatalf("revoked %d sessions, want 1", len(f.revoked))
	}
	got := map[string]*http.Cookie{}
	for _, c := range rec.Result().Cookies() {
		got[c.Name] = c
	}
	if len(got) != 2 {
		t.Fatalf("cookies %v, want both legacy names expired", got)
	}
	s, c := got["hatamoto_session"], got["hatamoto_csrf"]
	if s == nil || c == nil || s.MaxAge != -1 || c.MaxAge != -1 || s.Path != "/" || c.Path != "/" {
		t.Fatalf("legacy cookies not expired on Path=/: %v", got)
	}
	if !s.HttpOnly || c.HttpOnly || !s.Secure || !c.Secure {
		t.Errorf("attrs: session %+v csrf %+v", s, c)
	}
}

func TestExpireLegacyCookies_LiveSessionWithoutCSRF(t *testing.T) {
	useNeutralNames(t)
	f := &fakeLegacySessions{live: liveLegacyToken, kind: session.KindAdmin}
	cs := runLegacyCookies(t, f, map[string]string{"hatamoto_session": liveLegacyToken}).Result().Cookies()
	if len(cs) != 1 || cs[0].Name != "hatamoto_session" {
		t.Fatalf("cookies %v, want only hatamoto_session", cs)
	}
}

// A guessed or stale legacy value gets no response at all: no Set-Cookie, no write.
func TestExpireLegacyCookies_BogusValueNothing(t *testing.T) {
	useNeutralNames(t)
	f := &fakeLegacySessions{live: liveLegacyToken, kind: session.KindAdmin}
	rec := runLegacyCookies(t, f, map[string]string{"hatamoto_session": bogusLegacyToken, "hatamoto_csrf": "v"})
	if h := rec.Header().Values("Set-Cookie"); len(h) != 0 {
		t.Fatalf("Set-Cookie %q, want none", h)
	}
	if len(f.revoked) != 0 {
		t.Fatalf("revoked %d sessions, want 0", len(f.revoked))
	}
}

func TestExpireLegacyCookies_NonAdminSessionNothing(t *testing.T) {
	useNeutralNames(t)
	f := &fakeLegacySessions{live: liveLegacyToken, kind: session.Kind("other")}
	rec := runLegacyCookies(t, f, map[string]string{"hatamoto_session": liveLegacyToken})
	if len(rec.Header().Values("Set-Cookie")) != 0 || len(f.revoked) != 0 {
		t.Fatalf("non-admin session touched: %v, revoked %d", rec.Header().Values("Set-Cookie"), len(f.revoked))
	}
}

func TestExpireLegacyCookies_LoneCSRFNothing(t *testing.T) {
	useNeutralNames(t)
	f := &fakeLegacySessions{live: liveLegacyToken, kind: session.KindAdmin}
	rec := runLegacyCookies(t, f, map[string]string{"hatamoto_csrf": "v"})
	if len(rec.Header().Values("Set-Cookie")) != 0 || f.validated != 0 {
		t.Fatalf("lone csrf: Set-Cookie %q, validated %d", rec.Header().Values("Set-Cookie"), f.validated)
	}
}

func TestExpireLegacyCookies_ActiveEqualsLegacy(t *testing.T) {
	oldS, oldA, oldC := auth.SessionCookieName, auth.APISessionCookieName, middleware.CSRFCookieName
	t.Cleanup(func() {
		auth.SessionCookieName, auth.APISessionCookieName, middleware.CSRFCookieName = oldS, oldA, oldC
	})
	auth.SessionCookieName, middleware.CSRFCookieName = "hatamoto_session", "hatamoto_csrf"
	f := &fakeLegacySessions{live: liveLegacyToken, kind: session.KindAdmin}
	rec := runLegacyCookies(t, f, map[string]string{"hatamoto_session": liveLegacyToken, "hatamoto_csrf": "v"})
	if len(rec.Header().Values("Set-Cookie")) != 0 || f.validated != 0 {
		t.Fatalf("legacy names active: Set-Cookie %q, validated %d", rec.Header().Values("Set-Cookie"), f.validated)
	}
}
