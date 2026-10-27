package server

import "context"

// siteName is the name shown in outbound emails: the owner's site name from the
// database (set in the setup wizard and site settings), falling back to
// HATAMOTO_SITE_NAME while the stored value is empty. The store caches for 60 s.
func (s *Server) siteName(ctx context.Context) string {
	if s.siteConfig != nil {
		if cfg, err := s.siteConfig.Get(ctx); err == nil && cfg != nil && cfg.CompanyName != "" {
			return cfg.CompanyName
		}
	}
	if s.cfg != nil {
		return s.cfg.Hatamoto.SiteName
	}
	return ""
}
