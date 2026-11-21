package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/authz"
	"hatamoto.dev/core/internal/httpx"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/fonts"
	"hatamoto.dev/core/internal/middleware"
	"hatamoto.dev/core/internal/pluginhost"
	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/core/internal/statichost"
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
	s.installConsentRender()
	s.installCustomCSS()
	s.installTagsRender()
	engine, err := render.New(render.Options{
		HeadFilter:      pluginhost.HeadFilter(s.hooks),
		SiteGraphFilter: pluginhost.SiteGraphFilter(s.hooks),
		ExtSources:      pluginhost.ExtSources(s.hooks),
		Data:            s.renderData,
		Refs:            s.renderData,
		Logger:          s.logger,
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
		return prefetchMiddleware(s.agentCaptureMiddleware(s.aiReferralMiddleware(next)))
	}
	// Always mounted, so the route (and its reserved /fonts prefix) exists
	// even when no store is wired: it then answers 404.
	fontsHandler := http.NotFoundHandler()
	if s.fonts != nil {
		fontsHandler = fonts.Handler(s.fonts)
	}
	mountRenderRoutes(r, renderRoutes{
		hosting:            &s.hosting,
		pages:              h,
		assets:             s.renderEngine.AssetHandler(),
		fonts:              fontsHandler,
		robots:             s.handleRobots,
		sitemap:            s.handlePublicSitemap,
		subSitemap:         s.handlePublicSubSitemap,
		llms:               s.handleLLMsTxt,
		adsTxt:             s.handleAdsTxt(false),
		appAdsTxt:          s.handleAdsTxt(true),
		feed:               s.handleFeed,
		xsl:                s.handleSitemapXSL,
		formPost:           s.handlePublicFormPost,
		keyFile:            s.indexNowKeyFile,
		capture:            capture,
		preview:            s.handlePreviewShare,
		commentConfirm:     s.handleCommentConfirm,
		commentUnsubscribe: s.handleCommentUnsubscribe,
		commentPost:        s.handleCommentPost,
		commentEdit:        s.handleCommentEdit,
		commentDelete:      s.handleCommentDelete,
		commentFormLive:    s.handleCommentFormLive,
		formPage:           s.handleFormPage,
		favicon:            s.siteFavicon,

		acquireBound: s.cfg.Database.PublicAcquireTimeout,
	})
}

// Comment POST paths, next to identity's /-/comments/confirm/ (the "-" first
// segment is reserved for the system, so no user path can collide).
const (
	commentPostPrefix   = "/-/comments/"
	commentEditPrefix   = "/-/comments/edit/"
	commentDeletePrefix = "/-/comments/delete/"
)

// renderRoutes are the handlers mountRenderRoutes wires; split from Server so
// the routing is testable without a database.
type renderRoutes struct {
	// acquireBound is the public acquire timeout; 0 disables the bound.
	acquireBound time.Duration
	pages        *renderHandlers
	hosting      *statichost.Holder // static hosting snapshot; nil means served mode
	assets       http.Handler
	// fonts (optional) serves GET /fonts/<sha256>.woff2 from the font store.
	fonts      http.Handler
	robots     http.HandlerFunc
	sitemap    http.HandlerFunc
	subSitemap http.HandlerFunc
	llms       http.HandlerFunc
	// adsTxt and appAdsTxt (optional) serve /ads.txt and /app-ads.txt.
	adsTxt, appAdsTxt http.HandlerFunc
	feed              http.HandlerFunc
	xsl               http.HandlerFunc
	formPost          http.HandlerFunc
	// keyFile (optional) answers GET /<name> with the IndexNow key while it is on.
	keyFile func(ctx context.Context, name string) (key string, ok bool)
	// capture (optional) records AI-agent hits on every crawler-visible route.
	capture func(http.Handler) http.Handler
	// preview (optional) serves a shared draft preview link. It is mounted
	// outside capture and the page cache.
	preview http.HandlerFunc
	// commentConfirm (optional) serves the comment confirmation link,
	// /-/comments/confirm/{token}. Mounted outside capture and the page cache:
	// GET only shows a button, POST confirms.
	commentConfirm http.HandlerFunc
	// commentUnsubscribe (optional) serves the unsubscribe link of comment mail,
	// /-/comments/unsubscribe/{token}: GET shows a button, POST (also the
	// one-click request) turns the mail off. Mounted whether or not comments
	// are on, so people can always stop mail.
	commentUnsubscribe http.HandlerFunc
	// commentPost, commentEdit and commentDelete (optional) take the public
	// comment form, POST /-/comments/{entityID}, and the 15-minute edit and
	// delete of one's own comment. POST only, outside capture and the page
	// cache, behind the site-access gate (these paths are not in its bypass).
	commentPost, commentEdit, commentDelete http.HandlerFunc
	// commentFormLive (optional) answers GET /-/comments/form/{entityID}, the
	// comment form fragment of a static page (static mode only).
	commentFormLive http.HandlerFunc
	// formPage (optional) serves the hosted form page, GET /f/{slug}.
	formPage http.HandlerFunc
	// favicon (optional) returns the owner's favicon address ("" = none); the
	// root icon paths then redirect to it instead of serving the theme's icons.
	favicon func(context.Context) string
}

