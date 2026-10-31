package server

import (
	"bytes"
	"context"
	"net/http"
	"net/url"
	"strings"

	"hatamoto.dev/core/internal/agents"
	"hatamoto.dev/core/internal/analytics"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/mcpserver"
	"hatamoto.dev/core/internal/middleware"
)

// maxMCPBody is the largest page Fetch hands to a tool (a cap on memory, not
// on what the site serves).
const maxMCPBody = 4 << 20

// mountMCP serves the Model Context Protocol endpoint at /mcp. It does its own
// authentication (API key as a Bearer token), so it sits outside /api/v1's
// session middleware; the tools check the key's scopes.
func (s *Server) mountMCP(r *httpx.Group) {
	deps := mcpserver.Deps{
		Keys:           s.apiKeyService,
		Limiter:        s.apiKeyRateLimiter,
		Entities:       s.entityService,
		PublicEntities: entity.NewPostgresPublicRepository(s.db),
		Media:          s.mediaService,
		Menus:          s.menuService,
		Taxonomy:       s.taxonomyService,
		Redirects:      s.redirectService,
		Crawlers:       agents.NewStatsRepository(s.db),
		Referrals:      analytics.NewReader(s.db),
		Traffic:        analytics.NewReader(s.db),
		Audit:          mcpserver.NewPostgresAuditor(s.db, s.logger),
		Logger:         s.logger,
		ClientIP:       middleware.GetClientIP,
		PublicOrigin:   publicOrigin(s.cfg),
		AllowedOrigins: mcpAllowedOrigins(s.cfg),
		Fetch:          s.fetchPublic,
		PageURL:        s.entityPublicPath,
		Changed:        s.invalidateRendered,
		Design:         s.mcpDesign,
		RenderPage:     s.mcpRenderPage,
	}
	if s.sectionTemplateService != nil {
		deps.Templates = s.sectionTemplateService
	}
	h := mcpserver.New(deps, "1")
	for _, method := range []string{http.MethodPost, http.MethodGet, http.MethodDelete} {
		r.Handle(method+" /mcp", h)
	}
}

func mcpAllowedOrigins(cfg *config.Config) []string {
	if cfg == nil {
		return nil
	}
	return cfg.MCPAllowedOrigins
}

// publicOrigin is scheme://host of PUBLIC_BASE_URL ("" when unset).
func publicOrigin(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	u, err := url.Parse(strings.TrimSpace(cfg.PublicSite.BaseURL))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// entityPublicPath is the public path of an entity.
func (s *Server) entityPublicPath(ctx context.Context, e *entity.Entity) string {
	return locateEntity(ctx, s.urlSnapshot(ctx), entity.NewPostgresPublicRepository(s.db), e).Path
}

// memResponse is an http.ResponseWriter that keeps the answer in memory.
type memResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (m *memResponse) Header() http.Header { return m.header }
func (m *memResponse) WriteHeader(code int) {
	if m.status == 0 {
		m.status = code
	}
}
func (m *memResponse) Write(b []byte) (int, error) {
	m.WriteHeader(http.StatusOK)
	if room := maxMCPBody - m.body.Len(); room > 0 {
		m.body.Write(b[:min(len(b), room)])
	}
	return len(b), nil
}

// fetchPublic serves GET path through the site's own handler chain, in
// process: no socket, same bytes crawlers get. The Host is the configured
// public base URL's, so canonical links match production.
func (s *Server) fetchPublic(ctx context.Context, path string) (mcpserver.Response, error) {
	host := "localhost"
	if s.cfg != nil {
		if u, err := url.Parse(strings.TrimSpace(s.cfg.PublicSite.BaseURL)); err == nil && u.Host != "" {
			host = u.Host
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+host+path, nil)
	if err != nil {
		return mcpserver.Response{}, err
	}
	req.Header.Set("User-Agent", "hatamoto-mcp-preview")
	rec := &memResponse{header: http.Header{}}
	s.router.ServeHTTP(rec, req)
	if rec.status == 0 {
		rec.status = http.StatusOK
	}
	return mcpserver.Response{Status: rec.status, Body: rec.body.Bytes()}, nil
}
