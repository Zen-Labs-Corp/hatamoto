package server

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"

	"hatamoto.dev/core/internal/middleware"
	"hatamoto.dev/core/internal/pluginhost"
	"hatamoto.dev/sdk"
)

// requestRedirect lets a plugin answer a GET or HEAD for one of the exact
// paths it declared (request.redirect), before the router, the stored
// redirects and the page cache see the request. The answer is a same-site 302
// or 307 and/or cookies; everything else, including every failure, leaves the
// request to next untouched.
//
// It sits behind the site gate and in front of the mux, so it never runs for
// the admin address or the preview host. Core-owned paths cannot be declared
// (see pluginhost.RedirectPathsRefusal); only the configurable public API
// prefix, which no fixed list covers, is skipped here.
func (s *Server) requestRedirect(next http.Handler) http.Handler {
	var filter atomic.Pointer[pluginhost.RedirectFilter]
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			next.ServeHTTP(w, r)
			return
		}
		f := filter.Load()
		if f == nil {
			if f = pluginhost.NewRedirectFilter(s.plugins, s.hooks); f == nil {
				next.ServeHTTP(w, r)
				return
			}
			filter.Store(f)
		}
		if !f.Declares(r.URL.Path) || s.underPublicAPIPrefix(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		out, answered := f.Apply(r.Context(), sdk.RedirectContext{
			Path:    r.URL.Path,
			Query:   r.URL.RawQuery,
			Cookies: pluginCookies(r),
			Country: s.geoHolder.Country(middleware.GetClientIP(r)),
		})
		if !answered {
			// The path is declared, so the answer depends on the visitor: a
			// shared cache must not replay this response to someone else.
			next.ServeHTTP(&visitorSpecificWriter{ResponseWriter: w}, r)
			return
		}
		pluginhost.WriteSetCookies(w, r, out.SetCookies)
		if out.Redirect == nil {
			// Whatever answers (page, stored redirect, 404) carries the plugin's
			// Set-Cookie, so it is never stored anywhere.
			next.ServeHTTP(&visitorSpecificWriter{ResponseWriter: w, noStore: true},
				r.WithContext(withUncachedRequest(r.Context())))
			return
		}
		// Not http.Redirect: it re-cleans the path, which could turn a
		// validated "/./\\x" into "/\\x". The validated string is sent as is.
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Location", out.Redirect.Location)
		w.WriteHeader(out.Redirect.Status)
	})
}

// visitorSpecificWriter makes the response of a declared path safe for shared
// caches whichever handler writes it, by fixing the headers as they are sent.
// noStore forces Cache-Control: no-store (the response carries a plugin's
// Set-Cookie); otherwise a public or s-maxage policy becomes private. Vary:
// Cookie is added either way, the rule core uses for plugin routes that
// declare cookies.
type visitorSpecificWriter struct {
	http.ResponseWriter
	noStore bool
	sent    bool
}

func (v *visitorSpecificWriter) WriteHeader(code int) {
	if !v.sent && (code >= 200 || code == http.StatusSwitchingProtocols) {
		v.sent = true
		v.fix()
	}
	v.ResponseWriter.WriteHeader(code)
}

func (v *visitorSpecificWriter) Write(b []byte) (int, error) {
	if !v.sent {
		v.sent = true
		v.fix()
	}
	return v.ResponseWriter.Write(b)
}

func (v *visitorSpecificWriter) Flush() { http.NewResponseController(v.ResponseWriter).Flush() }

func (v *visitorSpecificWriter) Unwrap() http.ResponseWriter { return v.ResponseWriter }

func (v *visitorSpecificWriter) fix() {
	h := v.Header()
	if v.noStore {
		h.Set("Cache-Control", "no-store")
	} else if cc := h.Get("Cache-Control"); cc != "" {
		h.Set("Cache-Control", privatizeCacheControl(cc))
	}
	for _, f := range h.Values("Vary") {
		for tok := range strings.SplitSeq(f, ",") {
			if t := strings.TrimSpace(tok); t == "*" || strings.EqualFold(t, "Cookie") {
				return
			}
		}
	}
	h.Add("Vary", "Cookie")
}

// privatizeCacheControl turns a shared-cacheable policy into a private one:
// public becomes private and s-maxage is dropped.
func privatizeCacheControl(cc string) string {
	var out []string
	hasPrivate := false
	for d := range strings.SplitSeq(cc, ",") {
		d = strings.TrimSpace(d)
		name, _, _ := strings.Cut(strings.ToLower(d), "=")
		switch name {
		case "s-maxage", "":
			continue
		case "public":
			d = "private"
			name = "private"
		}
		if name == "private" {
			if hasPrivate {
				continue
			}
			hasPrivate = true
		}
		out = append(out, d)
	}
	if !hasPrivate && !slices.Contains(out, "no-store") {
		out = append(out, "private")
	}
	return strings.Join(out, ", ")
}

func (s *Server) underPublicAPIPrefix(p string) bool {
	if s.cfg == nil {
		return false
	}
	prefix := s.cfg.PublicSite.APIPrefix
	return prefix != "" && underPrefix(p, prefix)
}

// pluginCookies returns the request's plugin-namespaced cookies by browser
// name, first value each. The session, CSRF and every other core cookie stay
// out.
func pluginCookies(r *http.Request) map[string]string {
	out := map[string]string{}
	for _, c := range r.Cookies() {
		if !strings.HasPrefix(c.Name, pluginhost.CookiePrefix) {
			continue
		}
		if _, seen := out[c.Name]; !seen {
			out[c.Name] = c.Value
		}
	}
	return out
}

type uncachedRequestKey struct{}

// withUncachedRequest marks a request whose response depends on a plugin's
// cookie answer: serve neither reads nor writes the page cache for it and
// sends private headers.
func withUncachedRequest(ctx context.Context) context.Context {
	return context.WithValue(ctx, uncachedRequestKey{}, true)
}

func uncachedRequest(ctx context.Context) bool {
	v, _ := ctx.Value(uncachedRequestKey{}).(bool)
	return v
}
