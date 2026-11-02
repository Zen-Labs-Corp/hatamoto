package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"net/http"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/admin/starter"
	"hatamoto.dev/core/internal/adminpath"
	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/localeconv"
	"hatamoto.dev/core/internal/mailsettings"
	"hatamoto.dev/core/internal/person"
	"hatamoto.dev/core/internal/siteconfig"
)

// mountAdminRoutes builds the admin's deps once, builds the first admin from
// them and mounts it. It is the only server -> admin edge; admin never imports
// server. A failure (template parse, setup code) is recorded like a theme
// failure: Start refuses to boot instead of serving broken admin pages. The
// address can change while the server runs (see adminaddr_seam.go): later
// admins are built from the same deps, never by running this again.
func (s *Server) mountAdminRoutes(r *httpx.Group) {
	d, err := s.buildAdminDeps()
	if err != nil {
		s.renderErr = err // Start refuses to boot with the reason
		return
	}
	s.adminMu.Lock()
	s.adminDeps = d
	s.adminMu.Unlock()
	// The first admin is mounted through the route recorder so the route table
	// lists it; its patterns land on the admin's own mux.
	mux := http.NewServeMux()
	s.sink.admin = mux
	b, err := s.buildAdminBundle(d, s.adminPathValue(), mux, r.Group(""))
	s.sink.admin = nil
	if err != nil {
		s.renderErr = err
		return
	}
	s.adminBundle.Store(b)
	s.admin = &adminHolder{s: s}
	if s.addrStore != nil {
		s.refreshAddressState(context.Background())
	}
}

// buildAdminDeps is everything that must exist once per process: the services
// (page cache, import manager, updates, rate limiter) and the shared sudo
// store. It never runs twice, because some of these start goroutines or log a
// setup code.
func (s *Server) buildAdminDeps() (admin.Deps, error) {
	d := admin.Deps{
		Logger:           s.logger,
		SiteNameFallback: s.cfg.Hatamoto.SiteName,
		FlashKey:         s.adminFlashKey(),
		Dev:              s.cfg.Admin.Dev,
		AppVersion:       s.cfg.Admin.Version,
		PublicAPIPrefix:  s.cfg.PublicSite.APIPrefix,
		Demo:             s.cfg.Demo,
	}
	// Interfaces are only set from non-nil services (a typed nil would pass a
	// nil check later); the route-table build has none and never serves.
	if s.sessionStore != nil {
		d.Sessions = s.sessionStore
	}
	if s.userRepo != nil {
		d.Users = s.userRepo
	}
	if s.siteConfig != nil {
		d.Site = s.siteConfig
	}
	if s.authService != nil {
		d.Auth = s.authService
	}
	if s.userTokens != nil {
		d.Tokens = s.userTokens
	}
	if s.userRepo != nil {
		d.Finder = s.userRepo
	}
	if s.emailService != nil {
		d.Mailer = s.emailService
	}
	d.BaseURL = s.configuredBaseURL
	d.CookieSecure = s.cfg.AdminCookieSecure
	d.Invalidate = s.invalidateSiteSettings
	d.ValidateLocales = func(ctx context.Context, def string, entries []siteconfig.LocaleEntry, prefixAll bool, xdef string) ([]siteconfig.LocaleEntry, string, error) {
		return validateLocaleSettings(ctx, localeSettingsRequest{Default: def, Entries: entries, PrefixAll: prefixAll, XDefault: xdef}, s.checkLocaleSegments)
	}
	d.BuilderTokens = func() bool { return s.renderEngine != nil && s.renderEngine.BuilderTokens() }
	d.BuilderParts = func() []string {
		if s.renderEngine == nil {
			return nil
		}
		return s.renderEngine.BuilderParts()
	}
	d.Audit = s.adminAudit
	if s.db != nil {
		d.AuditLog = adminAuditLog{pool: s.db.Pool()}
		d.LocaleConvert = localeconv.NewService(s.db.Pool(), s.logger)
		d.LocaleConvertCandidates = regionTreeCandidates(s.db.Pool())
	}
	if s.siteConfig != nil && s.infra != nil && s.cfg != nil {
		s.pageCache = s.newPageCacheService()
		d.PageCache = s.pageCache
	}
	if s.spamStore != nil {
		d.Spam = spamAdmin{store: s.spamStore, verifier: s.spamVerifier, key: s.spam.SecretKey}
	}
	if s.seoStore != nil && s.entityService != nil && s.db != nil {
		d.SEO = seoAdmin{db: s.db, store: s.seoStore, entities: s.entityService}
	}
	if s.siteConfig != nil {
		d.SiteGraph = s.siteGraphPreview
	}
	if s.backups != nil {
		d.Backups = s.backups
	}
	if s.mailStore != nil && s.emailService != nil {
		d.EmailSettings = emailAdmin{
			store:   s.mailStore,
			key:     mailsettings.PasswordKey(s.cfg.Secret),
			apply:   s.applyEmail,
			current: s.currentEmailResolution,
		}
	}
	d.Updates, d.UpdatesUnavailable = s.adminUpdatesDeps()
	d.Content = s.contentDeps()
	if s.entityService != nil && s.db != nil {
		profiles := person.NewProfiles(s.people(), s.entityService)
		if s.redirectService != nil {
			profiles.WithRedirects(s.personPath, s.redirectService)
		}
		d.People = profiles
		d.PersonPath = s.personPath
	}
	if s.userRepo != nil {
		d.Manager = s.userRepo
	}
	if s.userTokens != nil {
		d.Passwords = s.userTokens
	}
	if s.apiKeyService != nil {
		d.APIKeys = s.apiKeyService
	}
	if s.webhooks != nil {
		d.Webhooks = adminWebhooks{Service: s.webhooks, deliverer: s.webhookDeliverer}
		d.WebhookDeliveryOn = s.webhookDeliveryOn
		d.WebhooksPluginLeft = s.webhooksPluginLeftBehind
	}
	if s.db != nil {
		d.Owners = auth.NewBootstrap(s.db)
		d.NeedsSetup = func(ctx context.Context) bool {
			n, err := s.userRepo.Count(ctx)
			return err == nil && n == 0
		}
		var err error
		if d.SetupCode, err = s.setupCode(); err != nil {
			return admin.Deps{}, err
		}
		if s.addrStore == nil {
			s.addrStore = adminpath.NewStore(s.db.Pool())
		}
	}
	if s.entityService != nil && s.menuService != nil && s.formService != nil && s.siteConfig != nil {
		d.Starter = &starter.Installer{
			Entities: s.entityService, Menus: s.menuService, Forms: s.formService, Site: s.siteConfig,
			Invalidate: s.invalidateRendered,
		}
	}
	d.Growth = s.adminGrowth()
	if s.db != nil && s.entityService != nil {
		s.importManager = s.newImportManager()
		d.Import = s.importManager
	}
	// One limiter for every unauthenticated auth screen (login, forgot, reset, accept, setup).
	d.RateLimit = s.authRateLimiter().Middleware
	// Sudo grants outlive an address change: the owner who just confirmed their
	// password is not asked again on the new address.
	d.Sudo = admin.NewSudoStore()
	if s.addrStore != nil {
		d.Address = &serverAddress{s: s}
	}
	return d, nil
}

