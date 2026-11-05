package server

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/middleware"
	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/core/internal/session"
	"hatamoto.dev/core/internal/siteaccess"
	"hatamoto.dev/core/internal/siteconfig"
)

// siteAccessMaxForm bounds the password form body.
const siteAccessMaxForm = 8 << 10

// siteAccessGate holds a visitor back from the public site while the owner has
// chosen coming soon, maintenance or a password. It wraps only the public
// branch of the dispatcher, so it runs before the mux and the page cache: a
// held-back visitor never reads a cached page, and no gate response is ever
// cached. Mode open costs one settings read.
type siteAccessGate struct {
	settings func(ctx context.Context) siteaccess.Settings
	// signedIn reports a valid admin session cookie.
	signedIn func(r *http.Request) bool
	// verify checks a password against the stored hash.
	verify func(ctx context.Context, password, hash string) bool
	// pages renders the notices; it is read per request because the render
	// handlers are built after the dispatcher.
	pages func() *renderHandlers
	// apiPrefix is the extra public API mount (PUBLIC_API_PREFIX), "" if none.
	apiPrefix string
	key       []byte
	limiter   *middleware.RateLimiter
	now       func() time.Time
}

// siteAccessKey derives the cookie key from the server secret, so it never
// shares a MAC domain with another feature.
func siteAccessKey(secret string) []byte {
	return previewKeyFor(secret, "hatamoto/site-access")
}

func (s *Server) siteAccessGate(next http.Handler) http.Handler {
	g := &siteAccessGate{
		settings: s.siteAccessSettings,
		signedIn: s.siteAccessSignedIn,
		verify: func(ctx context.Context, password, hash string) bool {
			ok, err := auth.VerifyPasswordContext(ctx, password, hash)
			return err == nil && ok
		},
		pages:   func() *renderHandlers { return s.renderHandlers },
		limiter: middleware.NewRateLimiter(0.2, 5),
		now:     time.Now,
	}
	if s.cfg != nil {
		g.apiPrefix = s.cfg.PublicSite.APIPrefix
		g.key = siteAccessKey(s.cfg.Secret)
	}
	return g.wrap(next)
}

// siteAccessSettings reads the gate settings from the hot site-config cache.
// A failed read must not open the gate: it keeps the last settings read, and
// with none yet it holds visitors back as under maintenance. Only a site with
// no config row at all counts as open.
func (s *Server) siteAccessSettings(ctx context.Context) siteaccess.Settings {
	read := s.accessSettings
	if read == nil {
		if s.siteConfig == nil {
			return siteaccess.Settings{Mode: siteaccess.ModeOpen}
		}
		read = func(ctx context.Context) (siteaccess.Settings, error) {
			c, err := s.siteConfig.Get(ctx)
			if err != nil || c == nil {
				return siteaccess.Settings{}, err
			}
			return c.SiteAccess, nil
		}
	}
	set, err := read(ctx)
	switch {
	case err == nil:
		s.lastAccess.Store(&set)
		return set
	case errors.Is(err, siteconfig.ErrNotConfigured):
		return siteaccess.Settings{Mode: siteaccess.ModeOpen}
	}
	if last := s.lastAccess.Load(); last != nil {
		return *last
	}
	return siteaccess.Settings{Mode: siteaccess.ModeMaintenance}
}

func (s *Server) siteAccessSignedIn(r *http.Request) bool {
	if s.sessionStore == nil {
		return false
	}
	c, err := r.Cookie(auth.SessionCookieName)
	if err != nil || c.Value == "" {
		return false
	}
	sess, err := s.sessionStore.Validate(r.Context(), c.Value)
	return err == nil && sess.Kind == session.KindAdmin && sess.UserID != nil
}

