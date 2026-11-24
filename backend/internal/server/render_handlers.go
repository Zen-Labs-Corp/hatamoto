package server

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/cache"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/logutil"
	"hatamoto.dev/core/internal/middleware"
	"hatamoto.dev/core/internal/pluginhost"
	"hatamoto.dev/core/internal/redirect"
	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/core/internal/statichost"
	"hatamoto.dev/sdk"
)

const (
	cachePublic = "public, max-age=60, s-maxage=300"
	// A page with a plugin directory may sit in a shared cache no longer than
	// it sits in the page cache (pluginDirectoryCacheAge).
	cachePublicDirectory = "public, max-age=60, s-maxage=60"
	cachePrivate         = "private, no-cache, no-store"

	// renderCSP replaces the global API policy on HTML responses: pages embed
	// images and iframes from https origins (and the site's own PDFs) and are framed by the admin
	// preview (same origin) only.
	renderCSP = "default-src 'self'; script-src 'self' 'unsafe-inline'; " +
		"style-src 'self' 'unsafe-inline'; img-src 'self' data: https:; " +
		"font-src 'self'; connect-src 'self'; frame-src 'self' https:; frame-ancestors 'self'; " +
		"object-src 'none'; base-uri 'self'; form-action 'self'"

	// sandboxCSP runs a page that carries community scripts in an opaque
	// origin: no allow-same-origin, so its JS has no cookies or storage of the
	// site and cannot read same-origin responses.
	sandboxCSP = "sandbox allow-scripts allow-forms allow-popups " +
		"allow-popups-to-escape-sandbox allow-top-navigation-by-user-activation"

	// renderCSPStrict is renderCSP without 'unsafe-inline' in script-src, for
	// themes that opt in (theme.json csp.strictScripts). No nonce: public HTML
	// is cached and shared, so a per-request nonce would go stale. The
	// 'inline-speculation-rules' keyword allows only the speculationrules block
	// of the link prefetch, never an executable script.
	renderCSPStrict = "default-src 'self'; script-src 'self' 'inline-speculation-rules'; " +
		"style-src 'self' 'unsafe-inline'; img-src 'self' data: https:; " +
		"font-src 'self'; connect-src 'self'; frame-src 'self' https:; frame-ancestors 'self'; " +
		"object-src 'none'; base-uri 'self'; form-action 'self'"
)

// pageRenderer is the part of *render.Engine the handlers use.
type pageRenderer interface {
	RenderPage(w io.Writer, v *render.PageView) error
	RenderNotFound(w io.Writer, v *render.PageView) error
	RenderError(w io.Writer, v *render.PageView, status int) error
	Version() string
}

// htmlCache is the slice of *cache.Cache the handlers use. A nil htmlCache disables caching.
type htmlCache interface {
	Get(ctx context.Context, key string) ([]byte, error)
	// Generation and SetIfGeneration keep a render that raced a purge out of
	// the cache: it may have read the data from before the change.
	Generation() uint64
	SetIfGeneration(ctx context.Context, key string, value []byte, ttl time.Duration, gen uint64) (bool, error)
	// SetIfGenerationTagged stores a page that depends only on the given tags:
	// it is rejected by a global purge or a drop of one of those tags.
	SetIfGenerationTagged(ctx context.Context, key string, value []byte, ttl time.Duration, gen uint64, tags ...string) (bool, error)
}

// renderHandlers serves the public site from the Go theme engine. Every
// collaborator is a field so the flow is testable without a database; the
// constructor wires the real ones. Mounting (GET /, GET /*) is done by the
// server router.
type renderHandlers struct {
	engine pageRenderer
	// hosting is the per-Server static hosting snapshot; nil means served mode.
	hosting *statichost.Holder
	// liveFn tells which paths only the live origin answers (static mode);
	// nil means the paths that need no API prefix.
	liveFn func(path string) bool
	cache  htmlCache
	// flight lets concurrent misses on one page key share one render. Nil
	// disables it; a pointer because the handlers are copied (preview, preload).
	flight *renderFlight
	// cacheTTL is how long a rendered page stays cached (HATAMOTO_PAGE_CACHE_TTL);
	// zero means config.DefaultPageCacheTTL.
	cacheTTL time.Duration
	logger   *slog.Logger
	// baseURLPinned reports that PUBLIC_BASE_URL is configured. Without it
	// the rendered canonical/OG URLs come from the request Host, so a cached
	// page would carry whatever Host the first requester sent: such pages are
	// never cached.
	baseURLPinned bool
	// searchLimit throttles /search?q= per client: it is uncached and runs a
	// ranked full-text query. Nil disables it (tests).
	searchLimit func(http.Handler) http.Handler
	// dataFailures counts failed template data lookups (recent, terms, ...);
	// a page rendered while it moved is served but not cached. Nil: no check.
	dataFailures func() int64
	// renderFailed counts a public page that could not be rendered (install
	// stats health); nil: not counted.
	renderFailed func()

	log404        func(path, referer, userAgent string)
	checkRedirect func(ctx context.Context, path string) *redirect.Redirect
	loadHome      func(r *http.Request) (*PublicPageResponse, pageOutcome, error)
	// snapshot is the URL registry snapshot; it carries the public locale set.
	// Nil: a single-locale install.
	snapshot  func(ctx context.Context) (*entity.URLSnapshot, error)
	resolve   func(ctx context.Context, path string, r *http.Request) (*routeResult, pageOutcome, error)
	buildView func(ctx context.Context, r *http.Request, rr *routeResult) (*render.PageView, error)
	// pageFilter (optional) is the render.page plugin chain. While a plugin
	// handles a page (see page_paths), that page depends on the request and is
	// never shared-cached.
	pageFilter *pluginhost.PageFilter
	// preview (optional) renders with another installed theme for a signed-in
	// owner or editor; such responses are never cached (uncached).
	preview  *themePreview
	uncached bool
	// runtime carries the live cache on/off and lifetime; nil means enabled with cacheTTL.
	runtime *pageCacheRuntime
	// cspExtras are the origins (captcha provider, enabled tag vendors) the CSP
	// adds. It is read on every response, cache hits included, so the CSP
	// follows the settings even for pages cached before they changed.
	cspExtras func(ctx context.Context) cspExtras
	// pluginCSP returns the csp hosts of the enabled plugins; nil means none.
	// Like cspExtras it is read on every response, cache hits included, so the
	// policy follows enabling and disabling at once.
	pluginCSP func() pluginhost.CSPSet
	// communityScripts reports a live community site.scripts; nil means none.
	// It is read on every response, cache hits included.
	communityScripts func() bool
	// communityPlugin reports whether a plugin is community-origin; nil means
	// none. It decides the per-page sandbox from the script tags in the body.
	communityPlugin func(name string) bool
	// encodeCached reports whether cache hits are served from stored compressed
	// variants; nil means never (the compression middleware may still compress).
	encodeCached func() bool
	// validators gives cached pages their ETag and Last-Modified and answers
	// conditional requests with 304. A pointer shared by forRequest's shallow
	// copies; nil = off.
	validators *pageValidators
	// notFound keeps rendered 404 bodies per path until the next page-cache
	// drop; nil = off. Shared by forRequest's shallow copies.
	notFound *notFoundCache
	// slot personalises the comment form of a page body per view; nil = off.
	slot *commentSlot
	// commentBypass sends a visitor with own non-public comments around the
	// shared page cache; nil = never.
	commentBypass *commentBypass
	// commentDevice finds the commenter behind the request's hm_c cookie (nil:
	// none). The render.page chain asks lazily, only for a plugin that holds
	// comments.read.
	commentDevice func(*http.Request) (uuid.UUID, bool)
}

