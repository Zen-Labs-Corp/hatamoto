package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"hatamoto.dev/core/internal/middleware"
	"hatamoto.dev/core/internal/packages"
	"hatamoto.dev/core/internal/render"
)

// Theme preview: an owner or editor can browse the public site rendered with
// any installed theme. The admin mints a signed, expiring token for one theme
// (only a signed-in owner or editor can reach that route); the token turns into
// a cookie on the public site. Preview responses are never cached or shared.
//
// The token is a bearer link, not tied to a user or session: whoever holds it
// can see the theme rendered over the already-published content for an hour
// (no drafts, nothing private), and the cookie it sets outlives a logout.
// That is deliberate for v1-18; docs/THEMES.md says so.
const (
	previewParam  = "hm_theme_preview"
	previewCookie = "hm_theme_preview"
	previewTTL    = time.Hour
)

type themePreview struct {
	key []byte
	mgr *packages.Manager
	// secureCookie is ADMIN_COOKIE_SECURE: the preview cookie is Secure at
	// least that often (see cookieSecure).
	secureCookie bool
}

// cookieSecure is the Secure attribute of the preview cookie: set when the
// operator asked for Secure cookies, over TLS, or behind a trusted proxy that
// says it terminated TLS (the same rule as the admin's cookies).
func (p *themePreview) cookieSecure(r *http.Request) bool {
	if p.secureCookie || r.TLS != nil {
		return true
	}
	return middleware.IsTrustedProxy(r.RemoteAddr) && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// newThemePreview derives its signing key from the server secret, so it never
// shares a MAC domain with another feature.
func (s *Server) newThemePreview() *themePreview {
	if s.packages == nil {
		return nil
	}
	return &themePreview{key: previewKey(s.cfg.Secret), mgr: s.packages, secureCookie: s.cfg.AdminCookieSecure}
}

func previewKey(secret string) []byte {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte("hatamoto/theme-preview"))
	return m.Sum(nil)
}

func (p *themePreview) sign(payload string) string {
	m := hmac.New(sha256.New, p.key)
	m.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// Token is the signed value that starts a preview of theme.
func (p *themePreview) Token(theme string, now time.Time) string {
	payload := theme + "|" + strconv.FormatInt(now.Add(previewTTL).Unix(), 10)
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + p.sign(payload)
}

// verify returns the theme a token or cookie value names.
func (p *themePreview) verify(v string, now time.Time) (string, bool) {
	enc, sig, ok := strings.Cut(v, ".")
	if !ok {
		return "", false
	}
	raw, err := base64.RawURLEncoding.DecodeString(enc)
	if err != nil {
		return "", false
	}
	payload := string(raw)
	if !hmac.Equal([]byte(sig), []byte(p.sign(payload))) {
		return "", false
	}
	theme, exp, ok := strings.Cut(payload, "|")
	if n, err := strconv.ParseInt(exp, 10, 64); !ok || err != nil || now.Unix() > n {
		return "", false
	}
	return theme, true
}

// engine returns the preview engine the request's cookie selects, or nil.
func (p *themePreview) engine(r *http.Request) *render.Engine {
	if p == nil {
		return nil
	}
	c, err := r.Cookie(previewCookie)
	if err != nil {
		return nil
	}
	theme, ok := p.verify(c.Value, time.Now())
	if !ok {
		return nil
	}
	eng, err := p.mgr.PreviewEngine(r.Context(), theme)
	if err != nil {
		return nil // the theme was uninstalled: show the real site
	}
	return eng
}

// start handles ?hm_theme_preview=<token|off>: it sets or clears the cookie and
// redirects to the same URL without the parameter. It reports whether it
// answered the request.
func (p *themePreview) start(w http.ResponseWriter, r *http.Request) bool {
	if p == nil {
		return false
	}
	tok := r.URL.Query().Get(previewParam)
	if tok == "" {
		return false
	}
	c := &http.Cookie{Name: previewCookie, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: p.cookieSecure(r)}
	switch _, ok := p.verify(tok, time.Now()); {
	case tok == "off" || !ok:
		c.MaxAge, c.Value = -1, ""
	default:
		c.Value, c.MaxAge = tok, int(previewTTL.Seconds())
	}
	http.SetCookie(w, c)
	q := r.URL.Query()
	q.Del(previewParam)
	u := url.URL{Path: r.URL.Path, RawQuery: q.Encode()}
	w.Header().Set("Cache-Control", cachePrivate)
	http.Redirect(w, r, u.String(), http.StatusSeeOther)
	return true
}

// forRequest answers a preview start, or returns the handlers to use for r:
// h itself, or a copy that renders with the preview engine and never caches.
func (h *renderHandlers) forRequest(w http.ResponseWriter, r *http.Request) (*renderHandlers, bool) {
	if h.preview.start(w, r) {
		return nil, true
	}
	eng := h.preview.engine(r)
	if eng == nil {
		return h, false
	}
	hp := *h
	hp.engine, hp.uncached = eng, true
	return &hp, false
}

// overrideAssets makes the active engine serve the preview theme's assets to
// requests carrying a preview cookie (its stylesheet is fingerprinted per theme).
func overrideAssets(p *themePreview, active *render.Engine) {
	if p == nil {
		return
	}
	active.OverrideAssets(func(r *http.Request) http.Handler {
		eng := p.engine(r)
		if eng == nil {
			return nil
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", cachePrivate)
			eng.AssetHandler().ServeHTTP(w, r)
		})
	})
}
