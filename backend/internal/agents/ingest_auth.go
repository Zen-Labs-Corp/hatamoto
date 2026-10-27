package agents

import (
	"crypto/subtle"
	"net/http"
)

// authorizeHeaderSecret compares the named request header to secret in
// constant time (503 when secret is empty, 401 on mismatch).
func authorizeHeaderSecret(w http.ResponseWriter, r *http.Request, header, secret string) bool {
	if secret == "" {
		http.Error(w, "tracking disabled", http.StatusServiceUnavailable)
		return false
	}
	presented := r.Header.Get(header)
	if presented == "" || subtle.ConstantTimeCompare([]byte(presented), []byte(secret)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	return true
}
