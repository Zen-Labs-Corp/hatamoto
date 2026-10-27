package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/middleware"
)

// demoBannerHTML is the small fixed strip public pages carry in demo mode:
// inline styles only and no theme classes, so any theme shows it the same.
const demoBannerHTML = `<div role="note" data-hm-demo-banner style="position:fixed;left:0;right:0;bottom:0;z-index:2147483647;margin:0;padding:6px 12px;background:#1a1a1a;color:#fff;font:13px/1.4 system-ui,sans-serif;text-align:center">` +
	config.DemoBanner + `</div>`

// demoAPIGuard refuses, in demo mode, the /api/v1 twins of the admin actions
// config.DemoConfig lists (users, API keys, own profile): 403 with a message.
func (s *Server) demoAPIGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if msg, refused := s.cfg.Demo.Refusal(r.Method, r.URL.Path); refused {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": msg})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// demoBanner adds the demo strip to every public HTML page. The admin carries
// its own banner and the API and media are not pages, so they are left alone.
func demoBanner(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if r.Method != http.MethodGet || p == defaultAdminPrefix || strings.HasPrefix(p, defaultAdminPrefix+"/") || strings.HasPrefix(p, "/api/") || strings.HasPrefix(p, "/p/") || strings.HasPrefix(p, "/media/") {
			next.ServeHTTP(w, r)
			return
		}
		bw := &bannerWriter{ResponseWriter: w}
		next.ServeHTTP(bw, r)
		bw.finish()
	})
}

// bannerWriter buffers an HTML response so the strip can be placed before
// </body>; any other response passes straight through.
type bannerWriter struct {
	http.ResponseWriter
	status int
	buf    bytes.Buffer
	html   bool
	begun  bool
}

func (b *bannerWriter) WriteHeader(code int) {
	if b.begun {
		return
	}
	b.begun, b.status = true, code
	ct := b.Header().Get("Content-Type")
	b.html = strings.HasPrefix(ct, "text/html") && b.Header().Get("Content-Encoding") == ""
	if !b.html {
		b.ResponseWriter.WriteHeader(code)
	}
}

func (b *bannerWriter) Write(p []byte) (int, error) {
	if !b.begun {
		if b.Header().Get("Content-Type") == "" {
			b.Header().Set("Content-Type", http.DetectContentType(p))
		}
		b.WriteHeader(http.StatusOK)
	}
	if !b.html {
		return b.ResponseWriter.Write(p)
	}
	return b.buf.Write(p)
}

func (b *bannerWriter) finish() {
	if !b.html {
		return
	}
	body := b.buf.Bytes()
	if i := bytes.LastIndex(bytes.ToLower(body), []byte("</body>")); i >= 0 {
		body = append(append(append([]byte{}, body[:i]...), demoBannerHTML...), body[i:]...)
	}
	b.Header().Set("Content-Length", strconv.Itoa(len(body)))
	b.ResponseWriter.WriteHeader(b.status)
	_, _ = b.ResponseWriter.Write(body)
}

// demoOn reports the public demo mode (HATAMOTO_DEMO=1).
func (s *Server) demoOn() bool { return s.cfg != nil && s.cfg.Demo.Enabled }

// authRateLimiter is the limiter of the unauthenticated admin screens: the
// usual 5 requests a second per address, or 1 a second (burst 5) in demo mode,
// where anyone can reach the sign-in page.
func (s *Server) authRateLimiter() *middleware.RateLimiter {
	if s.demoOn() {
		return middleware.NewRateLimiter(1, 5).ByIPv6Prefix()
	}
	return middleware.AuthRateLimiter()
}

