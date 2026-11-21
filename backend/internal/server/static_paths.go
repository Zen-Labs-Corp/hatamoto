package server

import (
	"strings"

	"hatamoto.dev/core/internal/adminpath"
	"hatamoto.dev/core/internal/statichost"
)

// isSearchPath reports "/search" or "/<one segment>/search" (a locale-prefixed
// search). The locale set is not read here, so any first segment counts:
// over-excluding a page called /x/search is harmless, because search is
// rendered on the live origin and must never be redirected to static files.
func isSearchPath(p string) bool {
	if p == "/search" {
		return true
	}
	seg, rest, ok := strings.Cut(strings.TrimPrefix(p, "/"), "/")
	return ok && seg != "" && rest == "search"
}

// staticNoRedirect reports paths the live origin keeps answering in static
// mode (never 301). It takes the path only, without the query, and reads no
// database. Redirecting these would break islands, assets, machine files or
// the admin, which no static site can answer.
func (s *Server) staticNoRedirect(p string) bool {
	// ReservedPrefixes, the core-owned first segments and root .txt/.xml files.
	if adminpath.RedirectPathRefusal(p) != "" {
		return true
	}
	if isSearchPath(p) || s.underPublicAPIPrefix(p) {
		return true
	}
	a := s.adminPathValue()
	return a != "" && (p == a || strings.HasPrefix(p, a+"/"))
}

// staticLivePath reports island targets only the live origin answers. Links
// and form targets to them point at L in a static page, because the static
// origin has no handler for them. apiPrefix is the configured public API
// prefix ("" when unset).
func staticLivePath(p, apiPrefix string) bool {
	if isSearchPath(p) {
		return true
	}
	if apiPrefix != "" && underPrefix(p, apiPrefix) {
		return true
	}
	seg, _, _ := strings.Cut(strings.TrimPrefix(p, "/"), "/")
	return statichost.LiveSegment(seg)
}
