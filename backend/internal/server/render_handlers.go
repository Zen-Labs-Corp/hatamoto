package server

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"hatamoto.dev/core/internal/cache"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/middleware"
	"hatamoto.dev/core/internal/pluginhost"
	"hatamoto.dev/core/internal/redirect"
	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/core/internal/spam"
	"hatamoto.dev/sdk"
)

const (
	cachePublic = "public, max-age=60, s-maxage=300"
	// A page with a plugin directory may sit in a shared cache no longer than
	// it sits in the page cache (pluginDirectoryCacheAge).
	cachePublicDirectory = "public, max-age=60, s-maxage=60"
	cachePrivate         = "private, no-cache, no-store"

	// renderCSP replaces the global API policy on HTML responses: pages embed
	// images and iframes from https origins and are framed by the admin
	// preview (same origin) only.
	renderCSP = "default-src 'self'; script-src 'self' 'unsafe-inline'; " +
		"style-src 'self' 'unsafe-inline'; img-src 'self' data: https:; " +
		"font-src 'self'; connect-src 'self'; frame-src https:; frame-ancestors 'self'; " +
		"object-src 'none'; base-uri 'self'; form-action 'self'"

	// sandboxCSP runs a page that carries community scripts in an opaque
	// origin: no allow-same-origin, so its JS has no cookies or storage of the
	// site and cannot read same-origin responses.
	sandboxCSP = "sandbox allow-scripts allow-forms allow-popups " +
		"allow-popups-to-escape-sandbox allow-top-navigation-by-user-activation"

	// renderCSPStrict is renderCSP without 'unsafe-inline' in script-src, for
	// themes that opt in (theme.json csp.strictScripts). No nonce: public HTML
	// is cached and shared, so a per-request nonce would go stale.
	renderCSPStrict = "default-src 'self'; script-src 'self'; " +
		"style-src 'self' 'unsafe-inline'; img-src 'self' data: https:; " +
		"font-src 'self'; connect-src 'self'; frame-src https:; frame-ancestors 'self'; " +
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
}

// renderHandlers serves the public site from the Go theme engine. Every
// collaborator is a field so the flow is testable without a database; the
// constructor wires the real ones. Mounting (GET /, GET /*) is done by the
// server router.
type renderHandlers struct {
	engine pageRenderer
	cache  htmlCache
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

	log404        func(path, referer, userAgent string)
	checkRedirect func(ctx context.Context, path string) *redirect.Redirect
	loadHome      func(r *http.Request) (*PublicPageResponse, pageOutcome, error)
	// snapshot is the URL registry snapshot; it carries the public locale set.
	// Nil: a single-locale install.
	snapshot  func(ctx context.Context) (*entity.URLSnapshot, error)
	resolve   func(ctx context.Context, path string, r *http.Request) (*routeResult, pageOutcome, error)
	buildView func(ctx context.Context, r *http.Request, rr *routeResult) (*render.PageView, error)
	// pageFilter (optional) is the render.page plugin chain. While a plugin
	// handles it, pages depend on the request and are never shared-cached.
	pageFilter *pluginhost.PageFilter
	// preview (optional) renders with another installed theme for a signed-in
	// owner or editor; such responses are never cached (uncached).
	preview  *themePreview
	uncached bool
	// runtime carries the live cache on/off and lifetime; nil means enabled with cacheTTL.
	runtime *pageCacheRuntime
	// captchaProvider names the configured captcha provider ("" or "none" for
	// none). It is read on every response, cache hits included, so the CSP
	// follows the settings even for pages cached before they changed.
	captchaProvider func(ctx context.Context) string
	// communityScripts reports a live community site.scripts; nil means none.
	// It is read on every response, cache hits included.
	communityScripts func() bool
	// communityPlugin reports whether a plugin is community-origin; nil means
	// none. It decides the per-page sandbox from the script tags in the body.
	communityPlugin func(name string) bool
}