// demoWriteLimit bounds what a visitor can write, so one visitor cannot fill
// the demo's disk or switch the demo off for the others.
//
//   - Every write request (anything but GET/HEAD/OPTIONS) is rate limited per
//     address, as before. This is the only limit on anonymous writes (the
//     contact form and the like keep their own public limits too).
//   - Writes of a signed-in session also spend a byte budget (HATAMOTO_DEMO_WRITE_BUDGET_MB):
//     the bytes actually read from the body, charged to the session and to the
//     client address (an IPv6 address counts as its /64). A key whose budget
//     is spent gets 429 for its own writes only, until the next restart.
//   - Sign-in and sign-out are never refused for budget, and an anonymous
//     request spends nothing, so it cannot use up anyone's budget.
//
// The media directory has its own cap (media.Config.MaxTotalBytes).
func (s *Server) demoWriteLimit(validate middleware.SessionValidator) httpx.Middleware {
	d := s.cfg.Demo
	rl := middleware.NewRateLimiter(d.WritesPerSecond, d.WriteBurst).ByIPv6Prefix()
	// Restoring a version, saving a template and making a block write a whole copy of a page for a
	// request of a few bytes, so the byte budget cannot see them: they share their own, slower,
	// per-user rate (3 at once, then one every 5 s).
	restores := middleware.NewRateLimiter(0.2, 3)
	budget := &demoBudget{limit: int64(d.WriteBudgetMB) * 1024 * 1024, used: map[string]int64{}}
	return func(next http.Handler) http.Handler {
		limited := rl.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, claims := validToken(r, validate)
			if token == "" {
				next.ServeHTTP(w, r) // not signed in: nothing to charge (and the route's own auth refuses it)
				return
			}
			sum := sha256.Sum256([]byte(token))
			keys := []string{"s:" + hex.EncodeToString(sum[:8]), "i:" + middleware.IPBucket(middleware.GetClientIP(r))}
			// the restore limit is per user, so signing in again gives no fresh burst
			if demoRestorePath.MatchString(r.URL.Path) && !restores.Allow(claims.UserID.String()) {
				w.Header().Set("Retry-After", "5")
				http.Error(w, "Restoring versions, saving templates and making blocks are limited in the demo: one every 5 seconds per user.", http.StatusTooManyRequests)
				return
			}
			if !budget.fits(keys, r.ContentLength) {
				w.Header().Set("Retry-After", "3600")
				http.Error(w, "Your write budget for this demo is used up. It resets every hour.", http.StatusTooManyRequests)
				return
			}
			if r.Body != nil {
				r.Body = &countingBody{ReadCloser: r.Body, budget: budget, keys: keys}
			}
			next.ServeHTTP(w, r)
		}))
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet, http.MethodHead, http.MethodOptions:
				next.ServeHTTP(w, r)
			default:
				if demoSignInOrOut(r.URL.Path) {
					next.ServeHTTP(w, r)
					return
				}
				limited.ServeHTTP(w, r)
			}
		})
	}
}

// demoRestorePath matches the requests that restore an entity version (admin, block-level, API),
// save a page as a template or make a block of it: a few request bytes, a full copy written.
// The demo is always served at the default admin address (app boot forces it).
var demoRestorePath = regexp.MustCompile(`/versions/[^/]+(/nodes/[^/]+)?/restore$|^/api/v1/entities/[^/]+/restore/[^/]+$|^` + regexp.QuoteMeta(defaultAdminPrefix) + `/entities/[^/]+/builder/(template|block)$`)

// demoSignInOrOut reports the paths a demo visitor must always reach.
func demoSignInOrOut(path string) bool {
	switch path {
	case defaultAdminPrefix + "/login", defaultAdminPrefix + "/logout", "/api/v1/auth/login", "/api/v1/auth/logout":
		return true
	}
	return false
}

// validToken is the session token (and its claims) of the request that the route would authenticate with, or
// "" when there is none. It looks where the route does: /admin reads the session cookie only
// (a bearer header is ignored there, so a junk one must not hide the cookie); elsewhere the
// bearer token comes first, then the cookie (as middleware.AuthMiddleware does).
func validToken(r *http.Request, validate middleware.SessionValidator) (string, *auth.Claims) {
	var candidates []string
	admin := r.URL.Path == defaultAdminPrefix || strings.HasPrefix(r.URL.Path, defaultAdminPrefix+"/")
	if h := r.Header.Get("Authorization"); !admin && len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		candidates = append(candidates, h[7:])
	}
	if c, err := r.Cookie(auth.SessionCookieName); err == nil {
		candidates = append(candidates, c.Value)
	}
	if c, err := r.Cookie(auth.APISessionCookieName); err == nil && !admin {
		candidates = append(candidates, c.Value)
	}
	for _, t := range candidates {
		if t == "" {
			continue
		}
		if claims, err := validate(r.Context(), t); err == nil {
			return t, claims
		}
	}
	return "", nil
}

// demoBudget is the bytes written so far per key (session or address).
type demoBudget struct {
	mu    sync.Mutex
	limit int64
	used  map[string]int64
}

// fits reports whether every key still has room for a body of the declared
// size (nothing is charged here: only bytes actually read are).
func (b *demoBudget) fits(keys []string, declared int64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, k := range keys {
		if b.used[k] >= b.limit || (declared > 0 && b.used[k]+declared > b.limit) {
			return false
		}
	}
	return true
}

// charge adds n bytes to every key and reports whether all are within budget.
func (b *demoBudget) charge(keys []string, n int64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	ok := true
	for _, k := range keys {
		b.used[k] += n
		ok = ok && b.used[k] <= b.limit
	}
	return ok
}

// countingBody charges the bytes read to the budget and fails the read once
// a key's budget is spent.
type countingBody struct {
	io.ReadCloser
	budget *demoBudget
	keys   []string
}

func (b *countingBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 && !b.budget.charge(b.keys, int64(n)) {
		return n, errDemoBudget
	}
	return n, err
}

var errDemoBudget = errors.New("demo write budget used up")
