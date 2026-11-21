package server

import (
	"net/http"
	"net/url"
)

// fromStaticSite reports a request from the static origin while static mode is
// on. Such a page has no session on this origin, so (like a sandboxed page) its
// proof of a page load is the stateless timing token.
func (s *Server) fromStaticSite(r *http.Request) bool {
	return s.hosting.Load().AllowsOrigin(r.Header.Get("Origin"))
}

// foreignOrigin reports, in static mode only, an island POST from another
// site: an Origin that is present, not "null", not S, not L and not this
// request's own host. Such a post can only come from a page we never
// published, so nothing from it may be stored. Served mode never refuses.
func (s *Server) foreignOrigin(r *http.Request) bool {
	hs := s.hosting.Load()
	if !hs.Active() {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" || origin == "null" || origin == hs.Site || origin == hs.Live {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return true // an unparsable Origin is not one of ours
	}
	return u.Host != r.Host
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