// adminUpdatesDeps is the admin's updates service, or why there is none. The
// service is set only from a built agent (a typed nil would pass the admin's
// nil check).
func (s *Server) adminUpdatesDeps() (admin.UpdatesService, string) {
	if s.demoOn() {
		return nil, "Updates are switched off in the public demo."
	}
	if s.updater != nil && s.db != nil {
		svc := newAdminUpdates(s.updater, pgUpdatesLog{db: s.db.Pool()}, os.Getenv, s.logger,
			func() context.Context { return s.lifecycleCtx })
		s.updatesSvc = svc
		return svc, ""
	}
	return nil, s.updaterProblem
}

// refreshAdminUpdates hands the admin the updates service once the agent is
// known: the routes are mounted in New, before SetUpdater or NoUpdater run.
func (s *Server) refreshAdminUpdates() {
	if s.admin == nil {
		return
	}
	svc, problem := s.adminUpdatesDeps()
	s.admin.SetUpdates(svc, problem)
}

// adminHolder gives late-bound services to the admin that serves now and to
// the deps the next admin is built from, so an address change does not lose
// them.
type adminHolder struct{ s *Server }

// SetUpdates sets the updates service (or why there is none).
func (h *adminHolder) SetUpdates(svc admin.UpdatesService, problem string) {
	h.s.adminMu.Lock()
	defer h.s.adminMu.Unlock()
	h.s.adminDeps.Updates, h.s.adminDeps.UpdatesUnavailable = svc, problem
	if b := h.s.adminBundle.Load(); b != nil {
		b.a.SetUpdates(svc, problem)
	}
}

