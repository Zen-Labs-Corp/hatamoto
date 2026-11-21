package middleware

import (
	"net/http"

	"hatamoto.dev/core/internal/authzctx"
)

// AttachSubject puts the request's authz subject (API key first, then the
// session) on the context, so the entity service can check the object it
// loads. A request with no usable subject is refused, never passed on as a system caller.
func AttachSubject(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s, status := requestSubject(r.Context())
		if status != 0 {
			deny(w, status)
			return
		}
		next.ServeHTTP(w, r.WithContext(authzctx.With(r.Context(), s)))
	})
}
