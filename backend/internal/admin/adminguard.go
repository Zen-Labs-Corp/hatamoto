package admin

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/session"
	"hatamoto.dev/core/internal/user"
)

// adminguard.go is the one home of the rules that seal the admin against
// same-origin page scripts: which requests may fetch an admin document, which
// headers every admin response carries, and (from the token cut-over on) which
// token a non-navigation request must present. Nothing else decides these.

type guardClass int

const (
	classAdmin    guardClass = iota // everything under the admin prefix except assets
	classAsset                      // {admin}/_/...
	classPluginUI                   // /plugin-ui/..., served at the root
	classMisc                       // /auth/..., /-/edit
	classRefused                    // a root-scope path that is none of its routes
)

type frameableKey struct{}

// frameable reports whether the guard verified a frame ticket for this request,
// so the document keeps frame-ancestors 'self'.
func frameable(ctx context.Context) bool {
	ok, _ := ctx.Value(frameableKey{}).(bool)
	return ok
}

// encodedSeparator reports whether the escaped path hides a slash or a
// backslash in a segment. The mux routes on escaped segments, so such a path
// can reach a handler the guard's prefix rules never meant it to.
func encodedSeparator(escaped string) bool {
	e := strings.ToLower(escaped)
	return strings.Contains(e, "%2f") || strings.Contains(e, "%5c")
}

// scope is the group a guard is installed on. The class of a request comes
// from the scope, never from a bare path prefix: a configured admin path that
// equals a root route's first segment must not move admin requests into a
// lenient class.
type scope int

const (
	scopeAdmin scope = iota // the group mounted at the admin prefix
	scopeRoot               // the group with /auth, /plugin-ui and /-/edit
)

// classify works on the escaped path: it is what ServeMux matches, so the
// class is decided on the same segments as the route. It only chooses among
// the routes of its own scope; the root scope refuses (classAdmin is the strict
// class, and the root scope has no admin route) anything it does not know.
func (a *Admin) classify(s scope, r *http.Request) guardClass {
	p := r.URL.EscapedPath()
	if s == scopeAdmin {
		if strings.HasPrefix(p, a.url("_")+"/") && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
			return classAsset
		}
		return classAdmin
	}
	switch {
	case strings.HasPrefix(p, "/plugin-ui/"):
		return classPluginUI
	case strings.HasPrefix(p, "/auth/"), p == "/-/edit":
		return classMisc
	}
	return classRefused
}