// newRenderHandlers wires the handlers to the server's services. pageCache may
// be nil to run uncached.
func (s *Server) newRenderHandlers(engine pageRenderer, pageCache *cache.Cache) *renderHandlers {
	h := &renderHandlers{
		engine:  engine,
		hosting: &s.hosting,
		liveFn: func(p string) bool {
			prefix := ""
			if s.cfg != nil {
				prefix = s.cfg.PublicSite.APIPrefix
			}
			return staticLivePath(p, prefix)
		},
		flight: &renderFlight{},
		logger: s.logger,
		// Without PUBLIC_BASE_URL the canonical URLs come from the request
		// Host, so such pages must never be cached (see renderHandlers).
		baseURLPinned: s.cfg != nil && strings.TrimSpace(s.cfg.PublicSite.BaseURL) != "",
		loadHome:      func(r *http.Request) (*PublicPageResponse, pageOutcome, error) { return s.loadHomepage(r) },
		snapshot:      s.urlSnapshotOrErr,
		resolve:       s.resolveForRender,
		buildView:     s.buildPageViewPrefetched,
		searchLimit:   middleware.NewRateLimiter(2, 30).Middleware,
		// setupPlugins runs before the render routes mount, so s.plugins is set
		// (nil: the runtime failed, so no plugin filters).
		pageFilter:    pluginhost.NewPageFilter(s.plugins, s.hooks),
		renderFailed:  func() { s.healthSink().RenderError() },
		preview:       s.preview,
		runtime:       s.pageRuntime,
		slot:          s.commentSlot,
		commentBypass: s.commentBypass,
		commentDevice: s.commentDevice,

		cspExtras:        s.cspExtras,
		pluginCSP:        s.pluginCSPHosts,
		communityScripts: func() bool { return qaSandboxForced() || s.communityScriptsLive() },
		communityPlugin:  s.communityPlugin,
		// Demo pages get their banner from a middleware that needs plain bytes.
		encodeCached: func() bool { return s.compressOn.Load() && !s.demoOn() },
		notFound:     newNotFoundCache(),
	}
	// Demo pages get their banner from a middleware that rewrites the body, so
	// an ETag of the cached bytes would not describe what goes out.
	if !s.demoOn() {
		h.validators = newPageValidators(nil)
	}
	if s.cfg != nil {
		h.cacheTTL = s.cfg.PageCacheTTL
	}
	if s.renderData != nil {
		h.dataFailures = s.renderData.Failures
	}
	if pageCache != nil {
		h.cache = pageCache
	}
	if s.redirectService != nil {
		h.checkRedirect = s.matchRedirect
		h.log404 = s.redirectService.Log404Async
	}
	return h
}

// resolveRouteForRender resolves a public path for the renderer. It does no
// 404 logging of its own: the handler logs the 404 once, whatever the source
// of "not found".
func (s *Server) resolveRouteForRender(ctx context.Context, path string, r *http.Request) (*routeResult, pageOutcome, error) {
	deps := routeDeps{
		snapshot: s.urlSnapshotOrErr,
		repo:     entity.NewPostgresPublicRepository(s.db),
		loadPage: s.loadPublicPage,
		loadHome: s.loadHomepage,
	}
	if s.redirectService != nil {
		deps.checkRedirect = s.matchRedirect
	}
	return s.resolveRouteWith(ctx, deps, path, withoutAccept(r))
}

