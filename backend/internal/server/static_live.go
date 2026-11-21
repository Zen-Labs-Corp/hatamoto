package server

import (
	"net/http"
	"strings"

	"hatamoto.dev/core/internal/statichost"
)

// staticLive fronts the public chain in static mode: page GETs on the live
// origin go to the static site (301), and plugin public routes answer CORS for
// the static origin. It sits after the site access gate (static mode refuses a
// gated site) and before localeEntry, so a locale redirect never sends a
// visitor to a live-origin page first. Served mode: next, untouched.
//
// Only a request addressed to the live host is redirected, so the static site
// itself (and the export, which renders with the static host) can never loop.
// The target is always the static origin plus the request URI, so a path that
// starts with "//" cannot turn the redirect into an open redirect.
func (s *Server) staticLive(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hs := s.hosting.Load()
		if !hs.Active() {
			next.ServeHTTP(w, r)
			return
		}
		origin := r.Header.Get("Origin")
		if strings.HasPrefix(r.URL.Path, "/p/") {
			if r.Method == http.MethodOptions && hs.AllowsOrigin(origin) {
				staticPreflight(w.Header(), hs, origin, "GET, POST, PUT, PATCH, DELETE, OPTIONS")
				w.WriteHeader(http.StatusNoContent)
				return
			}
			staticCORS(w.Header(), hs, origin)
			next.ServeHTTP(w, r) // OPTIONS from another origin keeps today's 405
			return
		}
		if (r.Method == http.MethodGet || r.Method == http.MethodHead) && !statichost.IsExport(r.Context()) &&
			strings.EqualFold(r.Host, hs.LiveHost()) && !s.staticNoRedirect(r.URL.Path) && !themePreviewRequest(r) {
			// A switch back to served mode must not stick in browser caches.
			w.Header().Set("Cache-Control", "public, max-age=3600")
			http.Redirect(w, r, hs.Site+r.URL.RequestURI(), http.StatusMovedPermanently)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// themePreviewRequest reports a request that starts or carries a theme
// preview. The preview is rendered by the live origin, and its token must not
// be sent on to the static host.
func themePreviewRequest(r *http.Request) bool {
	if r.URL.Query().Has(previewParam) {
		return true
	}
	_, err := r.Cookie(previewCookie)
	return err == nil
}
