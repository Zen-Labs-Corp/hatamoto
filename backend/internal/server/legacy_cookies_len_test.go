package server

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/session"
)

func TestLegacyTokenFixturesHaveSessionLength(t *testing.T) {
	if len(liveLegacyToken) != legacyTokenLen || len(bogusLegacyToken) != legacyTokenLen {
		t.Fatalf("fixture lengths %d, %d; want %d", len(liveLegacyToken), len(bogusLegacyToken), legacyTokenLen)
	}
}

// A cookie that cannot be a session token costs no lookup and gets no response.
func TestExpireLegacyCookies_WrongShapeNoLookup(t *testing.T) {
	useNeutralNames(t)
	for name, v := range map[string]string{
		"1":        "a",
		"42":       strings.Repeat("a", 42),
		"44":       strings.Repeat("a", 44),
		"4096":     strings.Repeat("a", 4096),
		"bad byte": strings.Repeat("a", 42) + "+",
		"padding":  strings.Repeat("a", 42) + "=",
	} {
		t.Run(name, func(t *testing.T) {
			f := &fakeLegacySessions{live: liveLegacyToken, kind: session.KindAdmin}
			rec := runLegacyCookies(t, f, map[string]string{"hatamoto_session": v, "hatamoto_csrf": "v"})
			if f.validated != 0 {
				t.Errorf("validated %d times, want 0", f.validated)
			}
			if h := rec.Header().Values("Set-Cookie"); len(h) != 0 {
				t.Errorf("Set-Cookie %q, want none", h)
			}
		})
	}
}

// A well-formed but unknown token is looked up once and still gets nothing back.
func TestExpireLegacyCookies_UnknownTokenLookedUpOnce(t *testing.T) {
	useNeutralNames(t)
	f := &fakeLegacySessions{live: liveLegacyToken, kind: session.KindAdmin}
	rec := runLegacyCookies(t, f, map[string]string{"hatamoto_session": bogusLegacyToken})
	if f.validated != 1 || len(rec.Header().Values("Set-Cookie")) != 0 {
		t.Fatalf("validated %d, Set-Cookie %q", f.validated, rec.Header().Values("Set-Cookie"))
	}
}

type failingRevoke struct{ fakeLegacySessions }

func (f *failingRevoke) Revoke(_ context.Context, _ uuid.UUID) error { return errors.New("db down") }

// A revoke failure leaves the cookie alone and is logged with the session id,
// never the token.
func TestExpireLegacyCookies_RevokeFailureLogged(t *testing.T) {
	useNeutralNames(t)
	f := &failingRevoke{fakeLegacySessions{live: liveLegacyToken, kind: session.KindAdmin}}
	var buf bytes.Buffer
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: "hatamoto_session", Value: liveLegacyToken})
	rec := httptest.NewRecorder()
	expireLegacyCookies(f, slog.New(slog.NewTextHandler(&buf, nil)))(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).ServeHTTP(rec, req)
	if h := rec.Header().Values("Set-Cookie"); len(h) != 0 {
		t.Errorf("Set-Cookie %q, want none", h)
	}
	out := buf.String()
	if !strings.Contains(out, "level=WARN") || !strings.Contains(out, "session_id=") {
		t.Errorf("no Warn with the session id:\n%s", out)
	}
	if strings.Contains(out, liveLegacyToken) {
		t.Errorf("token logged:\n%s", out)
	}
}
