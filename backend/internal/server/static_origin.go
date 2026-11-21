package server

import (
	"net/http"

	"hatamoto.dev/core/internal/statichost"
)

// fromStaticSite reports a request from the static origin while static mode is
// on. Such a page has no session on this origin, so (like a sandboxed page) its
// proof of a page load is the stateless timing token.
func (s *Server) fromStaticSite(r *http.Request) bool {
	return s.hosting.Load().AllowsOrigin(r.Header.Get("Origin"))
}

// foreignOrigin reports, in static mode only, an island POST from another
// site: an Origin that is present, not "null", and that is not S, L or this
// request's own host. Origins compare in normalised form (scheme, host, port,
// case-insensitive), so a different spelling of S or L is still ours. The Host
// fallback compares host and port only, because the scheme behind a proxy is
// not known here. Such a post can only come from a page we never published,
// so nothing from it may be stored. Served mode never refuses.
func (s *Server) foreignOrigin(r *http.Request) bool {
	hs := s.hosting.Load()
	if !hs.Active() {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" || origin == "null" {
		return false
	}
	norm, ok := statichost.NormalizeOrigin(origin)
	if !ok {
		return true // an unparsable Origin is not one of ours
	}
	if norm == hs.Site || norm == hs.Live {
		return false
	}
	for _, scheme := range []string{"http://", "https://"} {
		if own, ok := statichost.NormalizeOrigin(scheme + r.Host); ok && own == norm {
			return false
		}
	}
	return true
}

// refuseForeignOrigin answers 403 {"error":"origin not allowed"} for a
// foreignOrigin request. It wraps island POSTs that have no refusal of their
// own (/track, /consent). In served mode it passes every request through.
func (s *Server) refuseForeignOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.foreignOrigin(r) {
			writeError(w, http.StatusForbidden, "origin not allowed")
			return
		}
		next.ServeHTTP(w, r)
	})
}
