package server

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"hatamoto.dev/core/internal/agents"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/render"
)

// handleRobots serves /robots.txt with the same default policy as the Next.js
// frontend.
func (s *Server) handleRobots(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=60, s-maxage=60")
	var lines []string
	if set := s.seoSettings(r.Context()); set != nil {
		lines = set.Robots.ExtraLines
	}
	_, _ = w.Write([]byte(render.RobotsForAdminLines(s.publicBaseURL(r), nil, lines, s.robotsDenyTokens(r.Context()), s.adminPathValue())))
}

// handleAdsTxt serves /ads.txt (or /app-ads.txt when app is set) exactly as
// saved. An empty file answers a plain 404, not the themed page, so ad
// systems see "no file" rather than an HTML document.
func (s *Server) handleAdsTxt(app bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body string
		if set := s.seoSettings(r.Context()); set != nil {
			body = set.AdsTxt
			if app {
				body = set.AppAdsTxt
			}
		}
		if strings.TrimSpace(body) == "" {
			// No saved file: a redirect rule on this path (a hosted ads.txt) still applies.
			if rd := s.matchRedirect(r.Context(), r.URL.Path); rd != nil {
				writeRenderRedirect(w, r, &redirectHit{Target: rd.TargetURL, Status: rd.StatusCode, KeepQuery: rd.Expanded})
				return
			}
			http.Error(w, "404 page not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "public, max-age=60, s-maxage=60")
		_, _ = w.Write([]byte(body))
	}
}

// handleSitemapXSL serves /sitemap.xsl, referenced by the sitemap processing
// instruction.
func (s *Server) handleSitemapXSL(w http.ResponseWriter, r *http.Request) {
	b := render.SitemapXSL()
	if len(b) == 0 {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/xsl; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=2592000")
	_, _ = w.Write(b)
}

// robotsDenyTokens returns the user-agent tokens the admin's robots policy
// denies. Any failure (or no store) yields none, so robots.txt falls back to
// the default output, like the Next.js route.
func (s *Server) robotsDenyTokens(ctx context.Context) []string {
	if s.robotsPolicy == nil {
		return nil
	}
	denied, err := s.robotsPolicy.DeniedPurposes(ctx)
	if err != nil {
		// Never answer with the default file: it lacks the owner's crawler
		// policy. Flag it so the route answers 503 (see busyConverter).
		if errors.Is(err, database.ErrPoolBusy) {
			database.NotePoolBusy(ctx)
		}
		return nil
	}
	return agents.DenyTokens(agents.Default(), denied)
}

// rootIconHandler serves a site icon at the site root from the theme's
// icons/ directory (the embedded default set unless the theme overrides it).
func rootIconHandler(assets http.Handler, name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r2 := r.Clone(r.Context())
		r2.URL.Path = render.AssetPrefix + render.IconDir + name
		assets.ServeHTTP(w, r2)
	}
}
