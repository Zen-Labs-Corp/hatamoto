package server

import "net/http"

// stagingNoindex marks every response noindex while HATAMOTO_ENV=staging.
func stagingNoindex(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Robots-Tag", "noindex")
		next.ServeHTTP(w, r)
	})
}
