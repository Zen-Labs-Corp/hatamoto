package middleware

import (
	"net/http"

	"hatamoto.dev/core/internal/authzctx"
)

// AttachSubject puts the request's authz subject (API key first, then the
// session) on the context, so the entity service can check the object it
// loads. Requests without a subject pass unchanged.
func AttachSubject(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if s, status := requestSubject(ctx); status == 0 {
			ctx = authzctx.With(ctx, s)
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
