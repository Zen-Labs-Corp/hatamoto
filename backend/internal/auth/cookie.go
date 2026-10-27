package auth

import (
	"net/http"
	"time"
)

// SessionCookieName is the HttpOnly cookie that carries the opaque admin
// session token. The default is the historical name (tests and library users
// keep it); the server switches to the neutral name once at boot through
// UseNeutralCookieNames, before any request is served. Not safe to change
// afterwards.
var SessionCookieName = "hatamoto_session"

// APISessionCookieName is the HttpOnly cookie that carries the JSON API
// session token (kind api), used only when cookie auth is enabled. It is a
// separate credential from SessionCookieName and is renamed together with it
// by UseNeutralCookieNames.
var APISessionCookieName = "hatamoto_api_session"

// UseNeutralCookieNames sets a name that does not identify the software.
// Secure mode uses the __Host- prefix (browsers require Secure, Path=/ and no
// Domain, which SetSessionCookie sets). Browsers drop prefixed cookies that
// lack Secure, so plain-HTTP dev mode uses an unprefixed name.
func UseNeutralCookieNames(secure bool) {
	if secure {
		SessionCookieName = "__Host-s"
		APISessionCookieName = "__Host-a"
		return
	}
	SessionCookieName = "s"
	APISessionCookieName = "a"
}

// sessionCookieMaxAge mirrors adminSessionTTL (30 days).
const sessionCookieMaxAge = int(30 * 24 * time.Hour / time.Second)

// SetSessionCookie writes the admin session cookie. HttpOnly (no JS access),
// Secure (configurable for local HTTP dev), SameSite=Strict (admin + API are
// same-origin via the Next rewrite, so Strict is safe and strongest), Path=/.
func SetSessionCookie(w http.ResponseWriter, token string, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   sessionCookieMaxAge,
	})
}

// ClearSessionCookie expires the admin session cookie (logout / invalid
// session). Legacy-named cookies are expired by the server's legacy cookie
// middleware, so no response names the old product cookie unprompted.
func ClearSessionCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
}

// SetAPISessionCookie writes the JSON API session cookie. It carries a
// kind-api session, a different credential from the admin form cookie, so page
// script and the admin UI never share one. Attributes match SetSessionCookie.
func SetAPISessionCookie(w http.ResponseWriter, token string, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     APISessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   sessionCookieMaxAge,
	})
}

// ClearAPISessionCookie expires the JSON API session cookie.
func ClearAPISessionCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     APISessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
}