// SetStats sets the install stats service.
func (h *adminHolder) SetStats(svc admin.StatsService) {
	h.s.adminMu.Lock()
	defer h.s.adminMu.Unlock()
	h.s.adminDeps.Stats = svc
	if b := h.s.adminBundle.Load(); b != nil {
		b.a.SetStats(svc)
	}
}

// SetBackups sets the backup service.
func (h *adminHolder) SetBackups(svc admin.BackupsService) {
	h.s.adminMu.Lock()
	defer h.s.adminMu.Unlock()
	h.s.adminDeps.Backups = svc
	if b := h.s.adminBundle.Load(); b != nil {
		b.a.SetBackups(svc)
	}
}

// adminFlashKey derives the flash-cookie signing key from the server secret, so
// the two never share a MAC domain.
func (s *Server) adminFlashKey() []byte {
	m := hmac.New(sha256.New, []byte(s.cfg.Secret))
	m.Write([]byte("hatamoto/admin/flash"))
	return m.Sum(nil)
}

// setupCode returns the first-run wizard code while the install has no users,
// and logs it where the operator will look. HATAMOTO_SETUP_CODE pins it; an
// install with users has none (the wizard's first step then refuses). The
// strength rule for a pinned code applies only while setup is pending: it
// stops the boot with the reason. An install that already has users (for
// example an upgrade whose env file still holds an old short code) only gets
// a warning, since the code is no longer used.
func (s *Server) setupCode() (*admin.SetupCode, error) {
	ctx := context.Background()
	pinned := s.cfg.Admin.SetupCode
	n, err := s.userRepo.Count(ctx)
	if err != nil || n > 0 {
		if err == nil && pinned != "" {
			if cerr := s.cfg.Admin.CheckSetupCode(); cerr != nil {
				s.logger.Warn("setup code is ignored (the site already has users) and would not pass today's rule: " + cerr.Error() + "; remove HATAMOTO_SETUP_CODE")
			}
		}
		return nil, nil
	}
	if pinned != "" {
		if cerr := s.cfg.Admin.CheckSetupCode(); cerr != nil {
			return nil, cerr
		}
		s.logger.Warn("Setup: open " + s.setupURL() + " and enter the setup code from HATAMOTO_SETUP_CODE")
		return admin.SetupCodeFrom(pinned), nil
	}
	code, display := admin.NewSetupCode()
	s.logger.Warn("Setup: open "+s.setupURL()+" and enter code "+display, "setup_code", display)
	return code, nil
}

// setupURL is where the first-run wizard is: the configured site address
// (none: the path only) followed by the admin address and /setup.
func (s *Server) setupURL() string {
	base := ""
	if b := strings.TrimRight(strings.TrimSpace(s.cfg.PublicSite.BaseURL), "/"); b != "" {
		base = b
	} else if s.siteConfig != nil {
		if c, err := s.siteConfig.Get(context.Background()); err == nil {
			base = strings.TrimRight(strings.TrimSpace(c.PublicBaseURL), "/")
		}
	}
	return base + s.adminPathValue() + "/setup"
}

// regionTreeCandidates is the cheap check behind the "region pages found"
// notice on Settings > General: at least two published top-level pages with
// children, each carrying a top-level attribute whose value is its own slug
// (the region attribute Suggest looks for). An ordinary nested site, where
// /services and /about both have children, does not match. localeconv has no
// exported equivalent; its Suggest does the real analysis when the owner opens
// the conversion page. A top-level page whose attributes are not a JSON object
// (array, scalar) never matches; the CASE keeps jsonb_each_text from erroring on
// it, which a plain AND would not (evaluation order is not guaranteed).
func regionTreeCandidates(pool *pgxpool.Pool) func(context.Context) (bool, error) {
	return func(ctx context.Context) (bool, error) {
		var found bool
		err := pool.QueryRow(ctx, `
			SELECT count(*) >= 2 FROM hatamoto_entities p
			JOIN hatamoto_entity_schemas s ON s.id = p.schema_id
			WHERE s.slug = 'page' AND p.parent_id IS NULL AND p.variant_key IS NULL AND p.status = 'published'
			  AND EXISTS (SELECT 1 FROM hatamoto_entities c WHERE c.parent_id = p.id)
			  AND EXISTS (SELECT 1 FROM jsonb_each_text(
					CASE WHEN jsonb_typeof(p.attributes) = 'object' THEN p.attributes ELSE '{}'::jsonb END) a WHERE a.value = p.slug)`).Scan(&found)
		return found, err
	}
}
