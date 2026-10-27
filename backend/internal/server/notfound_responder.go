package server

import (
	"net/http"
	"strings"

	"hatamoto.dev/core/internal/httpx"
)

// refusalSegment replaces every real path segment when a refusal is dispatched
// to the mux. It matches no route, so only the catch-alls and the mux's own
// 404 and 405 answer.
const refusalSegment = "hm-nf"

// writeStandardNotFound is the one answer for every refused or probed admin
// request: exactly what the public site answers for an unmatched path with the
// same method, query, headers and path shape, so a refusal cannot be told from
// a random public 404 (and cannot be probed with OPTIONS or the theme-preview
// query, which never reach the themed 404 on the public site).
//
//   - GET and HEAD run the same theme-preview step as the public catch-all (a
//     preview query answers 303 and sets the cookie; a preview cookie picks the
//     preview theme) and then render the themed 404 for the request's own path,
//     as the public flow does. It does not log to the 404 table: a probe is not
//     a broken link.
//   - Every other method is dispatched to the real mux through the same error
//     bodies as the public chain, on a path of the same shape that only the
//     mux's 404 and 405 answer. The result is the same status, Allow header
//     and JSON body, with no copy of the routing rules.
func (s *Server) writeStandardNotFound(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		if s.mux != nil {
			httpx.WithErrorBodies(s.mux, publicErrorBody).ServeHTTP(w, unroutable(r))
			return
		}
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if h := s.renderHandlers; h != nil && h.engine != nil {
		h, done := h.forRequest(w, r)
		if done {
			return
		}
		h.writeNotFoundQuiet(w, r)
		return
	}
	// No theme engine (it failed to load): the same JSON answer the public
	// catch-all gives for a reserved path.
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
}

// publicErrorBody is the body the public chain gives the mux's own 404 and 405
// (the same as buildHandler's; the scanner table proves they stay equal).
func publicErrorBody(w http.ResponseWriter, status int) {
	msg := "not found"
	if status == http.StatusMethodNotAllowed {
		msg = "method not allowed"
	}
	writeJSON(w, status, map[string]string{"error": msg})
}

// unroutable returns r with each path segment replaced by refusalSegment. The
// segment count, a trailing slash and any dot or empty segment stay, because
// the mux's answer depends on the path shape (and on whether it is clean).
func unroutable(r *http.Request) *http.Request {
	segs := strings.Split(r.URL.Path, "/")
	for i, seg := range segs {
		if i > 0 && seg != "" && seg != "." && seg != ".." {
			segs[i] = refusalSegment
		}
	}
	r2 := r.Clone(r.Context())
	r2.URL.Path = strings.Join(segs, "/")
	r2.URL.RawPath = ""
	return r2
}

// registerNotFound makes httpx.Refuse answer with the standard responder.
func (s *Server) registerNotFound() {
	httpx.RegisterRefuser(s.writeStandardNotFound)
}
