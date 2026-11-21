package server

import (
	"net/http"

	"hatamoto.dev/core/internal/statichost"
)

// commentFormLivePrefix: GET <prefix>{entityID} answers the comment form fragment in static mode.
// "/-/comments/{id}/form" would overlap GET /-/comments/confirm/{token} (ServeMux panics).
const commentFormLivePrefix = "/-/comments/form/"

// staticHosting is the request-path read: one atomic load, never the DB.
func (s *Server) staticHosting() statichost.StaticHosting { return s.hosting.Load() }

// setStaticHosting is the only write path for the snapshot. Keeping it single
// means no request handler can widen the CORS allow-list by accident.
func (s *Server) setStaticHosting(v statichost.StaticHosting) { s.hosting.Store(v) }

// staticCORS answers a request from the static origin. While hosting is active
// the response always varies on Origin, so a cache never serves one origin's
// answer to another. ACAO is set only when origin is exactly S, and
// credentials are never allowed, so the static site cannot ride a session.
// It returns whether ACAO was set.
func staticCORS(hd http.Header, hs statichost.StaticHosting, origin string) bool {
	if !hs.Active() {
		return false
	}
	hd.Add("Vary", "Origin")
	if !hs.AllowsOrigin(origin) {
		return false
	}
	hd.Set("Access-Control-Allow-Origin", hs.Site)
	return true
}

// staticPreflight is staticCORS plus the preflight headers, which are added
// only for S so a refused origin learns nothing about the allowed methods.
func staticPreflight(hd http.Header, hs statichost.StaticHosting, origin, methods string) bool {
	if !staticCORS(hd, hs, origin) {
		return false
	}
	hd.Set("Access-Control-Allow-Methods", methods)
	hd.Set("Access-Control-Allow-Headers", "Content-Type, X-Page-URL")
	hd.Set("Access-Control-Max-Age", "600")
	return true
}
