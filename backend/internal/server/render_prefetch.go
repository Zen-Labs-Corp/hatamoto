package server

import (
	"net/http"
	"strings"

	"hatamoto.dev/core/internal/redirect"
)

// isPrefetch reports a browser prefetch (Sec-Purpose: prefetch, also
// "prefetch;anonymous-client-ip"). It is a hint, not a visit: nothing the
// owner reads as traffic may count it.
func isPrefetch(r *http.Request) bool {
	return r != nil && strings.Contains(strings.ToLower(r.Header.Get("Sec-Purpose")), "prefetch")
}

// prefetchMiddleware keeps a prefetch out of the public hit counters: redirect
// hits (via the request context), and the AI-referral count (the request is
// served as usual, but the referral is not recorded).
func prefetchMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isPrefetch(r) {
			r = r.WithContext(redirect.WithoutCounting(r.Context()))
		}
		next.ServeHTTP(w, r)
	})
}