// withoutAccept returns r without its Accept header. The loaders switch on
// Accept (application/ld+json answers with a bare JSON-LD outcome and no
// page), which must never reach the HTML render path.
func withoutAccept(r *http.Request) *http.Request {
	if r == nil {
		return nil
	}
	r2 := r.Clone(r.Context())
	r2.Header.Del("Accept")
	return r2
}

// handleRenderHome serves "/" from the homepage.
func (h *renderHandlers) handleRenderHome(w http.ResponseWriter, r *http.Request) {
	h, done := h.forRequest(w, r)
	if done {
		return
	}
	if h.redirectIfMatched(w, r, "/") {
		return
	}
	// With prefix-all every locale, the default included, lives under its own
	// segment: the bare root points at the default locale's home. Never cached.
	scoped := database.WithPoolBusyScope(r.Context())
	set, lookupErr := h.localeSet(scoped)
	if h.redirectHomeToDefault(w, r, set) {
		return
	}
	// A failed lookup above must not decide the page on a miss: the page is
	// built (and cached) only with a locale set that was actually read.
	onMiss := func() bool {
		if lookupErr == nil {
			return false
		}
		if h.failFastIfBusy(w, r, scoped) {
			return true
		}
		var err error
		if set, err = h.localeSet(r.Context()); err != nil || database.PoolBusyHit(r.Context()) {
			database.NotePoolBusy(r.Context())
			h.writeBusy(w, r)
			return true
		}
		return h.redirectHomeToDefault(w, r, set)
	}
	h.serve(w, r, "/", onMiss, func() (*routeResult, pageOutcome, error) {
		if !set.Multi() {
			return homeRoute(h.loadHome(r))
		}
		// The unprefixed home is the default locale's, like every other
		// unprefixed path: it sees the default locale's rows and the shared ones.
		def := set.Default()
		rr, outcome, err := homeRoute(h.loadHome(r.WithContext(entity.WithVisibleLocales(r.Context(), def))))
		if rr != nil {
			rr.Locale, rr.Rest = def, "/"
		}
		return rr, outcome, err
	})
}

// redirectHomeToDefault answers the bare root of a prefix-all site with a
// redirect to the default locale's home and reports whether it did.
func (h *renderHandlers) redirectHomeToDefault(w http.ResponseWriter, r *http.Request, set *entity.LocaleSet) bool {
	if !set.Multi() {
		return false
	}
	prefix := set.Prefix(set.Default())
	if prefix == "" {
		return false
	}
	w.Header().Set("Cache-Control", "no-store")
	target := prefix
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	http.Redirect(w, r, target, http.StatusFound)
	return true
}

// failFastIfBusy answers 503 when the pre-cache lookup under scoped gave up on
// a busy pool: the request has used its bound already, so it is flagged now
// instead of waiting a second bound on the same load.
func (h *renderHandlers) failFastIfBusy(w http.ResponseWriter, r *http.Request, scoped context.Context) bool {
	if !database.PoolBusyHit(scoped) {
		return false
	}
	database.NotePoolBusy(r.Context())
	h.writeBusy(w, r)
	return true
}

// localeSet is the public locale set; nil (safe to call) on a single-locale
// install and in a route-table build. The error is set when the URL snapshot
// could not be read: a cached page or a redirect still answers then, but a
// page must not be built from the nil set (see the handlers' miss hook).
// Callers pass a ctx under database.WithPoolBusyScope: a give-up here must not
// turn a cached page into a 503.
func (h *renderHandlers) localeSet(ctx context.Context) (*entity.LocaleSet, error) {
	if h.snapshot == nil {
		return nil, nil
	}
	snap, err := h.snapshot(ctx)
	if errors.Is(err, errNoEntityService) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return snap.Locales(), nil
}

// snapshotDegraded reports that pages rendered now may not match the site's
// locale setup (the snapshot or its locale settings could not be read), so
// they must not be cached.
func (h *renderHandlers) snapshotDegraded(ctx context.Context) bool {
	if h.snapshot == nil {
		return false
	}
	snap, err := h.snapshot(ctx)
	if errors.Is(err, errNoEntityService) {
		return false
	}
	return err != nil || snap.Degraded()
}

// homeRoute wraps a homepage load as a route, the way the public "/" handler
// serves it (shared with the admin preview).
func homeRoute(page *PublicPageResponse, outcome pageOutcome, err error) (*routeResult, pageOutcome, error) {
	if err != nil {
		return nil, outcome, err
	}
	if outcome.NotFound || page == nil {
		return &routeResult{NotFound: true}, pageOutcome{Status: http.StatusNotFound, NotFound: true}, nil
	}
	return &routeResult{Page: page, Kind: resolveKindHome}, outcome, nil
}

// handleRenderCatchAll serves every other public path. API and media paths are
// not pages: they keep the JSON 404 they always had.
func (h *renderHandlers) handleRenderCatchAll(w http.ResponseWriter, r *http.Request) {
	h, done := h.forRequest(w, r)
	if done {
		return
	}
	path := requestPath(r)
	if isReservedPath(path) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	scoped := database.WithPoolBusyScope(r.Context())
	set, lookupErr := h.localeSet(scoped)
	serve := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The resolver reads the snapshot itself; a lookup that already gave up
		// on a busy pool must not wait a second bound for it.
		onMiss := func() bool { return lookupErr != nil && h.failFastIfBusy(w, r, scoped) }
		h.serve(w, r, path, onMiss, func() (*routeResult, pageOutcome, error) {
			return h.resolve(r.Context(), path, r)
		})
	})
	bare := path // the path without its locale segment
	if _, rest, ok := set.Resolve(path); ok {
		bare = rest
	}
	if lookupErr != nil && strings.HasSuffix(path, searchPath) {
		// The locale set is unknown: any ".../search" may be a prefixed search,
		// so it is limited like one.
		bare = searchPath
	}
	if bare == searchPath && r.URL.Query().Get("q") != "" && h.searchLimit != nil {
		h.searchLimit(serve).ServeHTTP(w, r)
		return
	}
	serve(w, r)
}

