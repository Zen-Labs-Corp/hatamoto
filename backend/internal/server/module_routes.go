package server

import (
	"net/http"
	"strings"

	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/modhost"
)

// mountModuleRoutes serves the prefixes compiled-in modules registered through
// module/route. They are anonymous at the core level (the module authenticates
// its callers), so every mount shares one auth rate limiter and a body cap.
// With nothing registered it creates nothing.
func (s *Server) mountModuleRoutes(r *httpx.Group) {
	s.modulePrefixes = nil
	mounts := modhost.Routes()
	if len(mounts) == 0 {
		return
	}
	limiter := s.authRateLimiter()
	for _, m := range mounts {
		if err := modhost.MigrationErr(m.Module); err != nil {
			s.logger.Warn("module routes not mounted: its migrations failed", "module", m.Module, "prefix", m.Prefix)
			continue
		}
		g := r.Group("", limiter.Middleware, limitBody(m.MaxBody))
		for _, meth := range catchAllMethods {
			g.Handle(meth+" "+m.Prefix+"{rest...}", m.Handler)
		}
		s.modulePrefixes = append(s.modulePrefixes, m.Prefix)
	}
}

// limitBody caps the request body at n bytes.
func limitBody(n int64) httpx.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, n)
			next.ServeHTTP(w, r)
		})
	}
}

// underModulePrefix reports whether escaped, a request's escaped path (the one
// the mux routes on), is under a mounted module prefix. A path with an encoded
// slash or one the mux would redirect (dot segments, double slashes) is never
// the module's: it takes the public chain, where the site gate applies.
func (s *Server) underModulePrefix(escaped string) bool {
	if hasEncodedSlash(escaped) || !isCleanPath(escaped) {
		return false
	}
	for _, pre := range s.modulePrefixes {
		if strings.HasPrefix(escaped, pre) {
			return true
		}
	}
	return false
}
