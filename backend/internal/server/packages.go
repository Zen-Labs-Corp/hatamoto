package server

import (
	"context"
	"io/fs"
	"log/slog"
	"time"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/editionstate"
	"hatamoto.dev/core/internal/packages"
	"hatamoto.dev/core/internal/pluginhost"
	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/core/internal/safefetch"
)

// packageReconcileEvery is how often the running server compares itself with
// the packages table, so a package installed by the CLI (or another replica)
// goes live without a restart. Development polls faster for `hatamoto theme dev`.
func (s *Server) packageReconcileEvery() time.Duration {
	if s.cfg.Environment != "production" {
		return time.Second
	}
	return 5 * time.Second
}

// setupPackages builds the package manager over the plugin runtime and the
// theme engine, then applies what is installed: plugins load and the active
// theme is set. It runs after both exist.
func (s *Server) setupPackages() {
	if s.db == nil {
		return
	}
	trust, err := packages.ParseTrustSet(s.cfg.Packages.TrustedKeyList())
	if err != nil {
		s.logger.Error("packages: HATAMOTO_TRUSTED_KEYS is not valid; no signer is trusted", slog.Any("error", err))
		trust = packages.TrustSet{}
	}
	pluginKey, _ := editionstate.PluginKey()
	opts := packages.Options{
		Pool:      s.db.Pool(),
		Idle:      s.idle,
		Trust:     trust,
		PluginKey: pluginKey,
		Entitled:  editionstate.Plugin,
		AllowDev:  s.cfg.Environment != "production",
		OnChange:  s.onThemeChange,
		Logger:    s.logger,
	}
	if s.tagsStore != nil {
		opts.DropTagSettings = s.tagsStore.DropPack
	}
	if s.entityService != nil {
		opts.Schemas = s.entityService
	}
	if s.menuService != nil {
		opts.MenuSeeder = s.menuService
	}
	if s.demoOn() {
		// Public demo: first-party packages only, never a dev push.
		opts.AllowDev = false
		opts.OnlySigners = []string{"none"} // fail closed if the key cannot be read
		if pub, err := packages.ParsePublicKey(config.FirstPartyKey); err == nil {
			opts.OnlySigners = []string{packages.KeyID(pub)}
		}
	}
	if !s.cfg.Packages.RegistryOff() {
		opts.Registry = &packages.Registry{
			URL: s.cfg.Packages.RegistryURL, Trust: trust, Limits: packages.DefaultLimits, AllowInsecure: s.cfg.Packages.AllowInsecureRegistry,
			Client: safefetch.NewClient(safefetch.ClientOptions{Timeout: 60 * time.Second}),
		}
	}
	if !s.cfg.Packages.DeveloperRegistryOff() && len(opts.OnlySigners) == 0 {
		// The listing key signs only the developer index. It is never added to
		// the trust set that verifies packages.
		if listing, err := packages.ParsePublicKey(config.DeveloperListingKey); err == nil {
			opts.DeveloperRegistry = &packages.Registry{
				URL: s.cfg.Packages.DeveloperRegistryURL, Trust: packages.TrustSet{packages.KeyID(listing): listing},
				Limits: packages.DefaultLimits, AllowInsecure: s.cfg.Packages.AllowInsecureRegistry,
				Client: safefetch.NewClient(safefetch.ClientOptions{Timeout: 60 * time.Second}),
			}
		}
	}
	if s.plugins != nil {
		opts.Plugins, opts.Grants = s.plugins, s.plugins.Grants()
		if s.pluginDB != nil {
			opts.Dropper = s.pluginDB
		}
	}
	if s.renderEngine != nil {
		opts.Engine = s.renderEngine
		opts.NewPreview = func(layers []fs.FS, settings map[string]any) (*render.Engine, error) {
			return render.New(render.Options{
				Layers: layers, Settings: settings, Preview: true, // a preview never rewrites the admin's refused-template list
				HeadFilter: pluginhost.HeadFilter(s.hooks), SiteGraphFilter: pluginhost.SiteGraphFilter(s.hooks), ExtSources: pluginhost.ExtSources(s.hooks), Data: s.renderData, Refs: s.renderData, Logger: s.logger,
			})
		}
	}
	s.packages = packages.NewManager(opts)
	if s.plugins != nil {
		s.plugins.SetGrantPolicy(s.packages.CheckGrant)
		s.plugins.SetScriptsDeny(s.packages.ScriptsRefused)
	}
	s.preview = s.newThemePreview()
	ctx := context.Background()
	// The CLI reads this to know whether this site allows dev packages.
	if err := s.packages.Store().SetServerEnvironment(ctx, s.cfg.Environment); err != nil {
		s.logger.Error("packages: cannot record the server environment", slog.Any("error", err))
	}
	// The webhooks plugin moved into core: copy its subscriptions and uninstall
	// it before anything loads it or the scheduler starts (no double sends).
	s.retireWebhooksPlugin(ctx)
	// The gtm and google-analytics plugins moved into Settings > Privacy & tracking > Tracking.
	s.retireTrackingPlugins(ctx)
	// After the retire steps (legacyPluginID reads the table raw) and before the package reconcile; the plugin runtime is already set up by then.
	s.sealPluginSettings(ctx)
	s.packages.Reconcile(ctx)
	if s.cfg.Plugins.Dir != "" {
		s.logger.Warn("packages: HATAMOTO_PLUGIN_DIR is a development override; do not use it in production", slog.String("dir", s.cfg.Plugins.Dir))
		s.packages.LoadDevDir(ctx, s.cfg.Plugins.Dir)
	}
	s.packagesBooted.Store(true)
}

// onThemeChange runs when an installed package (the theme) changed: pages and
// sitemaps go, and (once the boot reconcile is done) a static export rebuilds everything, since every page
// carries the theme.
func (s *Server) onThemeChange(ctx context.Context) {
	if !s.packagesBooted.Load() {
		// The boot reconcile reports the installed theme as a change; that is
		// not an edit, so it must not queue a rebuild on every restart.
		s.purgeRendered(ctx)
		s.pageCache.notifyContentChanged()
		return
	}
	s.invalidateRendered(ctx)
	s.staticTrigger.Notify("theme", true)
}
