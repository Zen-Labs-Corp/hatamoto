package admin

import (
	"net/http"

	"hatamoto.dev/core/authz"
)

// contentGuard refuses a non-read request to the content screens unless the signed-in user may change content
// (content.write). Which entity or media item they may change is decided per object, in the entity service and
// the media handlers.
func (a *Admin) contentGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		if !a.can(r.Context(), authz.ActionContentWrite) {
			a.forbidden(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}
