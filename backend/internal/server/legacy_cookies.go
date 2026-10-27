package server

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/middleware"
	"hatamoto.dev/core/internal/session"
)

// Cookie names used before the neutral rename.
const (
	legacySessionCookie = "hatamoto_session"
	legacyCSRFCookie    = "hatamoto_csrf"
)

// legacyTokenLen is the length of a session token: 32 random bytes in unpadded
// base64url (session.newOpaqueToken). A cookie of any other length cannot be a
// session, so it costs no lookup.
const legacyTokenLen = 43

// validLegacyToken reports whether v has the shape of a session token.
func validLegacyToken(v string) bool {
	if len(v) != legacyTokenLen {
		return false
	}
	for i := 0; i < len(v); i++ {
		c := v[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

// legacySessions is the part of the session store the legacy-cookie cleanup uses.
type legacySessions interface {
	Validate(ctx context.Context, token string) (*session.Session, error)
	Revoke(ctx context.Context, sessionID uuid.UUID) error
}

// expireLegacyCookies retires a browser's pre-rename session. Only when the
// legacy session cookie holds a live admin session is that session revoked
// server-side and the legacy cookies expired; any other value, or a lone legacy
// CSRF cookie, gets no Set-Cookie, so the response cannot be used to probe for
// the product name. The legacy cookie never authenticates the request. A no-op
// when the active names are the legacy ones or there is no store.
func expireLegacyCookies(sessions legacySessions, log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if sessions != nil && auth.SessionCookieName != legacySessionCookie {
				retireLegacySession(w, r, sessions, log)
			}
			next.ServeHTTP(w, r)
		})
	}
}

func retireLegacySession(w http.ResponseWriter, r *http.Request, sessions legacySessions, log *slog.Logger) {
	c, err := r.Cookie(legacySessionCookie)
	if err != nil || !validLegacyToken(c.Value) {
		return
	}
	sess, err := sessions.Validate(r.Context(), c.Value)
	if err != nil || sess.Kind != session.KindAdmin || sess.UserID == nil {
		return
	}
	if err := sessions.Revoke(r.Context(), sess.ID); err != nil {
		// Still live: it is tried again on the next request. The token is never logged.
		log.Warn("legacy session revoke failed", slog.String("session_id", sess.ID.String()), slog.String("error", err.Error()))
		return
	}
	expireIfPresent(w, r, legacySessionCookie, true)
	if middleware.CSRFCookieName != legacyCSRFCookie {
		expireIfPresent(w, r, legacyCSRFCookie, false)
	}
}

// expireIfPresent expires name when the request carries it. Secure follows the
// active session name: the __Host- prefix is only used in secure mode.
func expireIfPresent(w http.ResponseWriter, r *http.Request, name string, httpOnly bool) {
	if _, err := r.Cookie(name); err != nil {
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Path:     "/",
		HttpOnly: httpOnly,
		Secure:   strings.HasPrefix(auth.SessionCookieName, "__Host-"),
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
}
