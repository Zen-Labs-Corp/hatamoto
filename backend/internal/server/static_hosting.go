package server

import (
	"context"
	"log/slog"
	"net/http"

	"hatamoto.dev/core/internal/staticexport"
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
	hd.Set("Access-Control-Allow-Headers", corsAllowHeaders)
	hd.Set("Access-Control-Max-Age", "600")
	return true
}

// hostingSettingsFunc reads the saved hosting settings; staticexport.SettingsStore.Get fits.
type hostingSettingsFunc func(ctx context.Context) (staticexport.Settings, bool, error)

// hostingSnapshot maps the saved settings and the Site URL to the island
// snapshot. No saved row is served mode, so a default install never turns
// islands on. An invalid origin is dropped by the holder, which leaves the
// snapshot inactive.
func hostingSnapshot(set staticexport.Settings, found bool, site string) statichost.StaticHosting {
	if !found {
		return statichost.StaticHosting{}
	}
	return statichost.StaticHosting{Static: set.Mode == staticexport.ModeStatic, Live: set.LiveOrigin, Site: site}
}

// refreshStaticHosting reads the hosting settings once, never per request, and
// stores the snapshot. It runs at boot, before every static export (the CLI
// never calls Start) and after every Site settings save (S derives from the
// Site URL).
func (s *Server) refreshStaticHosting(ctx context.Context) {
	if s.db == nil {
		return
	}
	s.refreshStaticHostingFrom(ctx, staticexport.NewSettingsStore(s.db.Pool()).Get)
}

// refreshStaticHostingFrom keeps the previous snapshot when the read fails, so
// a database hiccup never flips a live site between modes.
func (s *Server) refreshStaticHostingFrom(ctx context.Context, get hostingSettingsFunc) {
	set, found, err := get(ctx)
	if err != nil {
		s.logger.Warn("static hosting: settings read failed, keeping the previous mode", slog.String("error", err.Error()))
		return
	}
	s.setStaticHosting(hostingSnapshot(set, found, pluginEntityPaths{s}.BaseURL(ctx)))
	if why := s.hosting.Load().InactiveReason(); why != "" {
		s.logger.Warn("static hosting: mode is static but the island snapshot is inactive, so pages are served as in served mode", slog.String("reason", why))
	}
}