func mountRenderRoutes(r *httpx.Group, rt renderRoutes) {
	// Every crawler-visible route records agent hits (and AI-referred human
	// visits to pages) in-process here.
	capture := rt.capture
	if capture == nil {
		capture = func(next http.Handler) http.Handler { return next }
	}
	// Public page requests (and only those) give up on a busy connection pool
	// after the acquire bound and answer 503. It wraps capture, so every
	// crawler-visible route below carries it.
	bound, record := publicAcquireBound(rt.acquireBound), capture
	capture = func(next http.Handler) http.Handler {
		if rt.acquireBound > 0 {
			next = busyConverter(next)
		}
		return bound(record(next))
	}
	// A "GET" pattern also serves HEAD, so HEAD reaches these handlers rather
	// than the page catch-all.
	captured := r.Group("", capture)
	captured.HandleFunc("GET /robots.txt", rt.robots)
	captured.HandleFunc("GET /sitemap.xml", rt.sitemap)
	captured.HandleFunc("GET /llms.txt", rt.llms)
	if rt.adsTxt != nil {
		captured.HandleFunc("GET /ads.txt", rt.adsTxt)
	}
	if rt.appAdsTxt != nil {
		captured.HandleFunc("GET /app-ads.txt", rt.appAdsTxt)
	}
	if rt.feed != nil {
		captured.HandleFunc("GET /feed.xml", rt.feed)
		captured.HandleFunc("GET /atom.xml", rt.feed)
	}
	r.HandleFunc("GET /sitemap.xsl", rt.xsl)
	for _, name := range render.RootIcons {
		r.HandleFunc("GET /"+name, rootIconHandler(rt.assets, name, rt.favicon))
	}
	// GET only: an any-method pattern would conflict with the "GET /{rest...}"
	// catch-all below.
	r.Handle("GET /theme/assets/{rest...}", rt.assets)
	// Owner fonts: content-addressed files, not pages, so outside capture.
	if rt.fonts != nil {
		r.Handle("GET /fonts/{file}", rt.fonts)
	}

	// Own limiter: the public API form route has its own bucket, and the two
	// must not share a budget.
	formLimiter := middleware.NewRateLimiter(0.1, 5).ByIPv6Prefix()
	handleCORS(r, rt.hosting, http.MethodPost, "/forms/{slug}/submit", rt.formPost, formLimiter.Middleware)

	if rt.preview != nil {
		// Own limiter: the token is a bearer secret, so guessing is throttled
		// per client. The route is not in capture: nothing about it is logged
		// as page traffic.
		previewLimiter := middleware.NewRateLimiter(1, 20)
		r.HandleFunc("GET "+previewPathPrefix+"{token}", previewLimiter.Middleware(http.HandlerFunc(rt.preview)).ServeHTTP)
	}

	if rt.commentConfirm != nil {
		// Own limiter: the token is a bearer secret, so guessing is throttled
		// per client.
		confirmLimiter := middleware.NewRateLimiter(1, 20)
		h := confirmLimiter.Middleware(rt.commentConfirm).ServeHTTP
		r.HandleFunc("GET "+commentConfirmPrefix+"{token}", h) // GET also matches HEAD
		r.HandleFunc("POST "+commentConfirmPrefix+"{token}", h)
	}

	if rt.commentUnsubscribe != nil {
		unsubscribeLimiter := middleware.NewRateLimiter(1, 20)
		h := unsubscribeLimiter.Middleware(rt.commentUnsubscribe).ServeHTTP
		r.HandleFunc("GET "+commentUnsubscribePrefix+"{token}", h)
		r.HandleFunc("POST "+commentUnsubscribePrefix+"{token}", h)
	}

	if rt.commentPost != nil || rt.commentEdit != nil || rt.commentDelete != nil {
		// One per-client limiter for the three comment POST routes. Only the
		// new-comment post answers the static origin's CORS; the handlers refuse
		// cross-site posts themselves, and edit and delete have no CORS.
		commentLimiter := middleware.NewRateLimiter(0.2, 5).ByIPv6Prefix()
		if rt.commentPost != nil {
			handleStaticCORS(r, rt.hosting, http.MethodPost, commentPostPrefix+"{entityID}", rt.commentPost, commentLimiter.Middleware)
		}
		for _, m := range []struct {
			pattern string
			h       http.HandlerFunc
		}{
			{"POST " + commentEditPrefix + "{commentID}", rt.commentEdit},
			{"POST " + commentDeletePrefix + "{commentID}", rt.commentDelete},
		} {
			if m.h != nil {
				r.HandleFunc(m.pattern, commentLimiter.Middleware(m.h).ServeHTTP)
			}
		}
	}

	if rt.commentFormLive != nil {
		// Own limiter: every load issues a comment token, so loads are throttled
		// per client, separately from posts. CORS: the static site's loader
		// reads the fragment.
		formLiveLimiter := middleware.NewRateLimiter(1, 10).ByIPv6Prefix()
		handleCORS(r, rt.hosting, http.MethodGet, commentFormLivePrefix+"{entityID}", rt.commentFormLive, formLiveLimiter.Middleware)
	}

	if rt.formPage != nil {
		captured.HandleFunc("GET "+formPagePrefix+"{slug}", rt.formPage)
	}

	r.Group("", capture).HandleFunc("GET /", rt.pages.handleRenderHome)
	page := capture(http.HandlerFunc(rt.pages.handleRenderCatchAll))
	pages := r.Group("", capture)
	// Own limiter: every unknown child name costs a lookup, so children are
	// throttled per client, outside capture. The index and pages are not.
	subLimiter := middleware.NewRateLimiter(2, 30).ByIPv6Prefix()
	subSitemap := subLimiter.Middleware(capture(rt.subSitemap))
	// "sitemap-<sub>.xml" cannot be a ServeMux pattern (wildcards span a
	// whole segment), so single-segment paths are routed here: sitemap
	// children go to subSitemap, everything else to the pages.
	r.HandleFunc("GET /{file}", func(w http.ResponseWriter, req *http.Request) {
		if _, ok := sitemapChild(req.PathValue("file")); ok {
			subSitemap.ServeHTTP(w, req)
			return
		}
		if rt.keyFile != nil && strings.HasSuffix(req.PathValue("file"), ".txt") {
			if key, ok := rt.keyFile(req.Context(), req.PathValue("file")); ok {
				serveKeyFile(w, key)
				return
			}
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
		r := r.Group("/render", middleware.RequireCan(authz.ActionRenderPreview))
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
	s.staticTrigger.Notify("change", false)
}

// notifyRedirectsChanged runs after the redirect rules changed: redirects have
// no rendered-page cache to drop, but a static export carries them.
func (s *Server) notifyRedirectsChanged() {
	s.staticTrigger.Notify("redirects", false)
}

// onMediaChange runs when stored media changes: the cached logo sizes and
// image variant lookups go first, so a page rendered after the purge cannot
// name a copy that was replaced or deleted.
func (s *Server) onMediaChange(ctx context.Context) {
	s.logoSizes.reset()
	if s.renderData != nil {
		s.renderData.resetVariants()
	}
	s.invalidateRendered(ctx)
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

// invalidateEntityRendered runs when one entity's content changed. It drops
// that entity's pages, every page tagged as possibly listing any entity, and
// the sitemaps, without moving the global generation: an in-flight render of a
// page that depends on no entity is still stored.
func (s *Server) invalidateEntityRendered(ctx context.Context, id uuid.UUID) {
	s.staticTrigger.Notify("entity", false)
	if s.infra == nil || s.infra.CacheInvalidator == nil {
		return
	}
	if err := s.infra.CacheInvalidator.InvalidateEntity(ctx, id, ""); err != nil {
		s.logger.Warn("render: invalidate entity pages", "error", err)
	}
	s.pageCache.notifyContentChanged()
}

// entityEventID reads the entity ID of an entity hook payload (value or
// pointer); false when there is none or it does not parse.
func entityEventID(p any) (uuid.UUID, bool) {
	var raw string
	switch ev := p.(type) {
	case sdk.EntityEvent:
		raw = ev.ID
	case *sdk.EntityEvent:
		if ev == nil {
			return uuid.Nil, false
		}
		raw = ev.ID
	default:
		return uuid.Nil, false
	}
	id, err := uuid.Parse(raw)
	return id, err == nil
}

// registerRenderInvalidation drops cached pages and sitemaps when an entity is
// created, updated, published (or unpublished) or deleted (scheduled publishes
// fire the same hook). An event that names its entity takes the targeted path
// (invalidateEntityRendered); one without a usable ID, and import.completed,
// drop everything. Menu edits call invalidateRendered from their handlers.
// The same events change where a form is placed, so the form usage index
// is reset too.
func (s *Server) registerRenderInvalidation() {
	resetFormUsage := func() {
		if s.formUsage != nil {
			s.formUsage.Invalidate()
		}
	}
	for _, name := range []string{
		sdk.EntityCreated.Name, sdk.EntityUpdated.Name,
		sdk.EntityPublished.Name, sdk.EntityDeleted.Name,
	} {
		s.hooks.AddAction(name, 0, func(ctx context.Context, p any) {
			if id, ok := entityEventID(p); ok {
				s.invalidateEntityRendered(ctx, id)
			} else {
				s.invalidateRendered(ctx)
			}
			resetFormUsage()
		})
	}
	s.hooks.AddAction(sdk.ImportCompleted.Name, 0, func(ctx context.Context, _ any) {
		s.invalidateRendered(ctx)
		resetFormUsage()
	})
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

// pluginCSPHosts is what the enabled plugins add to the public pages' policy.
// A community plugin contributes image hosts only (policy refuses the rest at
// install; this keeps it so whatever a stored manifest says).
func (s *Server) pluginCSPHosts() pluginhost.CSPSet {
	if s.plugins == nil {
		return pluginhost.CSPSet{}
	}
	return s.plugins.CSPHosts(s.communityPlugin)
}
