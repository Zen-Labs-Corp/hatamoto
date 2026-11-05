package server

import (
	"context"

	"hatamoto.dev/core/internal/render"
)

// installCustomCSS lets the asset handler read the owner's additional CSS from
// the site config (cached) when custom.<hash>.css is requested.
func (s *Server) installCustomCSS() {
	render.SetCustomCSS(func(ctx context.Context) string {
		if s.siteConfig == nil {
			return ""
		}
		cfg, err := s.siteConfig.Get(ctx)
		if err != nil {
			return ""
		}
		return render.AdditionalCSS(cfg.ThemeExtra)
	})
}
