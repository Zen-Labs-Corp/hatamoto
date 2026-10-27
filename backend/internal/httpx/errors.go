package httpx

import (
	"net/http"
	"path"
)

// WithErrorBodies serves mux, replacing the plain-text 404 and 405 that
// ServeMux generates with the body written by fail(w, status). The Allow
// header of a 405 is kept.
func WithErrorBodies(mux *http.ServeMux, fail func(w http.ResponseWriter, status int)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.URL.Path != cleanPath(r.URL.Path) {
			// ServeMux would 307 to the cleaned path (replaying the body there);
			// chi answered 404, and so does this.
			fail(w, http.StatusNotFound)
			return
		}
		h, pattern := mux.Handler(r)
		if pattern != "" {
			mux.ServeHTTP(w, r)
			return
		}
		// No pattern matched: h is ServeMux's own 404/405 handler, or a
		// redirect (GET/HEAD to the cleaned path). Run it against a probe that only keeps the status.
		p := &statusProbe{header: w.Header()}
		h.ServeHTTP(p, r)
		if p.status >= 300 && p.status < 400 {
			h.ServeHTTP(w, r) // a real redirect, with ServeMux's own body
			return
		}
		fail(w, p.status)
	})
}

type statusProbe struct {
	header http.Header
	status int
}

func (p *statusProbe) Header() http.Header { return p.header }
func (p *statusProbe) WriteHeader(code int) {
	if p.status == 0 {
		p.status = code
	}
}
func (p *statusProbe) Write(b []byte) (int, error) {
	if p.status == 0 {
		p.status = http.StatusOK
	}
	return len(b), nil
}

// cleanPath is ServeMux's canonical form: path.Clean, keeping a trailing slash.
func cleanPath(p string) string {
	if p == "" {
		return "/"
	}
	c := path.Clean(p)
	if p[len(p)-1] == '/' && c != "/" {
		c += "/"
	}
	return c
}
