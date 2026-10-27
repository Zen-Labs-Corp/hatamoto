package server

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/middleware"
	"hatamoto.dev/core/internal/session"
)

// maxInviteFormBytes bounds the body read to find the invite token of a
// signed-out POST to the accept page.
const maxInviteFormBytes = 16 << 10

// interceptDeps are the inputs of interceptMiddleware; every one is a function
// so the middleware is testable without a database.
type interceptDeps struct {
	prefix      func() string                                // the resolved admin address
	signedIn    func(r *http.Request) bool                   // a valid admin session cookie
	hasOwner    func(ctx context.Context) bool               // an owner account exists
	inviteValid func(ctx context.Context, token string) bool // a live invite token
	refuse      http.HandlerFunc                             // the standard 404
	throttle    *nfThrottle                                  // nil or disabled: no throttling
	probes      *probeCounter                                // nil: probes are not counted
	// unidentified, when set, is called for a request whose client cannot be
	// told apart (see internalAddress), except a plain local request (see
	// plainLoopback); it must be cheap and idempotent.
	unidentified func()
	// oldPrefixes lists the addresses an address change retired since this
	// process started; nil means none.
	oldPrefixes func() []string
}

// interceptMiddleware answers scanner probes and every signed-out request that
// the admin address does not allow with the standard 404, before the mux sees
// it (so a mux redirect such as /admin to /admin/ cannot leak). Everything
// else passes through untouched.
//
// The default address '/admin' is exempt from the signed-out rules: every
// request at or under it passes straight through, so upgraded installs keep
// working. Probes elsewhere and the old-address refusal still apply.
//
// Throttling: every final 404, whoever wrote it, charges the client's bucket
// (a sweep for the address is made of ordinary public 404s). A blocked client
// gets the standard 404 for everything under the admin address and for probes;
// public pages are still served.
func interceptMiddleware(d interceptDeps) httpx.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// The mux answers a GET or HEAD for an unclean path with a redirect
			// to the clean one, whether or not anything is routed there. Refusing
			// it here would answer 404 where the public site answers 307, which
			// tells a right guess from a wrong one (and costs the sweeper
			// nothing, as a redirect is never charged). Hand it to the mux; the
			// follow-up request is classified normally.
			//
			// Not when the path cleans to something the admin serves: the mux
			// would first try its trailing-slash redirect (/<addr>/_ to
			// /<addr>/_/), which a wrong guess never gets. There the clean
			// path redirect is written here, as the mux writes it for a path
			// with no subtree match, so the answer is the public one.
			if (r.Method == http.MethodGet || r.Method == http.MethodHead) && muxRedirectsUnclean(r) {
				if cleaned, ok := d.cleanedAdminClass(r); ok {
					http.RedirectHandler(cleaned.String(), http.StatusTemporaryRedirect).ServeHTTP(w, r)
					return
				}
				next.ServeHTTP(w, r)
				return
			}
			key := ""
			plain := w // the hint page below must not charge the throttle
			if d.throttle.enabled() {
				if ip := middleware.GetClientIP(r); internalAddress(ip) {
					if d.unidentified != nil && !plainLoopback(ip, r) {
						d.unidentified()
					}
				} else {
					key = middleware.IPBucket(ip)
					w = &nfChargeWriter{ResponseWriter: w, charge: func() { d.throttle.charge(key) }}
				}
			}
			prefix := d.prefix()
			p := r.URL.Path
			// One session lookup per request, and only when a rule needs it.
			var signed, looked bool
			signedIn := func() bool {
				if !looked {
					signed, looked = d.signedIn(r), true
				}
				return signed
			}

			// The configured address is classified first: an address that a
			// scanner list also names (/administrator, /manager) must still
			// reach the admin rules, never be refused as a probe.
			if !underPrefix(p, prefix) {
				if name, ok := wellKnownName(p); ok {
					d.countProbe(name)
					d.refuse(w, r)
					return
				}
				if d.oldAddress(p, prefix) {
					// Someone signed in who follows an old bookmark learns that the
					// address changed; everyone else gets the standard 404.
					if signedIn() {
						writeAddressChangedHint(plain)
						return
					}
					d.countProbe("admin-old")
					d.refuse(w, r)
					return
				}
				if legacyAddress(p, prefix) {
					d.countProbe("admin-old")
					d.refuse(w, r)
					return
				}
				next.ServeHTTP(w, r)
				return
			}
			// The default address keeps upgraded installs working as before:
			// everything at or under /admin reaches the admin untouched, signed in
			// or out, blocked or not (the admin's own auth applies). The strict
			// rules below are for a chosen address.
			if prefix == defaultAdminPrefix {
				next.ServeHTTP(w, r)
				return
			}
			// A blocked client is refused, unless it holds a valid admin session
			// (a sweeper has none; an owner whose page has broken images must
			// not lock themselves out).
			if key != "" && d.throttle.blocked(key) && !signedIn() {
				d.refuse(w, r)
				return
			}
			// The mux splits the escaped path, so an encoded slash makes one
			// segment of what the checks below read as several (_%2fx is
			// /_/x here, one segment to the mux, which sends it to the
			// admin fallback). Signed out, refuse it.
			if hasEncodedSlash(r.URL.EscapedPath()) && !signedIn() {
				d.refuse(w, r)
				return
			}
			rel := strings.TrimPrefix(p, prefix)
			if alwaysAllowed(r, rel) || signedIn() || d.allowedSignedOut(r, rel) {
				next.ServeHTTP(w, r)
				return
			}
			d.refuse(w, r)
		})
	}
}

