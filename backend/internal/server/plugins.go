package server

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/pluginhost"
)

// setupPlugins builds the WASM plugin runtime. The plugins themselves come from
// installed packages (setupPackages loads them, at boot and whenever the
// database changes). Every plugin is isolated: one that fails to load is
// reported by GET /api/v1/plugins and the rest (and the server) carry on.
func (s *Server) setupPlugins() {
	s.pluginSettings = pluginhost.NewSettingsStore(s.db.Pool())
	s.pluginSettingsStore = s.pluginSettings

	rt, err := pluginhost.NewRuntime(s.logger, s.hooks, s.cfg.Plugins.CacheDir)
	if err != nil {
		s.logger.Error("plugins: runtime unavailable; no plugins loaded", slog.Any("error", err))
		return
	}
	s.plugins = rt

	ctx := context.Background()
	grants := pluginhost.NewGrantStore(s.db.Pool())
	if err := grants.Load(ctx); err != nil {
		// Without the stored grants every capability would read as denied:
		// plugins still load, and nothing is granted by mistake.
		s.logger.Error("plugins: grants not loaded; all capabilities denied", slog.Any("error", err))
	}
	pdb := pluginhost.NewPluginDB(s.db.Pool(), s.logger)
	pdb.AllowPublicObjects = s.cfg.Plugins.AllowPublicDBObjects
	s.pluginDB = pdb
	pdb.WarnPublicObjects(ctx)
	cfg := pluginhost.Config{
		Grants: grants,
		Services: pluginhost.Services{
			Entities:  s.entityService,
			Paths:     pluginEntityPaths{s},
			Terms:     s.taxonomyService,
			Menus:     s.menuService,
			Media:     s.mediaService,
			Redirects: s.redirectService,
			Forms:     s.formService,
			Settings:  s.pluginSettings,
			DB:        pdb,
			Fetcher:   s.pluginFetcher(),

			FetchPerMinute: s.cfg.Plugins.FetchPerMinute,
		},
	}
	if s.db != nil {
		cfg.Services.Groups = entity.NewGroupStore(s.db.Pool())
	}
	if s.emailService != nil {
		cfg.Services.Mail = s.emailService
	}
	if s.scheduler != nil {
		cfg.Scheduler = s.scheduler
	}
	rt.Configure(cfg)
}

// pluginEntityPaths answers the entities.* host functions with the public
// site's own routing: the URL registry places an entity, the public resolver
// finds the entity at a path.
type pluginEntityPaths struct{ s *Server }

func (p pluginEntityPaths) Path(ctx context.Context, ent *entity.Entity) string {
	return locateEntity(ctx, p.s.urlSnapshot(ctx), entity.NewPostgresPublicRepository(p.s.db), ent).Path
}

func (p pluginEntityPaths) Resolve(ctx context.Context, path string) (*entity.Entity, error) {
	if len(path) > 1 {
		path = strings.TrimSuffix(path, "/")
	}
	snap, err := p.s.entityService.URLRegistry().Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	return resolvePluginPath(ctx, entity.NewPostgresPublicRepository(p.s.db), snap, path)
}

// resolvePluginPath finds the published entity served at path. On a
// multi-locale site a locale-prefixed path is read in that locale, as the
// public site does; a hidden locale is not served. An unprefixed path is read
// as before (the site language).
func resolvePluginPath(ctx context.Context, repo resolveRepo, snap *entity.URLSnapshot, path string) (*entity.Entity, error) {
	if scoped, locale, rest, prefixed := localeScope(ctx, snap, path); prefixed {
		if snap.Locales().IsHidden(locale) {
			return nil, entity.ErrEntityNotFound
		}
		ctx, path = scoped, rest
	}
	res, err := resolveEntity(ctx, repo, snap, path)
	if err != nil {
		return nil, err
	}
	if res == nil || res.Kind != resolveKindEntity || res.Entity == nil {
		return nil, entity.ErrEntityNotFound
	}
	return res.Entity, nil
}

// BaseURL is the operator's or owner's configured origin, never one derived
// from a request (there is none here).
func (p pluginEntityPaths) BaseURL(ctx context.Context) string {
	if p.s.cfg != nil {
		if b := strings.TrimRight(strings.TrimSpace(p.s.cfg.PublicSite.BaseURL), "/"); b != "" {
			return b
		}
	}
	if p.s.siteConfig != nil {
		if cfg, err := p.s.siteConfig.Get(ctx); err == nil {
			return strings.TrimRight(strings.TrimSpace(cfg.PublicBaseURL), "/")
		}
	}
	return ""
}

// URL prefixes of the groups that carry a fixed catch-all.
const (
	catchAllAdminPrefix  = "/api/v1"
	catchAllPublicPrefix = "/api/v1/public"
)

// catchAllMethods are the methods the fixed catch-alls are registered for.
// HEAD is served by GET patterns; there is no CORS, so OPTIONS is not answered.
var catchAllMethods = []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete}

// mountCatchAll registers one fixed catch-all per method at g's prefix (and at
// the bare prefix, so it is served without a redirect). Core patterns are more
// specific and win; the rest goes to h.
func mountCatchAll(g *httpx.Group, prefix string, h http.Handler, methods ...string) {
	for _, m := range methods {
		g.Handle(m+" /{rest...}", h)
		if prefix != "" {
			g.Handle(m+" /", h) // the bare prefix and "prefix/"
		}
	}
}

func notFoundHandler(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
}

// pluginFetcher is the outbound HTTP client behind a plugin's http.fetch. A
// public demo has none (nil): the host function answers "unavailable".
func (s *Server) pluginFetcher() *pluginhost.Fetcher {
	if s.demoOn() {
		return nil
	}
	return pluginhost.NewFetcher()
}
