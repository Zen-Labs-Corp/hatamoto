package server

import (
	"net/http"

	"hatamoto.dev/core/internal/httpx"
)

// originNull reports a sandboxed (opaque origin) caller. Browsers send the
// literal "null" as the Origin of a page served with a CSP sandbox that lacks
// allow-same-origin.
func originNull(r *http.Request) bool { return r.Header.Get("Origin") == "null" }

// publicCORS lets a sandboxed page read the response. Only "Origin: null" is
// answered; any other origin is left as it was (no CORS headers). Credentials
// are never allowed, so the opaque origin cannot ride a session.
func publicCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if originNull(r) {
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", "null")
			h.Add("Vary", "Origin")
		}
		next.ServeHTTP(w, r)
	})
}

// preflight answers OPTIONS for a public route: 204, with CORS headers only
// for an opaque origin.
func preflight(methods string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if originNull(r) {
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", "null")
			h.Add("Vary", "Origin")
			h.Set("Access-Control-Allow-Methods", methods)
			h.Set("Access-Control-Allow-Headers", "Content-Type, X-Page-URL")
			h.Set("Access-Control-Max-Age", "600")
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleCORS mounts a public route with the opaque-origin CORS answer and the
// matching OPTIONS preflight. mw are applied inside the CORS wrapper.
func handleCORS(r *httpx.Group, method, path string, h http.HandlerFunc, mw ...httpx.Middleware) {
	r.HandleFunc("OPTIONS "+path, preflight(method+", OPTIONS"))
	r.Group("", append([]httpx.Middleware{publicCORS}, mw...)...).HandleFunc(method+" "+path, h)
}

// timingTokenOK reports whether tok is a valid, unexpired form timing token
// for slug. A sandboxed page has no session, so this stateless token is the
// only proof it loaded the page.
func (s *Server) timingTokenOK(slug, tok string) bool {
	return s.spam != nil && s.spam.CheckToken(slug, tok)
}