// muxRedirectsUnclean says whether net/http's ServeMux will answer r with a
// redirect to a cleaned path. The mux cleans r.URL.EscapedPath(), not the
// decoded path, so /a/%2e%2e/x is clean to it (it routes the request as is)
// even though the decoded path has a dot segment. Only a request the mux will
// redirect may skip classification; anything else is classified on the
// decoded path.
func muxRedirectsUnclean(r *http.Request) bool {
	if r.Method == http.MethodConnect {
		return false // the mux does not canonicalize CONNECT
	}
	p := r.URL.EscapedPath()
	return muxCleanPath(p) != p
}

// hasEncodedSlash reports a %2f or %2F in an escaped path.
func hasEncodedSlash(escaped string) bool {
	return strings.Contains(strings.ToLower(escaped), "%2f")
}

// cleanedAdminClass returns the URL of the mux's clean-path redirect for r
// when the decoded cleaned path is at or under the admin address, the old
// default address, or a well-known probe path: the classes this middleware
// would answer itself were the path clean.
func (d interceptDeps) cleanedAdminClass(r *http.Request) (*url.URL, bool) {
	escaped := muxCleanPath(r.URL.EscapedPath())
	decoded, err := url.PathUnescape(escaped)
	if err != nil {
		return nil, false
	}
	prefix := d.prefix()
	_, probe := wellKnownName(decoded)
	if (!underPrefix(decoded, prefix) || prefix == defaultAdminPrefix) && !probe && !d.oldAddress(decoded, prefix) && !legacyAddress(decoded, prefix) {
		return nil, false
	}
	// The same URL as net/http's urlFromEscaped.
	return &url.URL{Path: decoded, RawPath: escaped, RawQuery: r.URL.RawQuery}, true
}

// muxCleanPath is net/http's cleanPath: path.Clean, keeping a single trailing
// slash, with a leading slash added.
func muxCleanPath(p string) string {
	if p == "" {
		return "/"
	}
	if p[0] != '/' {
		p = "/" + p
	}
	np := path.Clean(p)
	if p[len(p)-1] == '/' && np != "/" {
		np += "/"
	}
	return np
}

// isCleanPath reports whether the mux leaves p as it is.
func isCleanPath(p string) bool { return muxCleanPath(p) == p }

// legacyAddress says whether p is at or under the default address while
// another one is live. Everyone gets the standard 404 there, as always.
func legacyAddress(p, live string) bool {
	return live != defaultAdminPrefix && underPrefix(p, defaultAdminPrefix)
}

// oldAddress says whether p is at or under an address that an address change
// in this process retired. The live address is never old.
func (d interceptDeps) oldAddress(p, live string) bool {
	if d.oldPrefixes != nil {
		for _, o := range d.oldPrefixes() {
			if o != live && underPrefix(p, o) {
				return true
			}
		}
	}
	return false
}