func isReservedPath(path string) bool {
	for _, p := range []string{"/api", "/media"} {
		if path == p || strings.HasPrefix(path, p+"/") {
			return true
		}
	}
	return false
}

func (h *renderHandlers) redirectIfMatched(w http.ResponseWriter, r *http.Request, path string) bool {
	if h.checkRedirect == nil {
		return false
	}
	rd := h.checkRedirect(r.Context(), path)
	if rd == nil {
		return false
	}
	writeRenderRedirect(w, r, &redirectHit{Target: rd.TargetURL, Status: rd.StatusCode, KeepQuery: rd.Expanded})
	return true
}

// writeRenderRedirect answers with a real HTTP redirect (301 unless the rule
// says 302/307/308).
func writeRenderRedirect(w http.ResponseWriter, r *http.Request, hit *redirectHit) {
	status := hit.Status
	switch status {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
	default:
		status = http.StatusMovedPermanently
	}
	w.Header().Set("Cache-Control", "public, max-age=300")
	target := hit.Target
	if hit.KeepQuery && r.URL.RawQuery != "" {
		sep := "?"
		if strings.Contains(target, "?") {
			sep = "&"
		}
		target += sep + r.URL.RawQuery
	}
	http.Redirect(w, r, target, status)
}

// serve runs the shared flow: cache, onMiss (optional, true = it answered),
// resolve, redirect/404/error, render.
func (h *renderHandlers) serve(w http.ResponseWriter, r *http.Request, path string, onMiss func() bool, load func() (*routeResult, pageOutcome, error)) {
	filtered := h.pageFilter.Applies(path)
	// A plugin's cookie answer (request.redirect) makes this response specific
	// to the request: no cache read or write, private headers.
	uncached := h.uncached || uncachedRequest(r.Context()) || h.commentBypass.applies(r)
	pageN := pagedQuery(r.URL.RawQuery) // 0 unless the query is exactly the canonical ?page=N, N >= 2
	cacheable := h.cache != nil && h.baseURLPinned && h.runtime.Enabled() && (r.URL.RawQuery == "" || pageN > 0) && !filtered && !uncached
	key := ""
	var gen uint64
	var hitGen uint64
	if cacheable {
		// Read before the page itself: a purge after this point rejects the
		// variant store below, so no stale compressed copy outlives it.
		hitGen = h.cache.Generation()
		key = cache.RenderedPageKey(h.engine.Version(), path)
		if pageN > 0 { // read-only until a render proves the page has a paged loop
			key = cache.RenderedPageKey(h.engine.Version(), path+"?page="+strconv.Itoa(pageN))
		}
		// A preload refresh skips the read so the page is re-rendered and overwritten.
		if body := h.cachedBody(r, key); len(body) > 0 {
			h.serveCached(w, r, path, key, body, hitGen)
			return
		}
	}

	if onMiss != nil && onMiss() {
		return
	}
	if cacheable && h.snapshotDegraded(r.Context()) {
		cacheable = false
	}
	// Concurrent misses on one key render once: the first request leads, the
	// rest wait for its stored page. A refresh must re-render and a smoke
	// request judges its own render, so neither joins.
	// A key whose last render was not storable (dynamic, degraded, 404) skips
	// the flight too: its followers would only wait to render themselves.
	var call *flightCall
	leader := false
	flightEligible := cacheable && h.flight != nil && !refreshRequested(r.Context()) && !isSmokeRequest(r.Context())
	// notStorable records that this key's render will not be cached.
	notStorable := func() {
		if flightEligible {
			h.flight.markUnstorable(key)
		}
	}
	if flightEligible && !h.flight.skip(key) {
		call, leader = h.flight.join(key)
		if leader {
			// Covers every exit that does not store: followers then render on their own.
			defer h.flight.finish(key, call, flightResult{})
		} else {
			res, ok := h.flight.wait(r.Context(), call)
			if !ok {
				return // this request's client is gone; the leader is unaffected
			}
			if res.stored {
				h.serveCached(w, r, path, key, res.body, h.cache.Generation())
				return
			}
		}
	}
	if cacheable {
		gen = h.cache.Generation() // before any read of page data
	}
	// Sampled before load(): jsonld.enrich runs inside the loaders.
	pluginFailuresBefore := render.DegradedCount()
	var scope *render.DegradeScope
	if isSmokeRequest(r.Context()) {
		// A smoke request judges its own render only: visitor traffic moves
		// the process-wide counters, never this scope.
		var release func()
		var ctx context.Context
		ctx, scope, release = render.BindDegradeScope(r.Context())
		defer release()
		r = r.WithContext(ctx)
	}
	rr, outcome, err := load()
	if err != nil {
		h.writeFailure(w, r, err)
		return
	}
	if rr.Redirect != nil {
		notStorable()
		writeRenderRedirect(w, r, rr.Redirect)
		return
	}
	if rr.NotFound || outcome.NotFound {
		notStorable()
		h.writeNotFound(w, r, path)
		return
	}

	if rr.Page == nil && rr.Archive == nil && rr.Listing == nil && rr.Registry == nil {
		// A route that resolved without any page data must never become a
		// cached blank 200.
		h.logger.ErrorContext(r.Context(), "render: route resolved without page data", "path", path)
		h.writeError(w, r, http.StatusInternalServerError, nil)
		return
	}

	if rr.Locale != "" {
		r = r.WithContext(entity.WithVisibleLocales(r.Context(), rr.Locale))
	}
	v, err := h.buildView(r.Context(), r, rr)
	var pe *pageError
	if errors.As(err, &pe) && pe.status == http.StatusNotFound {
		notStorable()
		h.writeNotFoundQuiet(w, r) // e.g. ?page past the last page: not a broken link
		return
	}
	if errors.Is(err, database.ErrPoolBusy) {
		h.writeFailure(w, r, err)
		return
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "render: build view", "error", err, "path", path)
		h.writeError(w, r, http.StatusInternalServerError, nil)
		return
	}
	var cookies []sdk.SetCookie
	if filtered {
		cookies = h.applyPageFilter(r, path, v)
	}
	var failuresBefore int64
	if h.dataFailures != nil {
		failuresBefore = h.dataFailures()
	}
	var buf bytes.Buffer
	v.Ctx = r.Context()
	// Keep in sync with the sandboxCSP condition in writeHTML: a page served
	// sandboxed must not carry frames (a sandboxed PDF viewer is blocked).
	v.NoFrames = h.communityScripts != nil && h.communityScripts() // such pages get sandboxCSP
	if err := h.engine.RenderPage(&buf, v); err != nil {
		h.countRenderFailure()
		h.logger.ErrorContext(r.Context(), "render: page", "error", err, "path", path)
		h.writeError(w, r, http.StatusInternalServerError, v)
		return
	}

	// a failed query reports no items, not a missing page: that degrades below instead
	if v.LoopOutOfRange != nil && *v.LoopOutOfRange && (h.dataFailures == nil || h.dataFailures() == failuresBefore) {
		notStorable()
		h.writeNotFoundQuiet(w, r) // a paged loop has no such page
		return
	}

	// A page with a failed section is served, but never cached: the failure
	// may be transient (plugin func, DB blip) and must not stick for the TTL.
	// Sections are executed twice (trial, then page); documented in THEMES.md.
	// A failed Related query or widget lookup (recent, terms, srcset, menu)
	// degrades the page the same way, as does a plugin template func or
	// render-time filter that failed, timed out, overran its output cap or
	// was skipped by a breaker (render.NoteDegraded).
	degraded := bytes.Contains(buf.Bytes(), []byte(render.SectionFailedMarker)) ||
		(v.Uncacheable != nil && *v.Uncacheable) ||
		render.DegradedCount() != pluginFailuresBefore ||
		(h.dataFailures != nil && h.dataFailures() != failuresBefore)
	if scope != nil && (bytes.Contains(buf.Bytes(), []byte(render.SectionFailedMarker)) ||
		(v.Uncacheable != nil && *v.Uncacheable) || scope.Count() > 0) {
		// Counted by the smoke gate; the page itself is still served. Only
		// this request's own signals count, never the shared counters above.
		h.logger.ErrorContext(r.Context(), "render: page degraded", "path", path)
	}
	if database.PoolBusyHit(r.Context()) {
		// A bounded acquire gave up while building the page and a caller
		// rendered without the data (menus, fonts, settings): never serve or
		// cache that as a page.
		h.writeBusy(w, r)
		return
	}
	private := degraded || filtered || uncached
	// storable is the page-cache write condition. The validators use the same
	// one: a response the cache would not keep (preview, dynamic, a page past
	// a paged loop) must not answer 304 either.
	storable := cacheable && !private && !v.Preview && !v.Dynamic && (pageN == 0 || (v.PagedLoop != nil && *v.PagedLoop))
	// Stored before the response is written (in memory, microseconds) so the
	// followers are released without waiting for this client's network write.
	stored := false
	if storable {
		ok, err := h.cache.SetIfGenerationTagged(r.Context(), key, buf.Bytes(), h.pageTTLFor(v), gen, pageCacheTags(v)...)
		if err != nil {
			h.logger.Warn("render: cache set failed", "error", err)
		}
		stored = ok && err == nil
	}
	if flightEligible {
		if stored {
			h.flight.markStorable(key)
		} else if !storable {
			h.flight.markUnstorable(key)
		}
	}
	if leader {
		h.flight.finish(key, call, flightResult{body: buf.Bytes(), stored: stored})
	}
	pluginhost.WriteSetCookies(w, r, cookies)
	// Static mode: the page is rewritten for the static site and carries no
	// comment token, so it is written here, without validators or variants.
	if out, ok := h.staticPage(r, buf.Bytes()); ok {
		h.writeHTML(w, r, http.StatusOK, out, private)
		return
	}
	// The stored and shared bytes stay the template; only this response is
	// personalised (private, no validators, never a 304).
	if out, personal := h.slot.fill(r, buf.Bytes()); personal {
		h.writeHTML(w, r, http.StatusOK, out, true)
		return
	}
	if pv := h.validators; storable && pv != nil {
		// Hash exactly the bytes that get cached, so a later hit agrees.
		sum := fingerprintSum(buf.Bytes())
		now := pv.clock()
		since := pv.stamp(key, sum, now)
		setValidators(w.Header(), sum, since, now)
		if notModified(r, sum, since, now) {
			h.writeNotModified(w, r, buf.Bytes())
		} else {
			h.writeHTML(w, r, http.StatusOK, buf.Bytes(), false)
		}
	} else {
		h.writeHTML(w, r, http.StatusOK, buf.Bytes(), private)
	}
}

