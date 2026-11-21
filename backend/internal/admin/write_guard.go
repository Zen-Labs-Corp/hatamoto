package admin

import (
	"net/http"

	"hatamoto.dev/core/authz"
)

// writeGuard refuses every request that is not a read (GET, HEAD, OPTIONS) unless the signed-in user may change the
// site (site.write). Own-account routes are mounted outside it (see mountScreens).
func (a *Admin) writeGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		if !a.can(r.Context(), authz.ActionSiteWrite) {
			a.forbidden(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}