// addressChangedHint is the page a signed-in admin gets at an old address. It
// is self-contained (no admin templates or assets) and says nothing about the
// new address, which is in the mail the owners got.
const addressChangedHint = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex"><title>Admin address changed</title></head>
<body style="font:16px/1.5 system-ui,sans-serif;max-width:32rem;margin:4rem auto;padding:0 1rem">
<p>The admin address changed; check your email.</p>
</body></html>
`

// writeAddressChangedHint answers 404 with the hint. It writes to the raw
// writer so the not-found throttle does not charge a signed-in admin.
func writeAddressChangedHint(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNotFound)
	_, _ = io.WriteString(w, addressChangedHint)
}

func (d interceptDeps) countProbe(name string) {
	if d.probes != nil {
		d.probes.hit(name)
	}
}

// underPrefix reports whether p is the prefix itself or below it.
func underPrefix(p, prefix string) bool {
	return p == prefix || strings.HasPrefix(p, prefix+"/")
}

// alwaysAllowed says whether the request reaches the admin without a session
// check: the pages that must answer signed out. rel is the path below the
// address ("" for the address itself). It is a cheap string test, so these
// requests cost no session lookup.
func alwaysAllowed(r *http.Request, rel string) bool {
	switch rel {
	case "", "/", "/login", "/forgot", "/reset":
		// The bare address redirects to the login page, so the URL the CLI
		// prints works; it reveals nothing beyond <address>/login answering.
		return true
	}
	if isPreviewFramePath(rel) {
		return r.Method == http.MethodGet || r.Method == http.MethodHead
	}
	// Static assets; a dot segment is refused (the mux would clean it into a
	// different path than the one checked here).
	return strings.HasPrefix(rel, "/_/") && path.Clean(rel) == rel
}

// allowedSignedOut says whether a signed-out request to a state-dependent page
// may reach the admin: /accept with a live invite, /setup before an owner.
func (d interceptDeps) allowedSignedOut(r *http.Request, rel string) bool {
	switch rel {
	case "/accept":
		return d.inviteValid(r.Context(), inviteToken(r))
	case "/setup":
		return !d.hasOwner(r.Context())
	}
	return false
}

// previewTokenLen is the length of the editor preview token: 24 random bytes
// as unpadded base64url.
const previewTokenLen = 32

// isPreviewFramePath matches exactly /entities/{uuid}/edit/preview/{token}, the
// admin's deliberately public, cookieless route for the sandboxed editor
// preview frame (the token is its credential). No other shape is accepted, so
// no dot segment or extra segment gets through.
func isPreviewFramePath(rel string) bool {
	parts := strings.Split(rel, "/")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "entities" || parts[3] != "edit" || parts[4] != "preview" {
		return false
	}
	if _, err := uuid.Parse(parts[2]); err != nil || len(parts[2]) != 36 {
		return false
	}
	tok := parts[5]
	if len(tok) != previewTokenLen {
		return false
	}
	for _, c := range []byte(tok) {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}

// inviteToken reads the invite token of an accept request: the query for a
// GET, the query or the form body (bounded, then restored) for a POST.
func inviteToken(r *http.Request) string {
	if t := r.URL.Query().Get("token"); t != "" {
		return t
	}
	if r.Method != http.MethodPost || r.Body == nil ||
		!strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
		return ""
	}
	b, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, maxInviteFormBytes))
	r.Body = io.NopCloser(bytes.NewReader(b))
	if err != nil {
		return ""
	}
	form, err := url.ParseQuery(string(b))
	if err != nil {
		return ""
	}
	return form.Get("token")
}

// cgnat is RFC 6598 shared address space, which net.IP.IsPrivate does not cover.
var cgnat = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

// internalAddress reports a client address that cannot tell visitors apart:
// loopback, private (RFC 1918, IPv6 ULA), shared CGNAT space (100.64.0.0/10,
// what Tailscale and some CNIs use), link-local, or an IPv4-mapped form of
// those. The address is the one middleware.GetClientIP identified, so a trusted
// proxy that reports the real visitor never lands here; a proxy that does not
// (nginx without X-Forwarded-For, a Docker bridge gateway, node SNAT) makes
// every visitor share its one address, and one throttle bucket would then lock
// everybody out. A direct internet client cannot present such an address, so
// nothing is gained by spoofing one.
func internalAddress(ip string) bool {
	a := net.ParseIP(ip)
	return a != nil && (a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() || cgnat.Contains(a))
}

// plainLoopback is a local client (dev) that sends no forwarding header: there
// is no proxy to configure, so the operator is not warned about one.
func plainLoopback(ip string, r *http.Request) bool {
	a := net.ParseIP(ip)
	return a != nil && a.IsLoopback() && r.Header.Get("X-Forwarded-For") == "" && r.Header.Get("X-Real-IP") == ""
}

// nfChargeWriter charges the throttle once when the response turns out to be a
// 404 or 405, whichever handler wrote it.
type nfChargeWriter struct {
	http.ResponseWriter
	charge func()
	done   bool
}

func (w *nfChargeWriter) WriteHeader(code int) {
	if !w.done && (code == http.StatusNotFound || code == http.StatusMethodNotAllowed) {
		w.done = true
		w.charge()
	}
	w.ResponseWriter.WriteHeader(code)
}

// Unwrap lets http.ResponseController reach the real writer (Flush, deadlines).
func (w *nfChargeWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *nfChargeWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

const ownerCacheTTL = 5 * time.Second

// ownerCache answers "does an owner exist" from the user count, cached for a
// few seconds so a signed-out hit on the setup address is one cheap lookup.
type ownerCache struct {
	count func(ctx context.Context) (int, error)
	mu    sync.Mutex
	at    time.Time
	has   bool
}

func (c *ownerCache) hasOwner(ctx context.Context) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.at.IsZero() && time.Since(c.at) < ownerCacheTTL {
		return c.has
	}
	n, err := c.count(ctx)
	if err != nil {
		return true // fail closed: do not offer the setup address
	}
	c.at, c.has = time.Now(), n > 0
	return c.has
}

// newInterceptDeps wires the intercept to the server's services. Missing
// collaborators (a route shell without a database) answer "no".
func (s *Server) newInterceptDeps() interceptDeps {
	d := interceptDeps{
		prefix:      s.adminPathValue,
		signedIn:    func(*http.Request) bool { return false },
		hasOwner:    func(context.Context) bool { return true },
		inviteValid: func(context.Context, string) bool { return false },
		refuse:      s.writeStandardNotFound,
		probes:      newProbeCounter(s.logger),
		oldPrefixes: s.oldAdminPrefixes,
	}
	if s.cfg != nil {
		d.throttle = newNFThrottle(s.cfg.Admin.NotFoundPerMin, s.cfg.Admin.NotFoundBlock)
		d.throttle.onBlock = func(blocked int) {
			// Count only: no address, bucket key or path.
			s.logger.Info("admin address throttle: a client was blocked", "event", "admin_throttle_block", "blocked_clients", blocked)
		}
		d.unidentified = sync.OnceFunc(func() {
			s.logger.Warn("not-found throttle skipped: requests arrive from a loopback or private address, so clients cannot be told apart; if hatamoto runs behind a reverse proxy or a container network, set HATAMOTO_TRUSTED_PROXIES to its address and make it send X-Forwarded-For")
		})
	}
	if s.sessionStore != nil {
		d.signedIn = func(r *http.Request) bool {
			c, err := r.Cookie(auth.SessionCookieName)
			if err != nil || c.Value == "" {
				return false
			}
			sess, err := s.sessionStore.Validate(r.Context(), c.Value)
			return err == nil && sess.Kind == session.KindAdmin && sess.UserID != nil
		}
	}
	if s.userRepo != nil {
		d.hasOwner = (&ownerCache{count: s.userRepo.Count}).hasOwner
	}
	if s.userTokens != nil {
		d.inviteValid = func(ctx context.Context, token string) bool {
			if token == "" {
				return false
			}
			inv, err := s.userTokens.PeekInvite(ctx, token)
			return err == nil && inv != nil
		}
	}
	return d
}
