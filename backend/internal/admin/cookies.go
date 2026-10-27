package admin

import (
	"net/http"
	"strings"
	"time"

	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/middleware"
)

// isHTTPS reports whether the browser talked to us over TLS: directly, or via a
// trusted proxy (HATAMOTO_TRUSTED_PROXIES; default loopback only) that says so (Caddy sets X-Forwarded-Proto). The Secure
// cookie attribute follows this rather than ENVIRONMENT, so a first-run wizard
// on plain HTTP still logs in.
func isHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	if middleware.IsTrustedProxy(r.RemoteAddr) {
		return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
	}
	return false
}

// secure is the Secure attribute for every cookie the admin sets.
// ADMIN_COOKIE_SECURE is a minimum: an operator who asked for Secure cookies
// gets them even when the proxy's headers are not trusted or not sent.
func (a *Admin) secure(r *http.Request) bool { return a.deps.CookieSecure || isHTTPS(r) }

// preCookie is the pre-session cookie: it binds the token of the sign-in, reset
// and setup forms before any session exists.
const preCookie = "hm_pre"

// preTTL is how long a pre-session cookie lives: long enough for a sign-in tab
// left open over a working day. parsePreID also refuses an expiry further out.
const preTTL = 12 * time.Hour

// setPre mints the pre-session cookie and returns its value. The server also
// checks the expiry embedded in the value, so the lifetime does not rest on the browser.
func (a *Admin) setPre(w http.ResponseWriter, r *http.Request) string {
	id := a.newPreID(time.Now().Add(preTTL))
	http.SetCookie(w, a.preCookie(r, id, int(preTTL/time.Second)))
	return id
}

// clearPre expires the pre-session cookie (sign-in replaces it with the session token).
func (a *Admin) clearPre(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, a.preCookie(r, "", -1))
}

func (a *Admin) preCookie(r *http.Request, value string, maxAge int) *http.Cookie {
	return &http.Cookie{Name: preCookie, Value: value, Path: a.AdminPath(), MaxAge: maxAge,
		HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: a.secure(r)}
}

// SetSession writes the session cookie after a successful sign-in and retires the pre-session one.
func (a *Admin) setSession(w http.ResponseWriter, r *http.Request, token string) {
	// A session the browser already presented dies with this sign-in, so
	// credentials do not pile up server-side on a shared machine.
	if c, err := r.Cookie(auth.SessionCookieName); err == nil && c.Value != "" && a.deps.Sessions != nil {
		if old, err := a.deps.Sessions.Validate(r.Context(), c.Value); err == nil {
			_ = a.deps.Sessions.Revoke(r.Context(), old.ID)
		}
	}
	auth.SetSessionCookie(w, token, a.secure(r))
	a.clearPre(w, r)
}

// clearSession expires the session and pre-session cookies, and the readable
// hatamoto_csrf cookie that browsers upgraded from an older release still hold.
func (a *Admin) clearSession(w http.ResponseWriter, r *http.Request) {
	secure := a.secure(r)
	auth.ClearSessionCookie(w, secure)
	a.clearPre(w, r)
	middleware.ClearCSRFCookie(w, secure) // legacy cookie, dead for the admin
}

const themeCookie = "hm_theme"

// themeOf is the theme the page renders with: the chosen one, or "auto" (the
// browser's prefers-color-scheme, applied by CSS) until the user chooses.
func themeOf(r *http.Request) string {
	if c, err := r.Cookie(themeCookie); err == nil {
		switch c.Value {
		case "dark", "light":
			return c.Value
		}
	}
	return "auto"
}

// handleTheme toggles light/dark. It is a POST (CSRF-checked like the rest) so
// the server can render data-admin-theme on <html> with no flash and no inline
// script.
func (a *Admin) handleTheme(w http.ResponseWriter, r *http.Request) {
	// Flip what the user currently sees. With no choice yet that is the
	// browser's scheme, which only the page's script can tell us ("scheme");
	// without it light is assumed.
	shown := themeOf(r)
	if shown == "auto" {
		shown = "light"
		if r.PostFormValue("scheme") == "dark" {
			shown = "dark"
		}
	}
	next := "dark"
	if shown == "dark" {
		next = "light"
	}
	http.SetCookie(w, &http.Cookie{
		Name: themeCookie, Value: next, Path: "/", MaxAge: int(365 * 24 * time.Hour / time.Second),
		Secure: a.secure(r), SameSite: http.SameSiteLaxMode,
	})
	// The page the toggle was used on: the posted "back" (the referrer is
	// never sent), else the Referer, else the admin home.
	back := SafeNextFor(a.adminPath, r.PostFormValue("back"))
	if ref := r.Referer(); back == "" && ref != "" {
		if i := strings.Index(ref, a.adminPath); i >= 0 {
			back = SafeNextFor(a.adminPath, ref[i:])
		}
	}
	if back == "" {
		back = a.adminPath
	}
	Redirect(w, r, back)
}
