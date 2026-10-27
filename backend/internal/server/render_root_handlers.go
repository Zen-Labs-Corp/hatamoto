package server

import (
	"context"
	"net/http"

	"hatamoto.dev/core/internal/agents"
	"hatamoto.dev/core/internal/render"
)

// handleRobots serves /robots.txt with the same default policy as the Next.js
// frontend.
func (s *Server) handleRobots(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300, s-maxage=300")
	_, _ = w.Write([]byte(render.RobotsForAdmin(s.publicBaseURL(r), nil, s.robotsDenyTokens(r.Context()), s.adminPathValue())))
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