func (g *siteAccessGate) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		set := g.settings(r.Context())
		if !set.On() {
			next.ServeHTTP(w, r)
			return
		}
		path := r.URL.Path
		if g.agentLogs(path) || (!g.publicAPI(path) && siteaccess.Bypass(path)) {
			next.ServeHTTP(w, r)
			return
		}
		if path == "/robots.txt" && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
			writeGateText(w, r, http.StatusOK, "User-agent: *\nDisallow: /\n", 0)
			return
		}
		if path == siteaccess.SubmitPath && r.Method == http.MethodPost && set.Mode == siteaccess.ModePassword {
			g.submit(w, r, set)
			return
		}
		if g.signedIn != nil && g.signedIn(r) {
			next.ServeHTTP(&noStoreWriter{ResponseWriter: w}, r)
			return
		}
		if set.Mode == siteaccess.ModePassword && g.hasCookie(r, set) {
			next.ServeHTTP(&noStoreWriter{ResponseWriter: w}, r)
			return
		}
		g.refuse(w, r, set)
	})
}

func (g *siteAccessGate) hasCookie(r *http.Request, set siteaccess.Settings) bool {
	c, err := r.Cookie(siteaccess.CookieName)
	return err == nil && siteaccess.Verify(g.key, c.Value, set.Epoch, g.now())
}

// publicAPI reports a path of the anonymous public API (/api/v1/public and the
// PUBLIC_API_PREFIX mount): it serves published content, so it follows the mode.
func (g *siteAccessGate) publicAPI(p string) bool {
	return g.apiPrefix != "" && (p == g.apiPrefix || strings.HasPrefix(p, g.apiPrefix+"/"))
}

// agentLogs reports the access-log ingest route, which has its own secret and
// stays open at /api/v1/public and under the public API prefix.
func (g *siteAccessGate) agentLogs(p string) bool {
	return p == "/api/v1/public/agent-logs" || (g.apiPrefix != "" && p == g.apiPrefix+"/agent-logs")
}

// machinePath reports the paths that get no themed notice: sitemaps, feeds,
// llms.txt and the whole public API.
func (g *siteAccessGate) machinePath(p string) bool {
	return feedPath(p) || g.publicAPI(p) || p == "/api" || strings.HasPrefix(p, "/api/")
}

// feedPath reports the sitemaps, feeds, llms.txt and the ads.txt files at the site root.
func feedPath(p string) bool {
	switch p {
	case "/sitemap.xml", "/sitemap.xsl", "/feed.xml", "/atom.xml", "/llms.txt", "/ads.txt", "/app-ads.txt":
		return true
	}
	return strings.HasPrefix(p, "/sitemap-")
}

func retryAfter(set siteaccess.Settings) int {
	if siteaccess.ValidRetry(set.RetryAfter) {
		return set.RetryAfter
	}
	return siteaccess.DefaultRetryAfter
}

// refuse answers a held-back visitor for the current mode.
func (g *siteAccessGate) refuse(w http.ResponseWriter, r *http.Request, set siteaccess.Settings) {
	if g.machinePath(r.URL.Path) {
		if set.Mode == siteaccess.ModeMaintenance {
			writeGateText(w, r, http.StatusServiceUnavailable, "Service unavailable\n", retryAfter(set))
		} else {
			writeGateText(w, r, http.StatusNotFound, "Not found\n", 0)
		}
		return
	}
	a := &render.AccessView{Mode: string(set.Mode), Heading: set.Heading, Message: set.Message}
	status, retry := http.StatusOK, 0
	switch set.Mode {
	case siteaccess.ModeMaintenance:
		status, retry = http.StatusServiceUnavailable, retryAfter(set)
	case siteaccess.ModePassword:
		status = http.StatusUnauthorized
		a.Action = siteaccess.SubmitPath
		a.Next = siteaccess.SafeNext(r.URL.RequestURI())
	}
	g.writePage(w, r, status, a, retry)
}