// newRenderHandlers wires the handlers to the server's services. pageCache may
// be nil to run uncached.
func (s *Server) newRenderHandlers(engine pageRenderer, pageCache *cache.Cache) *renderHandlers {
	h := &renderHandlers{
		engine: engine,
		logger: s.logger,
		// Without PUBLIC_BASE_URL the canonical URLs come from the request
		// Host, so such pages must never be cached (see renderHandlers).
		baseURLPinned: s.cfg != nil && strings.TrimSpace(s.cfg.PublicSite.BaseURL) != "",
		loadHome:      func(r *http.Request) (*PublicPageResponse, pageOutcome, error) { return s.loadHomepage(r) },
		snapshot:      s.urlSnapshotOrErr,
		resolve:       s.resolveForRender,
		buildView:     s.buildPageView,
		searchLimit:   middleware.NewRateLimiter(2, 30).Middleware,
		pageFilter:    pluginhost.NewPageFilter(s.hooks),
		preview:       s.preview,
		runtime:       s.pageRuntime,

		captchaProvider:  s.captchaProvider,
		communityScripts: func() bool { return qaSandboxForced() || s.communityScriptsLive() },
		communityPlugin:  s.communityPlugin,
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
	if set := h.localeSet(r.Context()); set.Multi() {
		if prefix := set.Prefix(set.Default()); prefix != "" {
			w.Header().Set("Cache-Control", "no-store")
			target := prefix
			if r.URL.RawQuery != "" {
				target += "?" + r.URL.RawQuery
			}
			http.Redirect(w, r, target, http.StatusFound)
			return
		}
	}
	h.serve(w, r, "/", func() (*routeResult, pageOutcome, error) {
		return homeRoute(h.loadHome(r))
	})
}

// localeSet is the public locale set; nil (safe to call) on a single-locale
// install or when the snapshot is unavailable.
func (h *renderHandlers) localeSet(ctx context.Context) *entity.LocaleSet {
	if h.snapshot == nil {
		return nil
	}
	snap, err := h.snapshot(ctx)
	if err != nil {
		return nil
	}
	return snap.Locales()
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
	serve := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.serve(w, r, path, func() (*routeResult, pageOutcome, error) {
			return h.resolve(r.Context(), path, r)
		})
	})
	bare := path // the path without its locale segment
	if _, rest, ok := h.localeSet(r.Context()).Resolve(path); ok {
		bare = rest
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

// serve runs the shared flow: cache, resolve, redirect/404/error, render.
func (h *renderHandlers) serve(w http.ResponseWriter, r *http.Request, path string, load func() (*routeResult, pageOutcome, error)) {
	filtered := h.pageFilter.Active()
	pageN := pagedQuery(r.URL.RawQuery) // 0 unless the query is exactly the canonical ?page=N, N >= 2
	cacheable := h.cache != nil && h.baseURLPinned && h.runtime.Enabled() && (r.URL.RawQuery == "" || pageN > 0) && !filtered && !h.uncached
	key := ""
	var gen uint64
	if cacheable {
		key = cache.RenderedPageKey(h.engine.Version(), path)
		if pageN > 0 { // read-only until a render proves the page has a paged loop
			key = cache.RenderedPageKey(h.engine.Version(), path+"?page="+strconv.Itoa(pageN))
		}
		// A preload refresh skips the read so the page is re-rendered and overwritten.
		if body := h.cachedBody(r, key); len(body) > 0 {
			// A redirect created after the page was cached must win over the
			// cached copy; the lookup is cheap (cached in the redirect service).
			if h.redirectIfMatched(w, r, path) {
				return
			}
			h.writeHTML(w, r, http.StatusOK, body, false)
			return
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
		writeRenderRedirect(w, r, rr.Redirect)
		return
	}
	if rr.NotFound || outcome.NotFound {
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
		h.writeNotFoundQuiet(w, r) // e.g. ?page past the last page: not a broken link
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
	if err := h.engine.RenderPage(&buf, v); err != nil {
		h.logger.ErrorContext(r.Context(), "render: page", "error", err, "path", path)
		h.writeError(w, r, http.StatusInternalServerError, v)
		return
	}

	// a failed query reports no items, not a missing page: that degrades below instead
	if v.LoopOutOfRange != nil && *v.LoopOutOfRange && (h.dataFailures == nil || h.dataFailures() == failuresBefore) {
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
	private := degraded || filtered || h.uncached
	setPluginCookies(w, r, cookies)
	h.writeHTML(w, r, http.StatusOK, buf.Bytes(), private)
	if cacheable && !private && !v.Preview && !v.Dynamic && (pageN == 0 || (v.PagedLoop != nil && *v.PagedLoop)) {
		if _, err := h.cache.SetIfGeneration(r.Context(), key, buf.Bytes(), h.pageTTLFor(v), gen); err != nil {
			h.logger.Warn("render: cache set failed", "error", err)
		}
	}
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
	if h.log404 != nil {
		h.log404(path, r.Referer(), r.UserAgent())
	}
	h.writeNotFoundQuiet(w, r)
}

// writeNotFoundQuiet is writeNotFound without the 404-log entry, for pages that
// exist but are not Go-served (a gate refusal is not a broken link).
func (h *renderHandlers) writeNotFoundQuiet(w http.ResponseWriter, r *http.Request) {
	v := h.errorView(r, &routeResult{NotFound: true})
	var buf bytes.Buffer
	if err := h.engine.RenderNotFound(&buf, v); err != nil {
		h.logger.ErrorContext(r.Context(), "render: 404", "error", err)
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	h.writeHTML(w, r, http.StatusNotFound, buf.Bytes(), true)
}

// writeFailure maps a loader error to a themed error page.
func (h *renderHandlers) writeFailure(w http.ResponseWriter, r *http.Request, err error) {
	var pe *pageError
	if errors.As(err, &pe) {
		if pe.status == http.StatusNotFound {
			h.writeNotFound(w, r, requestPath(r))
			return
		}
		h.writeError(w, r, pe.status, nil)
		return
	}
	h.logger.ErrorContext(r.Context(), "render: load", "error", err, "path", r.URL.Path)
	h.writeError(w, r, http.StatusInternalServerError, nil)
}

func (h *renderHandlers) writeError(w http.ResponseWriter, r *http.Request, status int, v *render.PageView) {
	if v == nil {
		v = h.errorView(r, &routeResult{NotFound: true})
	}
	v.Ctx = r.Context()
	var buf bytes.Buffer
	if err := h.engine.RenderError(&buf, v, status); err != nil {
		http.Error(w, http.StatusText(status), status)
		return
	}
	h.writeHTML(w, r, status, buf.Bytes(), true)
}

// errorView builds the site chrome for an error page; if even that fails the
// page renders without it.
func (h *renderHandlers) errorView(r *http.Request, rr *routeResult) *render.PageView {
	v, err := h.buildView(r.Context(), r, rr)
	if err != nil || v == nil {
		v = &render.PageView{BaseURL: determineBaseURL(r), Path: requestPath(r)}
	}
	v.Ctx = r.Context()
	return v
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
	hd := w.Header()
	hd.Set("Content-Type", "text/html; charset=utf-8")
	if private {
		hd.Set("Cache-Control", cachePrivate)
		hd.Set("Vary", "Cookie")
	} else if bytes.Contains(body, directoryMarker) {
		hd.Set("Cache-Control", cachePublicDirectory)
	} else {
		hd.Set("Cache-Control", cachePublic)
	}
	applyRenderSecurityHeaders(hd, h.csp(), providerOf(r.Context(), h.captchaProvider))
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
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
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
// The CSP (base) gains the configured captcha provider's origins.
func applyRenderSecurityHeaders(hd http.Header, base, provider string) {
	hd.Set("X-Frame-Options", "SAMEORIGIN")
	csp := cspWithProvider(base, provider)
	for _, name := range []string{"Content-Security-Policy", "Content-Security-Policy-Report-Only"} {
		if hd.Get(name) != "" {
			hd.Set(name, csp)
		}
	}
}

// captchaOrigins are the origins a provider's script and verification traffic
// need beyond 'self'. Its iframes are already covered by frame-src https:.
var captchaOrigins = map[string]string{
	spam.ProviderTurnstile: "https://challenges.cloudflare.com",
	spam.ProviderRecaptcha: "https://www.google.com https://www.gstatic.com",
	spam.ProviderHCaptcha:  "https://hcaptcha.com https://*.hcaptcha.com",
}

// cspWithProvider adds the provider's origins to script-src and connect-src of
// base. No provider (or an unknown one) returns base unchanged.
func cspWithProvider(base, provider string) string {
	origins := captchaOrigins[provider]
	if origins == "" {
		return base
	}
	if strings.Contains(base, "script-src 'self' 'unsafe-inline'") {
		base = strings.Replace(base, "script-src 'self' 'unsafe-inline'", "script-src 'self' 'unsafe-inline' "+origins, 1)
	} else {
		base = strings.Replace(base, "script-src 'self'", "script-src 'self' "+origins, 1)
	}
	return strings.Replace(base, "connect-src 'self'", "connect-src 'self' "+origins, 1)
}

// providerOf reads the provider through fn; nil fn means none.
func providerOf(ctx context.Context, fn func(context.Context) string) string {
	if fn == nil {
		return ""
	}
	return fn(ctx)
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

// urlSnapshotOrErr is the URL snapshot, or an error when the server has no
// entity service (route-table builds); callers treat that as a one-locale site.
func (s *Server) urlSnapshotOrErr(ctx context.Context) (*entity.URLSnapshot, error) {
	if s.entityService == nil {
		return nil, errors.New("no entity service")
	}
	return s.entityService.URLRegistry().Snapshot(ctx)
}