// staticPage returns the static-mode body of a public GET/HEAD response; false
// in served mode. It runs after the page cache (the cache keeps the template
// bytes, so a mode switch needs no purge) and before commentSlot.fill (the
// loader replaces the form, so no token is issued here).
func (h *renderHandlers) staticPage(r *http.Request, body []byte) ([]byte, bool) {
	hs := statichost.HostingFrom(r.Context(), h.hosting.Load())
	if !hs.IslandsOn() || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
		return nil, false
	}
	live := h.liveFn
	if live == nil {
		live = func(p string) bool { return staticLivePath(p, "") }
	}
	return staticRewrite{hs: hs, export: statichost.IsExport(r.Context()), live: live}.page(body), true
}

// serveCached answers from a cached page body: a redirect created after the
// page was cached wins, then validators (304) and the encoded variant. hitGen
// is the cache token read before the page itself.
func (h *renderHandlers) serveCached(w http.ResponseWriter, r *http.Request, path, key string, body []byte, hitGen uint64) {
	// The redirect lookup is cheap (cached in the redirect service).
	if h.redirectIfMatched(w, r, path) {
		return
	}
	// Static mode: rewrite for the static site before the comment slot, so no
	// token is ever issued; no validators, never a 304, no stored variant.
	if out, ok := h.staticPage(r, body); ok {
		h.writeHTML(w, r, http.StatusOK, out, false)
		return
	}
	// A page with the comment form is filled per request: private, no validators,
	// no stored variant, never 304. Followers of a flight come through here too.
	if out, ok := h.slot.fill(r, body); ok {
		h.writeHTML(w, r, http.StatusOK, out, true)
		return
	}
	var fp []byte
	if pv := h.validators; pv != nil {
		sum := fingerprintSum(body)
		now := pv.clock()
		since := pv.stamp(key, sum, now)
		setValidators(w.Header(), sum, since, now)
		if notModified(r, sum, since, now) {
			h.writeNotModified(w, r, body)
			return
		}
		var fpBuf [fingerprintLen]byte
		fp = binary.BigEndian.AppendUint64(fpBuf[:0], sum)
	}
	enc, packed := h.encodedVariantFP(r, key, body, fp, hitGen)
	h.writeHTMLEncoded(w, r, http.StatusOK, body, false, enc, packed)
}

