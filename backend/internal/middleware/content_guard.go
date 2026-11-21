package middleware

import (
	"net/http"

	"hatamoto.dev/core/authz"
)

// ContentGuard refuses a request that is not a read (GET, HEAD, OPTIONS)
// unless the session may change content (content.write). API key requests
// pass: AuthOrAPIKey already asked authz for the route's action.
func ContentGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		if _, isKey := GetAPIKey(r.Context()); isKey {
			next.ServeHTTP(w, r)
			return
		}
		if !Can(r.Context(), authz.ActionContentWrite, authz.Object{}) {
			deny(w, http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