// submit handles the password form: rate limited per client, then the
// password is checked and a signed cookie is set.
func (g *siteAccessGate) submit(w http.ResponseWriter, r *http.Request, set siteaccess.Settings) {
	if g.limiter != nil && !g.limiter.Allow(middleware.IPBucket(middleware.GetClientIP(r))) {
		writeGateText(w, r, http.StatusTooManyRequests, "Too many attempts\n", 5)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, siteAccessMaxForm)
	if err := r.ParseForm(); err != nil {
		writeGateText(w, r, http.StatusBadRequest, "Bad request\n", 0)
		return
	}
	next := siteaccess.SafeNext(r.PostForm.Get("next"))
	pw := r.PostForm.Get("password")
	if pw == "" || set.PasswordHash == "" || !g.verify(r.Context(), pw, set.PasswordHash) {
		g.writePage(w, r, http.StatusUnauthorized, &render.AccessView{
			Mode: string(siteaccess.ModePassword), Heading: set.Heading, Message: set.Message,
			Action: siteaccess.SubmitPath, Next: next, Error: "That password is not right. Try again.", HasError: true,
		}, 0)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     siteaccess.CookieName,
		Value:    siteaccess.Sign(g.key, set.Epoch, g.now()),
		Path:     "/",
		MaxAge:   int(siteaccess.CookieTTL / time.Second),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   strings.HasPrefix(determineBaseURL(r), "https:"),
	})
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (g *siteAccessGate) writePage(w http.ResponseWriter, r *http.Request, status int, a *render.AccessView, retry int) {
	var h *renderHandlers
	if g.pages != nil {
		h = g.pages()
	}
	if h == nil {
		writeGateText(w, r, status, http.StatusText(status)+"\n", retry)
		return
	}
	h.writeAccess(w, r, status, a, retry)
}

// writeGateText answers with a plain body, never cached and never indexed.
func writeGateText(w http.ResponseWriter, r *http.Request, status int, body string, retry int) {
	hd := w.Header()
	hd.Set("Content-Type", "text/plain; charset=utf-8")
	hd.Set("Cache-Control", "no-store")
	hd.Set("X-Robots-Tag", "noindex")
	if retry > 0 {
		hd.Set("Retry-After", strconv.Itoa(retry))
	}
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = io.WriteString(w, body)
	}
}

// accessRenderer is the part of *render.Engine that draws the notices.
type accessRenderer interface {
	RenderAccess(w io.Writer, v *render.PageView, status int) error
}

// writeAccess renders a site access notice with the themed chrome. It is
// never cached and never indexed, and it gets the same security headers as a
// page.
func (h *renderHandlers) writeAccess(w http.ResponseWriter, r *http.Request, status int, a *render.AccessView, retry int) {
	ar, ok := h.engine.(accessRenderer)
	if !ok {
		writeGateText(w, r, status, http.StatusText(status)+"\n", retry)
		return
	}
	v := h.errorView(r, &routeResult{NotFound: true})
	v.Access = a
	var buf bytes.Buffer
	if err := ar.RenderAccess(&buf, v, status); err != nil {
		h.logger.ErrorContext(r.Context(), "render: site access", "error", err)
		writeGateText(w, r, status, http.StatusText(status)+"\n", retry)
		return
	}
	hd := w.Header()
	hd.Set("Content-Type", "text/html; charset=utf-8")
	hd.Set("Cache-Control", "no-store")
	hd.Set("X-Robots-Tag", "noindex")
	if retry > 0 {
		hd.Set("Retry-After", strconv.Itoa(retry))
	}
	applyRenderSecurityHeaders(hd, h.csp(), cspExtras{})
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = w.Write(buf.Bytes())
	}
}

// noStoreWriter marks what a let-through visitor receives as private and
// uncacheable, so a CDN never keeps the real page while a mode is on. The
// header is set just before the status line, after the handler chose its own.
type noStoreWriter struct {
	http.ResponseWriter
	wrote bool
}

func (n *noStoreWriter) WriteHeader(code int) {
	if !n.wrote {
		n.wrote = true
		if code >= 200 && code != http.StatusNotModified {
			n.Header().Set("Cache-Control", "private, no-store")
		}
	}
	n.ResponseWriter.WriteHeader(code)
}

func (n *noStoreWriter) Write(b []byte) (int, error) {
	if !n.wrote {
		n.WriteHeader(http.StatusOK)
	}
	return n.ResponseWriter.Write(b)
}

func (n *noStoreWriter) Unwrap() http.ResponseWriter { return n.ResponseWriter }