// pageTTLFor is the cache lifetime of v's page: the configured TTL, capped by
// the view's own MaxCacheAge (a page with live registry data).
func (h *renderHandlers) pageTTLFor(v *render.PageView) time.Duration {
	ttl := h.pageCacheTTL()
	if v.MaxCacheAge > 0 && v.MaxCacheAge < ttl {
		ttl = v.MaxCacheAge
	}
	return ttl
}

// pageCacheTags: every page may show other entities (recent/query funcs in any
// layout, related, breadcrumbs, alternates, refs), so it always carries
// TagAnyEntity; its own entity tag lets an own-entity drop reach it.
func pageCacheTags(v *render.PageView) []string {
	tags := []string{cache.TagAnyEntity}
	id := v.EntityID
	if id == "" && v.Entity != nil {
		id = v.Entity.ID
	}
	if u, err := uuid.Parse(id); err == nil {
		tags = append(tags, cache.EntityTag(u))
	}
	return tags
}

// pagedQuery is N when rawQuery is exactly "page=N" in canonical decimal form
// with 2 <= N <= render.MaxQueryPage, else 0: the only query a page cache key
// is built for.
func pagedQuery(rawQuery string) int {
	s, ok := strings.CutPrefix(rawQuery, "page=")
	if !ok {
		return 0
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 2 || n > render.MaxQueryPage || strconv.Itoa(n) != s {
		return 0
	}
	return n
}

// cachedBody returns the cached page for key, or nil on a miss or a refresh.
func (h *renderHandlers) cachedBody(r *http.Request, key string) []byte {
	if refreshRequested(r.Context()) {
		return nil
	}
	body, err := h.cache.Get(r.Context(), key)
	if err != nil {
		return nil
	}
	return body
}

func (h *renderHandlers) pageCacheTTL() time.Duration {
	if ttl := h.runtime.TTL(); ttl > 0 {
		return ttl
	}
	if h.cacheTTL > 0 {
		return h.cacheTTL
	}
	return config.DefaultPageCacheTTL
}

func (h *renderHandlers) writeNotFound(w http.ResponseWriter, r *http.Request, path string) {
	if h.log404 != nil && !isPrefetch(r) {
		h.log404(redactPreviewToken(path), r.Referer(), r.UserAgent())
	}
	h.writeNotFoundQuiet(w, r)
}

// writeNotFoundQuiet is writeNotFound without the 404-log entry, for pages that
// exist but are not Go-served (a gate refusal is not a broken link).
func (h *renderHandlers) writeNotFoundQuiet(w http.ResponseWriter, r *http.Request) {
	key, gen, storeOK := h.notFoundKey(r) // gen before any page data is read
	now := time.Now()
	var body []byte
	hit := false
	if storeOK {
		body, hit = h.notFound.get(key, gen, now)
	}
	if !hit {
		// Only a miss pays for this: a stored body was built from a healthy
		// snapshot and stays readable, but a degraded one must not be kept.
		storeOK = storeOK && !h.snapshotDegraded(r.Context())
		before := render.DegradedCount()
		var failuresBefore int64
		if h.dataFailures != nil {
			failuresBefore = h.dataFailures()
		}
		v, built := h.errorViewBuilt(r, &routeResult{NotFound: true})
		var buf bytes.Buffer
		if err := h.engine.RenderNotFound(&buf, v); err != nil {
			h.logger.ErrorContext(r.Context(), "render: 404", "error", err)
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		body = buf.Bytes()
		degraded := bytes.Contains(body, []byte(render.SectionFailedMarker)) ||
			(v.Uncacheable != nil && *v.Uncacheable) ||
			render.DegradedCount() != before ||
			(h.dataFailures != nil && h.dataFailures() != failuresBefore)
		if storeOK && built && !degraded && !v.Preview && !v.Dynamic && !database.PoolBusyHit(r.Context()) {
			h.notFound.put(key, body, gen, now.Add(h.pageTTLFor(v)))
		}
	}
	// Per request and after the store: the store keeps the template bytes.
	if out, ok := h.staticPage(r, body); ok {
		body = out
	}
	h.writeHTML(w, r, http.StatusNotFound, body, true)
}

// notFoundKey is the store key of a 404 for r and the page-cache token to
// check it against; ok is false when this 404 must be rendered afresh and not
// stored (the same rules as a page-cache read, and any query). The snapshot
// state is checked by the caller, on a miss only.
func (h *renderHandlers) notFoundKey(r *http.Request) (key string, gen uint64, ok bool) {
	if h.notFound == nil || h.cache == nil || !h.baseURLPinned || !h.runtime.Enabled() {
		return "", 0, false
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return "", 0, false
	}
	path := requestPath(r)
	ctx := r.Context()
	if r.URL.RawQuery != "" || len(path) > maxNotFoundPath ||
		h.uncached || uncachedRequest(ctx) || h.commentBypass.applies(r) ||
		isSmokeRequest(ctx) || refreshRequested(ctx) || hiddenLocalesServed(ctx) ||
		h.pageFilter.Applies(path) {
		return "", 0, false
	}
	return h.engine.Version() + "\x00" + path, h.cache.Generation(), true
}

// writeFailure maps a loader error to a themed error page.
func (h *renderHandlers) writeFailure(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, database.ErrPoolBusy) {
		h.logger.DebugContext(r.Context(), "render: pool busy", "path", logutil.CapField(r.URL.Path))
		h.writeBusy(w, r)
		return
	}
	var pe *pageError
	if errors.As(err, &pe) {
		if pe.status == http.StatusNotFound {
			h.writeNotFound(w, r, requestPath(r))
			return
		}
		h.writeError(w, r, pe.status, nil)
		return
	}
	h.logger.ErrorContext(r.Context(), "render: load", "error", err, "path", logutil.CapField(r.URL.Path))
	h.writeError(w, r, http.StatusInternalServerError, nil)
}

func (h *renderHandlers) countRenderFailure() {
	if h.renderFailed != nil {
		h.renderFailed()
	}
}

func (h *renderHandlers) writeError(w http.ResponseWriter, r *http.Request, status int, v *render.PageView) {
	if v == nil {
		v = h.errorView(r, &routeResult{NotFound: true})
	}
	v.Ctx = r.Context()
	var buf bytes.Buffer
	if err := h.engine.RenderError(&buf, v, status); err != nil {
		h.countRenderFailure()
		http.Error(w, http.StatusText(status), status)
		return
	}
	body := buf.Bytes()
	if out, ok := h.staticPage(r, body); ok {
		body = out
	}
	h.writeHTML(w, r, status, body, true)
}

// errorView builds the site chrome for an error page; if even that fails the
// page renders without it.
func (h *renderHandlers) errorView(r *http.Request, rr *routeResult) *render.PageView {
	v, _ := h.errorViewBuilt(r, rr)
	return v
}

// errorViewBuilt is errorView that also reports whether the full view was
// built (false: the chrome-less fallback, never stored).
func (h *renderHandlers) errorViewBuilt(r *http.Request, rr *routeResult) (*render.PageView, bool) {
	v, err := h.buildView(r.Context(), r, rr)
	built := err == nil && v != nil
	if !built {
		v = &render.PageView{BaseURL: determineBaseURL(r), Path: requestPath(r)}
	}
	v.Ctx = r.Context()
	return v, built
}

// directoryMarker is how writeHTML recognises a rendered page with a plugin
// directory, for fresh renders and page-cache hits alike. The plugin_directory
// template emits it on its own root, so it is present on the legacy sections
// path and on builder (page_doc) output, which carries no section markers.
var directoryMarker = []byte(`data-hm-directory`)

// writeHTML writes a rendered document with the renderer's headers. private
// marks responses that must not be shared-cached (degraded pages,
// errors).
func (h *renderHandlers) writeHTML(w http.ResponseWriter, r *http.Request, status int, body []byte, private bool) {
	h.writeHTMLEncoded(w, r, status, body, private, httpx.EncodingNone, nil)
}

// writeHTMLEncoded is writeHTML for a body that is also available compressed
// (packed, in coding enc): the headers follow the plain body, the wire bytes
// are the compressed ones. The compression middleware leaves a response that
// already carries a Content-Encoding alone.
func (h *renderHandlers) writeHTMLEncoded(w http.ResponseWriter, r *http.Request, status int, body []byte, private bool, enc httpx.Encoding, packed []byte) {
	hd := w.Header()
	h.setPageHeaders(hd, r, body, private)
	if packed != nil {
		body = packed
		hd.Set("Content-Encoding", string(enc))
		hd.Add("Vary", "Accept-Encoding")
		hd.Set("Content-Length", strconv.Itoa(len(body)))
	}
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

// writeNotModified answers a conditional request for a public page: the 200's
// headers (Cache-Control incl. the directory TTL, CSP) and no body. The
// compression middleware adds no Vary to a bodiless 304, so it is set here to
// match the 200: Accept-Encoding only while compression is on (the 200 gets it
// from the middleware or from a stored variant). A Range request skips the
// middleware, so its 200 lacks the Vary its 304 carries; a spare Vary on a 304
// is harmless.
func (h *renderHandlers) writeNotModified(w http.ResponseWriter, r *http.Request, body []byte) {
	hd := w.Header()
	h.setPageHeaders(hd, r, body, false)
	hd.Del("Content-Type")
	if h.encodeCached != nil && h.encodeCached() {
		hd.Add("Vary", "Accept-Encoding")
	}
	w.WriteHeader(http.StatusNotModified)
}

// setPageHeaders sets the headers that follow the plain body: content type,
// cache policy, CSP and the plugin sandbox.
func (h *renderHandlers) setPageHeaders(hd http.Header, r *http.Request, body []byte, private bool) {
	hd.Set("Content-Type", "text/html; charset=utf-8")
	if private {
		hd.Set("Cache-Control", cachePrivate)
		hd.Set("Vary", "Cookie")
	} else if bytes.Contains(body, directoryMarker) {
		hd.Set("Cache-Control", cachePublicDirectory)
	} else {
		hd.Set("Cache-Control", cachePublic)
	}
	applyRenderSecurityHeaders(hd, h.csp(), extrasOf(r.Context(), h.cspExtras).merge(pluginExtras(h.pluginCSPHosts())))
	// A page is sandboxed when its own body links a community plugin.js, or when
	// some community script is live site-wide. The live check is a REQUIRED
	// guard, not a transition aid: the body marker only sees plugin.js, while
	// head-filter scripts, gated sections and partials, and islands carry no
	// marker. It can go only once the engine emits an explicit marker for every
	// community script path.
	if h.linksCommunityScript(body) || (h.communityScripts != nil && h.communityScripts()) {
		// Its own enforced header: sandbox is ignored in Report-Only, and CSP
		// headers combine (the intersection applies). Set even with
		// SECURITY_HEADERS_DISABLED: it is a safety control, not a hardening extra.
		hd.Add("Content-Security-Policy", sandboxCSP)
	}
}

// pluginScriptSrc is how pages link a plugin script: src="/p/<name>/assets/plugin.js".
var pluginScriptSrc = []byte(`src="/p/`)

const pluginScriptTail = "/assets/plugin.js"

// linksCommunityScript reports whether body links the plugin.js of a
// community-origin plugin.
func (h *renderHandlers) linksCommunityScript(body []byte) bool {
	if h.communityPlugin == nil {
		return false
	}
	for rest := body; ; {
		i := bytes.Index(rest, pluginScriptSrc)
		if i < 0 {
			return false
		}
		rest = rest[i+len(pluginScriptSrc):]
		end := bytes.IndexByte(rest, '"')
		if end < 0 {
			return false
		}
		if name, ok := strings.CutSuffix(string(rest[:end]), pluginScriptTail); ok && !strings.Contains(name, "/") && h.communityPlugin(name) {
			return true
		}
	}
}

// pluginCSPHosts is the set of the enabled plugins' csp hosts; the zero set
// when nothing is set.
func (h *renderHandlers) pluginCSPHosts() pluginhost.CSPSet {
	if h.pluginCSP == nil {
		return pluginhost.CSPSet{}
	}
	return h.pluginCSP()
}

// csp is the public policy: the strict one when the engine's theme opted in.
func (h *renderHandlers) csp() string {
	if s, ok := h.engine.(interface{ StrictScripts() bool }); ok && s.StrictScripts() {
		return renderCSPStrict
	}
	return renderCSP
}

// applyRenderSecurityHeaders overrides the global DENY/frame-ancestors 'none'
// so the admin editor can frame same-origin pages, and widens img/frame
// sources. It only replaces what the global middleware set (it is a no-op for
// the CSP when headers are disabled).
// The CSP (base) gains the extras' origins (captcha, tag vendors, plugins).
func applyRenderSecurityHeaders(hd http.Header, base string, extras cspExtras) {
	hd.Set("X-Frame-Options", "SAMEORIGIN")
	csp := cspWithExtras(base, extras)
	for _, name := range []string{"Content-Security-Policy", "Content-Security-Policy-Report-Only"} {
		if hd.Get(name) != "" {
			hd.Set(name, csp)
		}
	}
}

// captchaProvider is the captcha provider whose origins the CSP should allow:
// "" when none is set up, its secret cannot be read (no widget is rendered, the
// same CaptchaUsable test captchaWidgetFor applies) or the settings cannot be read.
func (s *Server) captchaProvider(ctx context.Context) string {
	if s.spam == nil {
		return ""
	}
	set, err := s.spam.Settings.Get(ctx)
	if err != nil || !s.spam.CaptchaUsable(set) {
		return ""
	}
	return set.Provider
}

var errNoEntityService = errors.New("no entity service")

// urlSnapshotOrErr is the URL snapshot, or an error when the server has no
// entity service (route-table builds, errNoEntityService); callers treat that
// as a one-locale site.
func (s *Server) urlSnapshotOrErr(ctx context.Context) (*entity.URLSnapshot, error) {
	if s.entityService == nil {
		return nil, errNoEntityService
	}
	return s.entityService.URLRegistry().Snapshot(ctx)
}
