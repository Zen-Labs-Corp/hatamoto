package server

import (
	"context"
	"fmt"
	"net/http"

	"hatamoto.dev/core/internal/httpx"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/middleware"
	"hatamoto.dev/core/internal/pluginhost"
	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/sdk"
)

// setupRender builds the theme engine. A theme that fails to load is recorded
// in s.renderErr and makes Start fail: silently serving only the JSON API
// would put a site without pages in production.
func (s *Server) setupRender() {
	s.renderData = newRenderData(s)
	// hm.js is bundled when the engine is built, so the prefix must be set first.
	render.SetPublicAPIPrefix(s.cfg.PublicSite.APIPrefix)
	render.SetTrackEngagement(func() bool { return s.analyticsService == nil || s.analyticsService.EngagementEnabled() })
	engine, err := render.New(render.Options{
		HeadFilter: pluginhost.HeadFilter(s.hooks),
		ExtSources: pluginhost.ExtSources(s.hooks),
		Data:       s.renderData,
		Refs:       s.renderData,
		Logger:     s.logger,
	})
	if err != nil {
		s.logger.Error("render: theme failed to load", "error", err)
		render.NoteBootFault("theme-load-failed")
		s.renderErr = fmt.Errorf("render: theme failed to load: %w", err)
		return
	}
	s.renderEngine = engine
	// Save-time schema sanitising needs the theme's field kinds.
	entity.SetFieldKindResolver(engine.FieldKind)
	s.logger.Info("render: Go theme rendering enabled", "theme_version", engine.Version())
}

// mountRenderRoutes registers the root HTML routes (pages, robots, sitemaps,
// llms.txt, theme assets, no-JS form post). It does nothing unless the engine
// exists (it is nil only when the theme failed to load, which fails Start). Static and /api, /media,
// /health routes win over the "/{rest...}" catch-all by ServeMux's most-specific-pattern precedence.
func (s *Server) mountRenderRoutes(r *httpx.Group) {
	if s.renderEngine == nil {
		return
	}
	if s.pageRuntime == nil {
		s.pageRuntime = newPageCacheRuntime()
	}
	h := s.newRenderHandlers(s.renderEngine, s.infra.Cache)
	s.renderHandlers = h
	overrideAssets(h.preview, s.renderEngine)
	capture := func(next http.Handler) http.Handler {
		return s.agentCaptureMiddleware(s.aiReferralMiddleware(next))
	}
	mountRenderRoutes(r, renderRoutes{
		pages:      h,
		assets:     s.renderEngine.AssetHandler(),
		robots:     s.handleRobots,
		sitemap:    s.handlePublicSitemap,
		subSitemap: s.handlePublicSubSitemap,
		llms:       s.handleLLMsTxt,
		feed:       s.handleFeed,
		xsl:        s.handleSitemapXSL,
		formPost:   s.handlePublicFormPost,
		capture:    capture,
	})
}

// renderRoutes are the handlers mountRenderRoutes wires; split from Server so
// the routing is testable without a database.
type renderRoutes struct {
	pages      *renderHandlers
	assets     http.Handler
	robots     http.HandlerFunc
	sitemap    http.HandlerFunc
	subSitemap http.HandlerFunc
	llms       http.HandlerFunc
	feed       http.HandlerFunc
	xsl        http.HandlerFunc
	formPost   http.HandlerFunc
	// capture (optional) records AI-agent hits on every crawler-visible route.
	capture func(http.Handler) http.Handler
}