// guardFor is the router-level middleware installed on the admin group
// (scopeAdmin) and on the root group that carries /auth, /plugin-ui and /-/edit
// (scopeRoot).
func (a *Admin) guardFor(s scope) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			class := a.classify(s, r)
			if class == classAdmin || class == classMisc {
				w = &sealWriter{ResponseWriter: w}
			}
			if class == classRefused || r.Header.Get("Service-Worker") == "script" {
				a.refuse(w, r)
				return
			}
			if encodedSeparator(r.URL.EscapedPath()) {
				a.refuse(w, r) // nothing the admin links to carries an encoded separator
				return
			}
			switch class {
			case classAsset:
				// Not documents: scripts, styles, fonts and images (tokens.css is
				// read cross-origin by plugin frames). No secrets in any of them.
			case classPluginUI:
				if !pluginUIAllowed(r) {
					a.refuse(w, r)
					return
				}
			case classMisc:
			default:
				a.warnNoFetchMetadata(r)
				r = a.withAuth(r) // the one authentication of this request; RequireSession reuses it
				if ref, ok := a.previewRoute(r); ok {
					// A staged preview frame: its credential is the one-time ticket in
					// the URL, checked against this request's session and the entity.
					id, tok := ref.id, ref.ticket
					_, sess, authed := a.cachedAuth(r)
					var body []byte
					if authed && sess != nil {
						body, ok = a.tickets.consume(ticketPreview, tok, sess.ID, id.String())
					} else {
						ok = false
					}
					if !ok {
						a.refuse(w, r)
						return
					}
					// The handler owns its headers.
					w = w.(*sealWriter).ResponseWriter
					ctx := context.WithValue(r.Context(), frameableKey{}, true)
					r = r.WithContext(context.WithValue(ctx, previewPayloadKey{}, body))
				} else {
					var ok bool
					if r, ok = a.tokenRule(w, r); !ok {
						return
					}
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// noFetchMetaEvery is the minimum gap between two "no fetch metadata" warnings.
const noFetchMetaEvery = 10 * time.Minute

// warnNoFetchMetadata says why an admin on plain HTTP shows nothing but
// refusals: browsers send Sec-Fetch-* only over HTTPS or localhost. It logs at
// most once per noFetchMetaEvery.
func (a *Admin) warnNoFetchMetadata(r *http.Request) {
	if r.Header.Get("Sec-Fetch-Mode") != "" || isHTTPS(r) {
		return
	}
	now := time.Now()
	last := a.noFetchMetaLogged.Load()
	if now.Sub(time.Unix(0, last)) < noFetchMetaEvery || !a.noFetchMetaLogged.CompareAndSwap(last, now.UnixNano()) {
		return
	}
	a.logger().Warn("admin request without fetch metadata refused; browsers only send it over HTTPS or localhost, serve the admin over HTTPS")
}

type authKey struct{}

// authResult is the outcome of authenticating a request, kept in the context
// (also when it failed, so a failed lookup is not repeated).
type authResult struct {
	user *user.User
	sess *session.Session
	ok   bool
}

// withAuth authenticates r once and stores the result in its context.
func (a *Admin) withAuth(r *http.Request) *http.Request {
	u, sess, ok := a.authenticate(r)
	return r.WithContext(context.WithValue(r.Context(), authKey{}, authResult{u, sess, ok}))
}

// cachedAuth is the guard's authentication of r, or a fresh one when the
// request did not come through the guard.
func (a *Admin) cachedAuth(r *http.Request) (*user.User, *session.Session, bool) {
	if res, ok := r.Context().Value(authKey{}).(authResult); ok {
		return res.user, res.sess, res.ok
	}
	return a.authenticate(r)
}

// expectedToken is the token this request must present: the session token for
// a signed-in request, else the pre-session token of the first valid hm_pre
// cookie. A top-level navigation with neither mints the cookie (the sign-in, reset and
// setup forms are served to it). "" means nothing can match.
func (a *Admin) expectedToken(w http.ResponseWriter, r *http.Request) string {
	if res, ok := r.Context().Value(authKey{}).(authResult); ok && res.ok {
		return a.sessionToken(res.sess.ID)
	}
	if ids := a.validPreIDs(r); len(ids) > 0 {
		return a.preToken(ids[0])
	}
	if isNavigation(r) {
		return a.preToken(a.setPre(w, r))
	}
	return ""
}

// tokenRule is the token rule of the admin class. A navigation GET needs no
// token (its response is unreadable to page script). Every other safe request
// must present the token in X-CSRF-Token. An unsafe request must be
// same-origin and present the token in the header or the _csrf field. The
// expected token is stored in the context for the templates (csrfToken) only
// once the request has passed: a refusal page never carries it.
func (a *Admin) tokenRule(w http.ResponseWriter, r *http.Request) (*http.Request, bool) {
	want := a.expectedToken(w, r)
	// shown is the token the templates render: the one that was matched. A
	// request admitted by the pre-session fallback never gets the session
	// token back, since a script can choose the hm_pre it presents.
	shown := want
	pass := func(r *http.Request) (*http.Request, bool) {
		return r.WithContext(context.WithValue(r.Context(), csrfKey, shown)), true
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		if isNavigation(r) || tokenEqual(r.Header.Get(csrfHeader), want) {
			return pass(r)
		}
		a.refuse(w, r)
		return r, false
	}
	if !sameOrigin(r) {
		a.blocked(w, r)
		return r, false
	}
	accept := func(got string) bool {
		// A tab opened from an external link while signed in may hold a form
		// rendered before the session, which carries the pre-session token.
		if tokenEqual(got, want) {
			return true
		}
		if a.preSessionRoute(r) && a.validPreToken(r, got) {
			shown = got
			return true
		}
		return false
	}
	r, ok := checkToken(r, a.url(streamedUploadPath), accept)
	if !ok {
		a.blocked(w, r)
		return r, false
	}
	return pass(r)
}

// preSessionRoute is a form that is served before any session exists, so its
// token may be a pre-session one: sign-in, recovery, invitation and the first
// setup step (the later setup steps need the owner's session).
func (a *Admin) preSessionRoute(r *http.Request) bool {
	for _, p := range []string{"login", "forgot", "reset", "accept", "setup"} {
		if r.URL.Path == a.url(p) {
			return true
		}
	}
	return false
}

// isNavigation is a top-level document navigation, which is the only way a
// browser reaches an admin document without a script in between.
func isNavigation(r *http.Request) bool {
	return (r.Method == http.MethodGet || r.Method == http.MethodHead) &&
		r.Header.Get("Sec-Fetch-Mode") == "navigate" && r.Header.Get("Sec-Fetch-Dest") == "document"
}

// handleEditLink is the admin bar's /-/edit?id=<uuid> redirector: only a
// top-level user navigation (Sec-Fetch-User) by a signed-in user is sent to
// the editor. Everything else gets the same refusal as an unknown admin URL.
func (a *Admin) handleEditLink(w http.ResponseWriter, r *http.Request) {
	h := r.Header
	if r.Method != http.MethodGet || !isNavigation(r) || h.Get("Sec-Fetch-User") != "?1" {
		a.refuse(w, r)
		return
	}
	id, err := uuid.Parse(r.URL.Query().Get("id"))
	if err != nil {
		a.refuse(w, r)
		return
	}
	if _, _, ok := a.cachedAuth(a.withAuth(r)); !ok {
		a.refuse(w, r)
		return
	}
	http.Redirect(w, r, a.url("entities", id.String(), "edit"), http.StatusSeeOther)
}

// pluginUIAllowed admits what a plugin frame loads. Site is not checked for
// subresources: a sandboxed frame without allow-same-origin is an opaque
// origin, so its requests are cross-site.
func pluginUIAllowed(r *http.Request) bool {
	switch r.Header.Get("Sec-Fetch-Dest") {
	case "iframe":
		return r.Header.Get("Sec-Fetch-Mode") == "navigate" && r.Header.Get("Sec-Fetch-Site") == "same-origin"
	case "script", "style", "image", "font":
		return true
	}
	return false // document, empty, missing: fail closed
}

type previewRef struct {
	id     uuid.UUID
	ticket string
}

// previewRoute reports whether r is a frame navigation to a preview URL,
// <admin>/entities/{id}/edit/preview/{ticket}: GET, same-origin iframe
// navigation, and exactly that shape. It does not judge the ticket. Anything
// else (including a fetch of that URL) falls to the token rule.
func (a *Admin) previewRoute(r *http.Request) (previewRef, bool) {
	h := r.Header
	if r.Method != http.MethodGet || h.Get("Sec-Fetch-Dest") != "iframe" ||
		h.Get("Sec-Fetch-Mode") != "navigate" || h.Get("Sec-Fetch-Site") != "same-origin" {
		return previewRef{}, false
	}
	rest, ok := strings.CutPrefix(r.URL.EscapedPath(), a.url("entities")+"/")
	if !ok {
		return previewRef{}, false
	}
	parts := strings.Split(rest, "/")
	if len(parts) != 4 || parts[1] != "edit" || parts[2] != "preview" || parts[3] == "" {
		return previewRef{}, false
	}
	id, err := uuid.Parse(parts[0])
	if err != nil {
		return previewRef{}, false
	}
	return previewRef{id: id, ticket: parts[3]}, true
}

// refuse is THE refusal call site: it answers with the site's standard
// not-found response (httpx.Refuse), so a refused admin request is the same
// page a probe gets for any path that does not exist.
//
// Kept on purpose: the request loses its admin token (the page must not echo
// it), the sealed response headers (Cache-Control no-store, COOP, Referrer-Policy:
// every admin response carries them, a refusal included) and HX-Refresh for
// htmx. Dropped: the admin's own secureHeaders (CSP with a nonce, frame
// denial), because the standard 404 renders under the server's own security
// headers and the admin CSP would only make it differ. The sealed headers are
// the one remaining difference from a public 404, and they carry no admin
// content.
//
// An htmx request is told to reload the page (a navigation, which signs the
// user in again or picks up the new token), so polling does not loop on a
// toast. Only the request's own HX-Request header decides: no session state.
func (a *Admin) refuse(w http.ResponseWriter, r *http.Request) {
	r = withoutToken(r)
	w = &sealWriter{ResponseWriter: w}
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Refresh", "true")
	}
	httpx.Refuse(w, r)
}

// sealWriter forces the admin's response headers at write time, so a handler
// that sets its own Cache-Control cannot weaken them.
type sealWriter struct {
	http.ResponseWriter
	sealed bool
}

func (s *sealWriter) seal() {
	if s.sealed {
		return
	}
	s.sealed = true
	h := s.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("Cross-Origin-Opener-Policy", "same-origin")
	h.Set("Referrer-Policy", "no-referrer")
}

func (s *sealWriter) WriteHeader(code int) {
	s.seal()
	s.ResponseWriter.WriteHeader(code)
}

func (s *sealWriter) Write(b []byte) (int, error) {
	s.seal()
	return s.ResponseWriter.Write(b)
}

func (s *sealWriter) Flush() {
	s.seal()
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *sealWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }
