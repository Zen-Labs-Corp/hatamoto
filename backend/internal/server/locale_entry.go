package server

import (
	"context"
	"net/http"
	"slices"
	"strings"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/siteconfig"
)

// localeEntryVary names every request input the entry decision reads, so a
// shared cache keys on it and cannot replay one visitor's answer to another.
const localeEntryVary = "Cookie, CF-IPCountry, CloudFront-Viewer-Country"

// localeEntryState is what the entry wrapper needs of the site: the locale set
// and the visitor entry settings.
type localeEntryState struct {
	set   *entity.LocaleSet
	entry siteconfig.VisitorEntry
}

// loadLocaleEntry reads the state for one request; ok is false when the site is
// either source is unavailable, which fails open.
func (s *Server) loadLocaleEntry(ctx context.Context) (localeEntryState, bool) {
	if s.entityService == nil || s.siteConfig == nil {
		return localeEntryState{}, false
	}
	snap, err := s.urlSnapshotOrErr(ctx)
	if err != nil || snap.Locales() == nil {
		return localeEntryState{}, false
	}
	cfg, err := s.siteConfig.Get(ctx)
	if err != nil {
		return localeEntryState{}, false
	}
	return localeEntryState{set: snap.Locales(), entry: cfg.VisitorEntry}, true
}

// localeEntry sends visitors of "/" and of the configured entry paths to their
// locale, and serves the /locale switcher, on a multi-locale site. It sits in
// front of the mux (and so the page cache, which would serve a cached "/"
// before any handler could redirect) and behind the site gate. A visitor is
// placed by the saved hm_locale cookie, then the legacy cookie (only when the
// core cookie is absent), then the CDN country header, then the default
// locale. Only GET and HEAD are handled; with the setting off, on a
// single-locale site or on any error the request passes to next untouched.
func (s *Server) localeEntry(next http.Handler) http.Handler {
	return s.localeEntryFrom(next, s.loadLocaleEntry)
}

func (s *Server) localeEntryFrom(next http.Handler, load func(context.Context) (localeEntryState, bool)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if (r.Method != http.MethodGet && r.Method != http.MethodHead) || s.underPublicAPIPrefix(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		p := r.URL.Path
		st, ok := load(r.Context())
		if !ok || !st.set.Multi() {
			next.ServeHTTP(w, r)
			return
		}
		if p == "/locale" && !hasLocaleSegment(st.set, "locale") {
			s.localeSwitch(w, r, st.set)
			return
		}
		p = strings.TrimSuffix(p, "/")
		if p == "" {
			p = "/"
		}
		if !st.entry.Enabled || (p != "/" && !slices.Contains(st.entry.Paths, p)) {
			next.ServeHTTP(w, r)
			return
		}
		tag := pickEntryLocale(r, st)
		prefix := st.set.Prefix(tag)
		if prefix == "" {
			// The default locale lives at the unprefixed URL: serve the page
			// as today, keyed on the inputs of the decision.
			next.ServeHTTP(&varyWriter{ResponseWriter: w}, r)
			return
		}
		target := prefix
		if p != "/" {
			target += p
		}
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		// Written directly, not with http.Redirect, which re-cleans the path.
		h := w.Header()
		h.Set("Location", target)
		h.Set("Cache-Control", "private, no-store")
		h.Add("Vary", localeEntryVary)
		w.WriteHeader(http.StatusFound)
	})
}

// hasLocaleSegment reports whether any locale, public or hidden, uses seg as
// its URL segment. An install that kept the segment "locale" from before the
// switcher existed serves that locale's home at /locale, so the switcher stays
// off there.
func hasLocaleSegment(set *entity.LocaleSet, seg string) bool {
	for _, e := range set.Entries() {
		if e.Segment == seg {
			return true
		}
	}
	return false
}

// pickEntryLocale is the locale tag a visitor lands on: a public locale, the
// default when nothing else matches.
func pickEntryLocale(r *http.Request, st localeEntryState) string {
	if c, err := r.Cookie(localeCookieName); err == nil {
		if tag, ok := lookupLocaleValue(st.set, c.Value); ok {
			return tag
		}
	} else if name := st.entry.LegacyCookie; name != "" {
		if c, err := r.Cookie(name); err == nil {
			if tag, ok := lookupLocaleValue(st.set, c.Value); ok {
				return tag
			}
		}
	}
	if cc := strings.ToUpper(strings.TrimSpace(consentCountry(r))); len(cc) == 2 {
		if tag, ok := st.set.Pick(cc); ok {
			return tag
		}
	}
	return st.set.Default()
}

// varyWriter adds the entry Vary tokens as the headers are sent, so a handler
// that sets its own Vary (or a cache hit replaying stored headers) keeps them.
// It also forces Cache-Control private, no-store, which replaces the page's own
// public value: a shared cache that ignores Vary on these inputs must not store
// the default locale's page and replay it to a visitor who is to be redirected.
type varyWriter struct {
	http.ResponseWriter
	sent bool
}

func (v *varyWriter) markSent() {
	if v.sent {
		return
	}
	v.sent = true
	h := v.Header()
	h.Add("Vary", localeEntryVary)
	h.Set("Cache-Control", "private, no-store")
}

func (v *varyWriter) WriteHeader(code int) {
	if code >= 200 || code == http.StatusSwitchingProtocols {
		v.markSent()
	}
	v.ResponseWriter.WriteHeader(code)
}

func (v *varyWriter) Write(b []byte) (int, error) {
	v.markSent()
	return v.ResponseWriter.Write(b)
}

func (v *varyWriter) Flush() { http.NewResponseController(v.ResponseWriter).Flush() }

func (v *varyWriter) Unwrap() http.ResponseWriter { return v.ResponseWriter }