func mountRenderRoutes(r *httpx.Group, rt renderRoutes) {
	// Every crawler-visible route records agent hits (and AI-referred human
	// visits to pages) in-process here.
	capture := rt.capture
	if capture == nil {
		capture = func(next http.Handler) http.Handler { return next }
	}
	// A "GET" pattern also serves HEAD, so HEAD reaches these handlers rather
	// than the page catch-all.
	captured := r.Group("", capture)
	captured.HandleFunc("GET /robots.txt", rt.robots)
	captured.HandleFunc("GET /sitemap.xml", rt.sitemap)
	captured.HandleFunc("GET /llms.txt", rt.llms)
	if rt.feed != nil {
		captured.HandleFunc("GET /feed.xml", rt.feed)
		captured.HandleFunc("GET /atom.xml", rt.feed)
	}
	r.HandleFunc("GET /sitemap.xsl", rt.xsl)
	for _, name := range render.RootIcons {
		r.HandleFunc("GET /"+name, rootIconHandler(rt.assets, name))
	}
	// GET only: an any-method pattern would conflict with the "GET /{rest...}"
	// catch-all below.
	r.Handle("GET /theme/assets/{rest...}", rt.assets)

	// Own limiter: the public API form route has its own bucket, and the two
	// must not share a budget.
	formLimiter := middleware.NewRateLimiter(0.1, 5)
	handleCORS(r, http.MethodPost, "/forms/{slug}/submit", rt.formPost, formLimiter.Middleware)

	r.Group("", capture).HandleFunc("GET /", rt.pages.handleRenderHome)
	page := capture(http.HandlerFunc(rt.pages.handleRenderCatchAll))
	pages := r.Group("", capture)
	subSitemap := capture(rt.subSitemap)
	// "sitemap-<sub>.xml" cannot be a ServeMux pattern (wildcards span a
	// whole segment), so single-segment paths are routed here: sitemap
	// children go to subSitemap, everything else to the pages.
	r.HandleFunc("GET /{file}", func(w http.ResponseWriter, req *http.Request) {
		if _, ok := sitemapChild(req.PathValue("file")); ok {
			subSitemap.ServeHTTP(w, req)
			return
		}
		page.ServeHTTP(w, req)
	})
	pages.HandleFunc("GET /{rest...}", rt.pages.handleRenderCatchAll)
}

// mountRenderAdminRoutes registers the editor-facing render API inside the
// protected /api/v1 group (auth and CSRF are applied by the group).
func (s *Server) mountRenderAdminRoutes(r *httpx.Group) {
	if s.renderEngine == nil {
		return
	}
	h := s.newRenderAdminHandlers(s.renderEngine)
	mountRenderAdmin(r, h)
}

func mountRenderAdmin(r *httpx.Group, h *renderAdminHandlers) {
	{
		r := r.Group("/render", middleware.RequireRole("owner", "editor"))
		r.HandleFunc("GET /theme", h.handleRenderTheme)
		r.HandleFunc("POST /preview", h.handleRenderPreview)
	}
}

// invalidateRendered drops cached rendered pages and sitemaps after a change
// to content or shared chrome (entities, site config, menus, settings) and
// schedules the debounced preload when the owner enabled it.
func (s *Server) invalidateRendered(ctx context.Context) {
	s.purgeRendered(ctx)
	s.pageCache.notifyContentChanged()
}

// purgeRendered drops cached pages and sitemaps without scheduling a preload
// (Clear cache and switching the cache off use it).
func (s *Server) purgeRendered(ctx context.Context) {
	if s.infra == nil || s.infra.CacheInvalidator == nil {
		return
	}
	if err := s.infra.CacheInvalidator.InvalidateRendered(ctx); err != nil {
		s.logger.Warn("render: invalidate cached pages", "error", err)
	}
	// Sitemaps list the same published set and depend on the same settings, so
	// they go with the pages.
	if err := s.infra.CacheInvalidator.InvalidateSitemaps(ctx); err != nil {
		s.logger.Warn("sitemap: invalidate cached sitemaps", "error", err)
	}
}

// registerRenderInvalidation drops the cached rendered pages whenever an
// entity is created, updated, published (or unpublished) or deleted; cached
// sitemaps go with them (scheduled publishes fire the same hook). Pages
// embed siblings and navigation, so the whole set goes (coarse on purpose).
// Menu edits call invalidateRendered from their handlers.
func (s *Server) registerRenderInvalidation() {
	for _, name := range []string{
		sdk.EntityCreated.Name, sdk.EntityUpdated.Name,
		sdk.EntityPublished.Name, sdk.EntityDeleted.Name, sdk.ImportCompleted.Name,
	} {
		s.hooks.AddAction(name, 0, func(ctx context.Context, _ any) { s.invalidateRendered(ctx) })
	}
}

// communityScriptsLive reports a community plugin script live on public pages;
// such pages are served in a CSP sandbox (opaque origin).
func (s *Server) communityScriptsLive() bool {
	if s.communityLive != nil { // test seam: a community script cannot be live in a test server
		return s.communityLive()
	}
	return s.packages != nil && s.packages.CommunityScriptsLive()
}

// communityPlugin reports whether name is a community-origin plugin, by the
// origins map communityScriptsLive reads.
func (s *Server) communityPlugin(name string) bool {
	return s.packages != nil && s.packages.CommunityPlugin(name)
}
