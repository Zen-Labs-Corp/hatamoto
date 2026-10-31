// Package server provides the HTTP server and routing configuration.
package server

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/agents"
	"hatamoto.dev/core/internal/analytics"
	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/author"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/email"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/form"
	"hatamoto.dev/core/internal/hooks"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/importjob"
	"hatamoto.dev/core/internal/installstats"
	"hatamoto.dev/core/internal/mailsettings"
	"hatamoto.dev/core/internal/media"
	"hatamoto.dev/core/internal/menu"
	"hatamoto.dev/core/internal/middleware"
	"hatamoto.dev/core/internal/packages"
	"hatamoto.dev/core/internal/person"
	"hatamoto.dev/core/internal/pluginhost"
	"hatamoto.dev/core/internal/redirect"
	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/core/internal/sectiontemplate"
	"hatamoto.dev/core/internal/seo"
	"hatamoto.dev/core/internal/session"
	"hatamoto.dev/core/internal/siteconfig"
	"hatamoto.dev/core/internal/spam"
	"hatamoto.dev/core/internal/taxonomy"
	"hatamoto.dev/core/internal/updater"
	"hatamoto.dev/core/internal/user"
	"hatamoto.dev/core/internal/webhook"
	"hatamoto.dev/core/internal/worker"
)

// Server represents the HTTP server.
type Server struct {
	// communityLive overrides communityScriptsLive; tests only.
	communityLive func() bool

	cfg *config.Config
	db  *database.DB
	// mux is built once in setupRoutes and never changes afterwards; routes is
	// its registration root (and the record behind Routes()). router is the
	// served handler: global middleware around mux with JSON 404/405 bodies.
	mux          *http.ServeMux
	routes       *httpx.Group
	router       http.Handler
	logger       *slog.Logger
	sessionStore *session.Store
	userTokens   *auth.UserTokens
	siteConfig   *siteconfig.Store
	httpServer   *http.Server

	// Infrastructure
	infra *Infrastructure

	// Services
	authService *auth.Service
	// validateSession resolves API session tokens; nil means authService's.
	// Route tests set a fake so they need no database.
	validateSession middleware.SessionValidator
	// validateAPICookie resolves the API session cookie (API-kind sessions
	// only); nil means authService's.
	validateAPICookie      middleware.SessionValidator
	apiCSRFKeyOnce         sync.Once
	apiCSRFKeyVal          []byte
	entityService          *entity.Service
	mediaService           *media.Service
	menuService            *menu.Service
	formService            *form.Service
	spam                   *spam.Pipeline // nil in tests that skip spam checks
	spamStore              *spam.Store
	seoStore               *seo.Store // site-wide SEO templates; nil in tests that skip them
	seoLogOnce             sync.Once
	spamVerifier           *spam.HTTPVerifier
	emailService           *email.Service
	mailStore              smtpSettingsStore
	applyMu                sync.Mutex // serialises applyEmail end to end
	emailMu                sync.Mutex // guards emailRes and emailApplied
	emailRes               emailResolution
	emailApplied           bool // a configuration has been applied since boot
	formConfirmationSender formConfirmationSender
	confirmThrottle        confirmationThrottle
	apiKeyService          *auth.APIKeyService
	// Test seams for /health and /ready; nil means the database and apiKeyService.
	pingDB                 func(context.Context) error
	keyValid               func(ctx context.Context, raw string) bool
	sectionTemplateService *sectiontemplate.Service
	redirectService        *redirect.Service
	agentWriter            *agents.Writer
	agentLogHandlers       *agents.LogHandlers // nil unless AGENT_LOG_INGEST + secret
	agentLogTailer         *agents.Tailer      // nil unless AGENT_LOG_INGEST + AGENT_LOG_CADDY_PATH
	agentAdminHandlers     *agents.AdminHandlers
	agentFirstCrawl        *agents.FirstCrawlTracker
	robotsPolicy           agents.RobotsStore
	analyticsService       *analytics.Service
	analyticsHandlers      *analytics.Handlers
	taxonomyService        *taxonomy.Service
	taxonomyHandlers       *taxonomy.Handlers
	authors                *author.Store
	//lint:ignore SA1019 the rollback mirror API stays mounted until the author package is removed
	authorHandlers *author.Handlers

	entitySink        *pluginhost.EntitySink
	apiKeyRateLimiter *middleware.APIKeyRateLimiter

	// Repositories
	userRepo user.Repository
	// entityRepo is the shared entity repository used by handlers that
	// need to reach the new City-Page-Restore (§3.5) methods without
	// going through the entityService facade.
	entityRepo entity.EntityRepository

	// Background workers
	scheduler *worker.Scheduler
	updater   *updater.Updater
	stats     *installstats.Service
	// webhooks queues and manages outgoing webhooks; webhookDeliverer sends them.
	webhooks         *webhook.Service
	webhookDeliverer *webhook.Deliverer
	// webhookDeliveryOn is true when a scheduled job will send queued webhooks.
	webhookDeliveryOn bool
	// stopProbes ends the probe counter's flush goroutine (set by buildHandler).
	stopProbes func()

	// updatesSvc is the admin's Update now runner; Shutdown waits for it.
	updatesSvc *adminUpdates
	// backups is the admin's backup service; Shutdown waits for its runs.
	backups *adminBackups
	// admin hands late services (updates, backups) to the live admin and to the
	// retained deps (nil in a route-table build that failed). See admin_mount.go.
	admin *adminHolder
	// adminDeps is the admin's wiring, built once; every swap builds a new
	// Admin from a copy. adminMu guards it and serialises address changes.
	adminDeps   admin.Deps
	adminMu     sync.Mutex
	adminBundle atomic.Pointer[adminBundle]
	// sink is where s.routes registers; it lets the first admin mount land on
	// its own mux while the route table still records it.
	sink *routeSink
	// addrStore persists the admin address (nil without a database).
	addrStore addressStore
	addrState atomic.Pointer[admin.AddressState]
	// oldPrefixes are the addresses the admin used to have (in memory only).
	oldPrefixes atomic.Pointer[[]string]
	// newAdmin builds an Admin; nil means admin.New. Tests inject failures.
	newAdmin func(admin.Deps) (*admin.Admin, error)
	// updaterProblem says why updater is nil, when it is.
	updaterProblem string
	// importManager runs the admin's WordPress import (nil without a database).
	importManager *importjob.Manager

	// pluginStatusFn overrides the runtime's statuses (tests).
	pluginStatusFn func() []pluginhost.Status

	// plugins loads the WASM plugins from the plugin directory and
	// registers their hook handlers on s.hooks.
	plugins *pluginhost.Runtime
	// pluginDB is the runtime's database service (nil before plugins are set up).
	pluginDB *pluginhost.PluginDB

	// hooks is the plugin action/filter engine. Created at the top of
	// setupDependencies so services built later (entity, lead) can capture
	// it; its lifecycle context is set in Start.
	hooks *hooks.Engine

	// renderEngine renders the public site from the Go theme. nil unless
	// the theme loaded; nil only after a theme load failure, which fails Start.
	renderEngine *render.Engine
	// renderData backs the engine's recent/terms/menu/srcset funcs.
	renderData *renderData

	// packages installs plugin and theme packages and applies them live.
	packages *packages.Manager
	// directory feeds plugin_directory sections (see pluginDirectory).
	directory     *registryDirectory
	directoryOnce sync.Once
	// preview signs and verifies theme-preview cookies (nil without packages).
	preview *themePreview

	// pluginSettings persists per-plugin settings (hatamoto_plugin_settings).
	pluginSettings *pluginhost.SettingsStore

	// pluginSettingsStore is the settings API's view of pluginSettings
	// (an interface so handlers are testable without a database).
	pluginSettingsStore pluginSettingsStore

	// Lifecycle context of background work. Cancelled by Shutdown.
	lifecycleCtx    context.Context
	lifecycleCancel context.CancelFunc

	// pageRuntime is the live page-cache on/off and lifetime; renderHandlers
	// are the public handlers, kept for the preloader. Both nil until the
	// render routes are mounted.
	pageRuntime    *pageCacheRuntime
	renderHandlers *renderHandlers
	pageCache      *pageCacheService

	// renderErr is a failed theme load with rendering enabled; Start returns it.
	renderErr error

	// smokeServer is the supervisor's loopback health probe (smoke_listener.go).
	smokeServer *http.Server
	smokeMu     sync.Mutex // one smoke pass at a time
}

// New creates a new server instance.
func New(cfg *config.Config, db *database.DB, logger *slog.Logger) *Server {
	// Hydrate the package-level canonical slug from validated config so
	// adapters can read it without a Server-struct hop.
	// config.Validate() (called from app.Main before this point)
	// guarantees cfg.Hatamoto.CanonicalSlug is non-empty.
	canonicalTenantSlug = cfg.Hatamoto.CanonicalSlug
	middleware.SetTrustedProxies(cfg.Admin.TrustedProxies)
	applyTrustedCDN(cfg, db, logger)

	s := &Server{
		cfg:    cfg,
		db:     db,
		mux:    http.NewServeMux(),
		logger: logger,
	}

	s.setupInfrastructure()
	s.setupDependencies()
	s.setupRender()
	s.setupPackages()
	s.setupRoutes()
	s.registerNotFound()
	s.router = s.qaWrap(wrapFault(s.buildHandler()))

	return s
}

// applyTrustedCDN turns on the HATAMOTO_TRUSTED_CDN preset: the persisted (or
// built-in) edge ranges become the CDN trust set and the log ingest uses the
// same set to spot edge addresses. Preset off touches nothing. No request-path
// work: the list is loaded once here and swapped in memory afterwards.
func applyTrustedCDN(cfg *config.Config, db *database.DB, logger *slog.Logger) {
	if cfg.Admin.TrustedCDN != "cloudflare" {
		return
	}
	var persist agents.RangePersister
	if db != nil {
		persist = agents.NewRangeRepository(db)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	set := agents.LoadCDN(ctx, persist)
	middleware.SetTrustedCDN(set)
	agents.SetLogEdgeCheck(middleware.IsTrustedCDNIP)
	logger.Info("trusted CDN preset on: reading the visitor address from CF-Connecting-IP behind Cloudflare edge ranges",
		slog.Int("ranges", len(set)), slog.Bool("refresh", !agentRangesOff()))
}

// Handler is the fully wired HTTP handler (site, API, MCP), for callers that
// drive the server without listening (tests).
func (s *Server) Handler() http.Handler { return s.router }

// csrfKey is the key of the API cookie CSRF token. It is derived from the
// server secret under its own label (distinct from the admin's), or random per
// process when no secret is set, so tokens then die on restart. Computed lazily
// so test Server literals need no setup.
func (s *Server) csrfKey() []byte {
	s.apiCSRFKeyOnce.Do(func() {
		if s.cfg == nil || s.cfg.Secret == "" {
			k := make([]byte, 32)
			_, _ = rand.Read(k)
			s.apiCSRFKeyVal = k
			return
		}
		m := hmac.New(sha256.New, []byte(s.cfg.Secret))
		m.Write([]byte("hatamoto/api/csrf"))
		s.apiCSRFKeyVal = m.Sum(nil)
	})
	return s.apiCSRFKeyVal
}

// setupInfrastructure initializes the in-process cache.
func (s *Server) setupInfrastructure() {
	s.infra = NewInfrastructure(s.logger)
}

// setupDependencies initializes services and repositories.
func (s *Server) setupDependencies() {
	// Hooks engine first: later services (entity events) and plugins
	// register against it.
	s.hooks = hooks.New(s.logger)

	// Repositories
	s.userRepo = user.NewPostgresRepository(s.db)

	// Entity repositories
	schemaRepo := entity.NewPostgresSchemaRepository(s.db)
	entityRepo := entity.NewPostgresEntityRepository(s.db)
	s.entityRepo = entityRepo

	// Sessions: server-side admin session store.
	s.sessionStore = session.NewStore(s.db)
	s.userTokens = auth.NewUserTokens(s.db)

	// Site config: the singleton row holding branding/theme/settings. Hot
	// path (every public page render) so it caches with a 60s TTL inside the
	// store.
	s.siteConfig = siteconfig.NewStore(s.db, s.logger)

	// Services. tenantLookupAdapter reads name from hatamoto_site_config.
	s.authService = auth.NewService(s.userRepo, &tenantLookupAdapter{siteConfig: s.siteConfig}, s.sessionStore)
	if s.demoOn() {
		s.authService.NeverLock(s.cfg.Demo.LoginEmail)
	}
	s.entityService = entity.NewService(schemaRepo, entityRepo, s.logger)
	// The block and theme_part types are created by code, not a migration.
	if s.db != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		st, err := entity.EnsureReuseSchemas(ctx, s.db.Pool(), s.logger)
		cancel()
		if err != nil {
			s.logger.Error("builder reuse schemas", slog.String("error", err.Error()))
		}
		s.entityService.SetReuseStatus(st)
	}
	// v2 linking sync: every UpdateEntity now reindexes outbound links and,
	// on slug change, rewrites every <a data-entity-id> pointing at
	// this entity to its new canonical URL (ADR §4.3, Phase 1 §E).
	entity.SetURLRegistry(s.entityService.URLRegistry())
	// Schema settings feed the sitemap structure; drop cached sitemaps when a
	// schema changes.
	// URL patterns live in schema settings, so rendered pages go too.
	s.entityService.SetOnSchemaChange(func() {
		s.invalidateSitemaps(context.Background())
		s.invalidateRendered(context.Background())
	})
	s.entityService.SetLinkSync(&entityLinkSyncAdapter{db: s.db, urls: s.entityService.URLRegistry(), changed: s.invalidateRendered})
	s.entityService.SetPathObserver(s.onEntityMoved)
	s.entityService.SetSchemaPatternObserver(s.onSchemaPatternChanged)
	s.entityService.SetLiveObserver(s.onEntitiesLive)
	// {year}/{month}/{day} URL tokens are read in the site time zone.
	s.entityService.URLRegistry().SetTimezone(func(ctx context.Context) string {
		if cfg, err := s.siteConfig.Get(ctx); err == nil {
			return cfg.Timezone
		}
		return ""
	})

	// The public locale prefix is read from the site settings.
	s.entityService.URLRegistry().SetLocales(func(ctx context.Context) (*entity.LocaleSet, error) {
		cfg, err := s.siteConfig.Get(ctx)
		if errors.Is(err, siteconfig.ErrNotConfigured) {
			// No settings row means no locales were configured: a single-locale
			// site, which is a stable answer (unlike a failed read) and cacheable.
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		entries := make([]entity.LocaleEntry, len(cfg.Locales))
		for i, l := range cfg.Locales {
			entries[i] = entity.LocaleEntry{Tag: l.Tag, Segment: l.Segment, Name: l.Name, Hidden: l.Hidden}
		}
		set, err := entity.NewLocaleSet(cfg.Language, entries, cfg.PrefixAll, cfg.XDefaultLocale)
		if err != nil {
			s.logger.Warn("invalid locale settings; serving a single locale", "error", err)
			return nil, nil
		}
		return set, nil
	})

	// Media service
	mediaRepo := media.NewPostgresRepository(s.db)
	mediaConfig := media.DefaultConfig()
	if s.cfg.Server.MediaUploadDir != "" {
		mediaConfig.UploadDir = s.cfg.Server.MediaUploadDir
	}
	if s.cfg.Server.MediaBaseURL != "" {
		mediaConfig.BaseURL = s.cfg.Server.MediaBaseURL
	}
	if s.cfg.Server.MediaMaxSizeMB > 0 {
		mediaConfig.MaxSizeMB = s.cfg.Server.MediaMaxSizeMB
	}
	if s.demoOn() {
		mediaConfig.MaxTotalBytes = int64(s.cfg.Demo.MediaQuotaMB) * 1024 * 1024
	}
	s.mediaService = media.NewService(mediaRepo, mediaConfig, s.logger)
	s.mediaService.SetChangeObserver(s.invalidateRendered)

	// Menu service
	menuRepo := menu.NewPostgresRepository(s.db)
	s.menuService = menu.NewService(menuRepo, s.logger)
	s.menuService.SetChangeObserver(s.invalidateRendered) // plugin menus.* writes bypass the handlers

	// Section template service
	sectionTemplateRepo := sectiontemplate.NewPostgresRepository(s.db)
	s.sectionTemplateService = sectiontemplate.NewService(sectionTemplateRepo, s.logger)

	// Form service
	formRepo := form.NewPostgresRepository(s.db)
	s.formService = form.NewService(formRepo, s.logger)
	s.seoStore = seo.NewStore(s.db.Pool())
	spamStore := spam.NewStore(s.db.Pool())
	spamVerifier := spam.NewHTTPVerifier(spam.NewClient(), nil)
	s.spamStore, s.spamVerifier = spamStore, spamVerifier
	s.spam = &spam.Pipeline{
		Settings:  spamStore,
		Verifier:  spamVerifier,
		SecretKey: spam.SecretKey(s.cfg.Secret),
		TimingKey: spam.TimingKey(s.cfg.Secret),
	}

	// Email service. It is always built (even with no SMTP host) so saved
	// admin settings can bring it to life later; the resolver then picks saved
	// settings over the server's SMTP_* environment.
	s.mailStore = mailsettings.NewStore(s.db.Pool())
	emailUsable := false
	emailSvc, err := email.NewService(envEmailConfig(s.cfg.SMTP), s.logger)
	if err != nil {
		s.logger.Warn("failed to initialize email service", slog.String("error", err.Error()))
	} else {
		s.emailService = emailSvc
		emailSvc.SetSiteNameSource(s.siteName)
		s.formConfirmationSender = emailSvc
		bootCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		res := s.applyEmail(bootCtx)
		cancel()
		emailUsable = res.Source != emailSourceNone && emailSvc.IsConfigured()
	}
	warnUnsendableNotifications(context.Background(), s.logger, s.cfg.Environment,
		emailUsable, s.countFormsWithNotifyEmail)

	// Entity events and before_save hook (plugin host).
	s.entitySink = pluginhost.NewEntitySink(s.hooks, s.logger)
	s.entityService.SetEvents(s.entitySink)
	s.entitySink.SetLocaleLookup(func(ctx context.Context, id uuid.UUID) string {
		e, err := s.entityService.GetEntity(ctx, id)
		if err != nil {
			return ""
		}
		return e.Locale
	})
	s.registerRenderInvalidation()
	s.setupWebhooks()
	s.entityService.SetBeforeSave(pluginhost.BeforeSave(s.hooks))

	// API key service
	s.apiKeyService = auth.NewAPIKeyService(s.db, s.logger)
	s.apiKeyRateLimiter = middleware.NewAPIKeyRateLimiter()

	// Knowledge / segmentation services deleted in Phase 6 along with
	// the rest of the AI-Visibility platform — see notes above.

	// Redirect / 404 monitor service
	s.redirectService = redirect.NewService(s.db, s.logger)
	s.redirectService.SameHostOnly = s.demoOn()

	// Agent (AI crawler) hit capture. Page paths are resolved to entities in the
	// writer goroutine, off the request path.
	// Partitions are ensured here, not only by the optional scheduler job, so
	// hits never fall into the DEFAULT partition when HATAMOTO_WORKER=off.
	ensureCtx, cancelEnsure := context.WithTimeout(context.Background(), 15*time.Second)
	worker.NewAgentHitsPartitionWorker(s.db, 2, 3, s.logger).EnsureCurrentAndNext(ensureCtx)
	cancelEnsure()
	s.agentWriter = agents.NewWriter(agents.NewRepository(s.db), agents.Default(), s.newAgentResolver(), s.logger)
	// s.hooks exists here (created earlier in New); both observers are cheap
	// no-ops until a plugin subscribes to agent.hit / agent.first_crawl.
	s.agentFirstCrawl = agents.NewFirstCrawlTracker(agents.NewFirstCrawlRepository(s.db), agents.Default(),
		pluginhost.AgentFirstCrawlObserver(s.hooks), s.logger)
	hitObserver := pluginhost.AgentHitObserver(s.hooks)
	s.agentWriter.SetObserver(func(hits []agents.Hit) {
		s.agentFirstCrawl.OnHits(hits)
		hitObserver(hits)
	})
	s.agentLogHandlers = newAgentLogHandlers(s.cfg.AgentAnalytics, s.agentWriter, s.logger)

	// Initialize scheduler (optional - can be nil if running API-only mode).
	// The scheduler hosts core maintenance jobs only; plugin tickers run in
	// the plugin via the plugin.Starter contract.
	if s.cfg.Server.EnableWorker {
		s.scheduler = worker.NewScheduler(s.db, s.logger)
		if d := disabledWorkerJobs(); len(d) > 0 {
			s.logger.Info("worker jobs disabled by WORKER_DISABLE_JOBS", "jobs", d)
		}

		// Scheduled publishing — promotes entities in the 'scheduled' status
		// to 'published' once published_at falls due. Ticks every minute so a
		// post lands close to its slot rather than compounding with the
		// frontend's 300s ISR window. Takes the DB directly; the promotion is
		// a single indexed UPDATE ... RETURNING and deliberately does not go
		// through entity.UpdateStatus, which replaces a still-future published_at with NOW().
		publishWorker := worker.NewPublishWorker(s.db, s.logger)
		publishWorker.SetEvents(scheduledPublishSink{s: s, next: s.entitySink})
		if workerJobEnabled("publish") {
			s.scheduler.SetPublishWorker(publishWorker)
		}

		// Daily retention purge: expired sessions (previously no scheduled caller).
		if workerJobEnabled("retention_purge") {
			retention := worker.NewRetentionPurgeWorker(s.sessionStore, s.logger)
			retention.AddPurge("user tokens", s.userTokens.PurgeExpired)
			s.scheduler.SetRetentionPurgeWorker(retention)
		}

		// Agent hits: monthly partitions (2 ahead, >=90d retention) and the
		// 30-day client-IP truncation. With HATAMOTO_WORKER=off the migration's
		// pre-created partitions still accept writes until 2027-01.
		if workerJobEnabled("agent_hits_partition") {
			s.scheduler.SetAgentHitsPartitionWorker(worker.NewAgentHitsPartitionWorker(s.db, 2, 3, s.logger))
		}
		if workerJobEnabled("agent_hits_maintenance") {
			s.scheduler.SetAgentHitsMaintenanceWorker(worker.NewAgentHitsMaintenanceWorker(s.db, 5000, s.logger))
		}

		// Agent verification: one worker backs both jobs. Ranges are persisted
		// (migration 011) and loaded before the first verify; a failed download
		// keeps the old ranges and never touches hits.
		if workerJobEnabled("agent_ranges_refresh") || workerJobEnabled("agent_verify") {
			ranges := agents.NewRangeStore()
			persist := agents.NewRangeRepository(s.db)
			verifyWorker := agents.NewVerifyWorker(agents.NewVerifyRepository(s.db), agents.Default(),
				agents.NewVerifier(ranges, nil), agents.NewRefresher(agents.Default(), nil, ranges, persist),
				ranges, persist, s.logger)
			if workerJobEnabled("agent_ranges_refresh") {
				s.scheduler.SetAgentRangesRefreshWorker(verifyWorker)
			} else {
				verifyWorker.NoRangeRefresh()
			}
			if workerJobEnabled("agent_verify") {
				s.scheduler.SetAgentVerifyWorker(verifyWorker)
			}
		}

		// Crawler hits recorded without a page get linked to it by path, daily.
		if workerJobEnabled(attributionBackfillJob) {
			if err := s.scheduler.ScheduleJitter(attributionBackfillJob, 24*time.Hour, time.Hour, 10*time.Minute, s.runAttributionBackfillJob); err != nil {
				s.logger.Error("agent attribution backfill not scheduled", "error", err)
			}
		}

		// Batched prune of high-churn tables (404 log 90d).
		if workerJobEnabled("prune") {
			s.scheduler.SetPruneWorker(worker.NewPruneWorker(s.db, 5000, s.logger))
		}

		// Entity-version retention: coalesce identical-consecutive versions and
		// keep the newest 20 per entity.
		if s.entityRepo != nil && workerJobEnabled("entity_versions") {
			s.scheduler.SetEntityVersionWorker(worker.NewEntityVersionWorker(s.entityRepo, 20, s.logger))
			if s.demoOn() {
				s.scheduler.SetEntityVersionInterval(10 * time.Minute)
			}
		}

		s.scheduleWebhooks()
	}
	s.logWebhookDelivery()

	// Analytics: pageviews and AI referrals, rolled up daily.
	s.analyticsService = analytics.NewService(analytics.NewPostgresRepository(s.db), s.logger)
	s.analyticsHandlers = analytics.NewHandlers(s.analyticsService)
	s.robotsPolicy = agents.NewRobotsRepository(s.db)
	s.agentAdminHandlers = agents.NewAdminHandlers(agents.NewAdminStore(s.db), agents.Default(),
		s.agentWriter.Stats)

	// Taxonomy (Phase 2) — first-class tags + categories across all
	// entity schemas, backed by hatamoto_terms + hatamoto_entity_terms
	// (migration 113). Pure additive: existing JSONB attributes.tags
	// remain valid; the new store runs in parallel.
	taxonomyRepo := taxonomy.NewPostgresRepository(s.db)
	s.taxonomyService = taxonomy.NewService(taxonomyRepo, s.logger)
	s.taxonomyService.SetMoveObserver(s.onTermsMoved)
	s.taxonomyService.SetChangeObserver(s.invalidateRendered)
	s.taxonomyHandlers = taxonomy.NewHandlers(s.taxonomyService, s.logger)

	// Bylines come from people (person entities); hatamoto_authors is only the
	// rollback mirror the resolver keeps in step (internal/person). The store
	// and handlers stay for the deprecated /api/v1/authors only.
	s.authors = author.NewStore(s.db)
	s.authorHandlers = author.NewHandlers(s.authors, s.logger)
	s.authorHandlers.OnChange = func(r *http.Request) { s.invalidateRendered(r.Context()) }
	s.entityService.SetBylines(person.NewByline(s.db.Pool()))

	// Plugins load last; their hooks register on the engine.
	pluginhost.BindHostHooks(s.hooks)
	s.setupPlugins()
}

// buildHandler wraps the finished mux in the global middleware. It runs after
// setupRoutes: the mux is never modified once served.
func (s *Server) buildHandler() http.Handler {
	// First listed runs outermost.
	deps := s.newInterceptDeps()
	s.stopProbes = deps.probes.Stop
	var legacy legacySessions
	if s.sessionStore != nil {
		legacy = s.sessionStore
	}
	chain := []httpx.Middleware{
		// Security headers — applied first so every response includes them.
		s.securityHeadersMiddleware,
		// Logging and recovery
		middleware.RecoveryLogger(s.logger, s.redactAdminPath),
		middleware.RequestLogger(s.logger, s.redactAdminPath),
		requestIDMiddleware,
		expireLegacyCookies(legacy, s.logger),
		// Before the demo middlewares and the mux: it answers scanner probes and
		// signed-out admin requests with the standard 404, and nothing between it
		// and the mux may change a response.
		interceptMiddleware(deps),
	}
	if s.demoOn() {
		validate := s.validateSession
		if validate == nil {
			validate = s.authService.ValidateBearerSession
		}
		chain = append(chain, s.demoWriteLimit(validate), demoBanner) // innermost: it sees the page, not the headers added around it
	}
	return httpx.Chain(chain...)(s.dispatcher())
}

// securityHeadersMiddleware sets standard security headers on every response.
// CSP ships as Content-Security-Policy-Report-Only until CSP_ENFORCE=true is
// set (so operators can watch for violations before enforcing), HSTS is only
// added in production (never on local HTTP demo), and the whole thing can be
// short-circuited via SECURITY_HEADERS_DISABLED for local debugging.
func (s *Server) securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.Security.HeadersDisabled {
			next.ServeHTTP(w, r)
			return
		}

		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("X-XSS-Protection", "0")

		// Restrictive default — no browser feature this API needs.
		w.Header().Set("Permissions-Policy",
			"geolocation=(), camera=(), microphone=()")

		const csp = "default-src 'self'; script-src 'self' 'unsafe-inline'; " +
			"style-src 'self' 'unsafe-inline'; img-src 'self' data:; " +
			"font-src 'self'; connect-src 'self'; frame-ancestors 'none'; " +
			"object-src 'none'; base-uri 'self'; form-action 'self'"
		if s.cfg.Security.CSPEnforce {
			w.Header().Set("Content-Security-Policy", csp)
		} else {
			w.Header().Set("Content-Security-Policy-Report-Only", csp)
		}

		if s.cfg.Environment == "production" {
			w.Header().Set("Strict-Transport-Security",
				"max-age=31536000; includeSubDomains")
		}

		next.ServeHTTP(w, r)
	})
}

// mountOpsRoutes registers health, readiness and media. A "GET" pattern also
// serves HEAD, so HEAD never falls through to the render catch-all.
func mountOpsRoutes(r *httpx.Group, health, ready, media http.HandlerFunc) {
	r.HandleFunc("GET /health", health)
	r.HandleFunc("GET /ready", ready)
	r.HandleFunc("GET /media/{rest...}", media)
}

// setupRoutes configures all API routes.
//
// Pure orchestrator after Phase 4. Mounts root-level routes, then
// frames the /api/v1 hierarchy: rate-limited public auth + SES +
// /track, the AuthMiddleware-protected admin group, and the
// publicTenantMiddleware-fronted public CMS group. Inside each group
// it dispatches to the per-domain mount methods defined in
// server_core.go (CMS surface). Phase 6 deleted the matching
// server_platform.go (AI-Visibility surface) along with all 87 of its
// routes; what survives is core + plugin-mounted endpoints only.
func (s *Server) setupRoutes() {
	// Health check endpoints (no auth required)
	// Serve uploaded media files (public route)
	s.sink = &routeSink{mux: s.mux, forward: s.forwardToAdmin()}
	s.routes = httpx.NewGroup(s.sink, "")
	mountOpsRoutes(s.routes, s.handleHealth, s.handleReady, s.handleServeMedia)

	// Fixed catch-alls answer paths no core route owns with a JSON 404 (or
	// 405 when the path has a route under another method).
	notFound := s.unmatchedHandler()
	{
		// With render on, the page catch-alls (mountRenderRoutes) serve GET,
		// so only the other methods get a catch-all here.
		methods := catchAllMethods
		if s.renderEngine != nil {
			methods = methods[1:]
		}
		mountCatchAll(s.routes, "", notFound, methods...)
	}

	// API v1 routes
	{
		r := s.routes.Group("/api/v1")
		// Unauthed: public auth + analytics /track.
		s.mountCoreUnauthedAPIRoutes(r)

		// Protected routes — AuthMiddleware applied once at the group
		// level so each mount method below can assume an authenticated
		// caller (sub-routers still apply RequireRole where stricter
		// gating is needed).
		{
			// X-API-Key requests are authenticated as keys and restricted to
			// the fail-closed allowlist in middleware.AuthOrAPIKey; everything
			// else goes through the session auth unchanged.
			validate := s.validateSession
			if validate == nil {
				validate = s.authService.ValidateBearerSession
			}
			validateCookie := s.validateAPICookie
			if validateCookie == nil {
				validateCookie = s.authService.ValidateAPICookieSession
			}
			protected := []httpx.Middleware{middleware.AuthOrAPIKey(s.apiKeyService, s.apiKeyRateLimiter,
				middleware.AuthMiddleware(validate, validateCookie, s.cfg.EnableCookieAuth))}
			// Synchronizer CSRF for cookie-authenticated mutations (bearer-token
			// requests are exempt inside the middleware). Only wired when cookie-auth
			// is enabled, so a backend-only deploy can't 403 the legacy frontend.
			if s.cfg.EnableCookieAuth {
				protected = append(protected, middleware.CSRF(s.csrfKey()))
			}
			if s.demoOn() {
				protected = append(protected, s.demoAPIGuard)
			}
			r := r.Group("", protected...)

			s.mountCoreProtectedRoutes(r)
		}
		mountCatchAll(r, catchAllAdminPrefix, notFound, catchAllMethods...)

		// Public routes (no auth required)
		s.mountPublicAPI(r.Group("/public"), catchAllPublicPrefix, notFound)
	}

	// PUBLIC_API_PREFIX: the same public API (and the pageview beacon) at an
	// extra path. The default mounts above stay, because the admin shows them.
	if p := s.cfg.PublicSite.APIPrefix; p != "" {
		g := s.routes.Group(p)
		s.mountPublicAPI(g, p, notFound)
		s.mountTrack(g)
	}

	// MCP endpoint for AI agents (API key Bearer auth, scoped per tool).
	s.mountMCP(s.routes)

	// Public plugin routes (/p/<plugin>/...): fixed catch-all, rate limited.
	s.mountPluginPublicRoutes(s.routes)

	// Go admin UI (/admin/*, incl. a fallback for screens still on Next).
	s.mountAdminRoutes(s.routes)

	// Go-rendered public site (root HTML routes). Registered last among the
	// routes; /api, /media, /health and /ready keep precedence.
	s.mountRenderRoutes(s.routes)
}

// Start begins listening for HTTP requests.
func (s *Server) Start() error {
	if s.renderErr != nil {
		return s.renderErr
	}
	s.httpServer = &http.Server{
		Addr:         s.cfg.Server.Address(),
		Handler:      s.router,
		ReadTimeout:  s.cfg.Server.ReadTimeout,
		WriteTimeout: s.cfg.Server.WriteTimeout,
		IdleTimeout:  s.cfg.Server.IdleTimeout,
	}

	// Establish the server's lifecycle context. Plugin goroutines watch
	// its Done channel; Shutdown cancels it to signal clean exit.
	s.lifecycleCtx, s.lifecycleCancel = context.WithCancel(context.Background())

	// Start background scheduler if it was wired up (the in-process worker).
	// Constructs were happening in setupDependencies but the loops never
	// fired — pre-existing oversight surfaced when the lead-score and
	// magic-token-cleanup workers were added.
	if s.scheduler != nil {
		s.scheduler.Start(s.lifecycleCtx)
	}
	// The CDN edge list refreshes from its own goroutine, not a worker job,
	// so a replica with the worker off still tracks the published ranges.
	if s.cfg.Admin.TrustedCDN == "cloudflare" && s.db != nil && !agentRangesOff() {
		refresher := agents.NewCDNRefresher(nil, agents.NewRangeRepository(s.db))
		go agents.RunCDNRefresh(s.lifecycleCtx, 24*time.Hour, refresher.Refresh, middleware.SetTrustedCDN)
	}
	s.loadPageCache(s.lifecycleCtx)
	s.warnIfAdminAddressServed(s.lifecycleCtx)
	if s.emailService != nil {
		go s.runEmailConvergence(s.lifecycleCtx, emailConvergeEvery)
	}

	s.analyticsService.Start()
	if s.packages != nil {
		go s.packages.Run(s.lifecycleCtx, s.packageReconcileEvery())
	}
	if s.db != nil {
		go s.runContentEpochPoller(s.lifecycleCtx)
	}

	// Crawler hits that predate page attribution are linked once at start, on
	// every install (independent of the worker), in the background.
	if s.db != nil && s.agentWriter != nil && workerJobEnabled(attributionBackfillJob) {
		go func() { _ = s.runAttributionBackfillJob(s.lifecycleCtx) }()
	}

	// Optional Caddy access-log tailer: dark unless AGENT_LOG_INGEST is on and
	// a path is set. Read-only; the file need not exist yet.
	if s.cfg.AgentAnalytics.LogIngestEnabled && s.cfg.AgentAnalytics.CaddyLogPath != "" && s.agentWriter != nil {
		s.agentLogTailer = agents.StartTailer(s.lifecycleCtx, s.cfg.AgentAnalytics.CaddyLogPath, s.agentWriter, s.logger)
	}

	// Plugin hook actions run under the same lifecycle context.
	s.hooks.SetLifecycleContext(s.lifecycleCtx)

	// The supervisor's loopback probe, only when it asked for one.
	s.startSmokeFromEnv(os.Getenv("HATAMOTO_SMOKE_ADDR"), os.Getenv("HATAMOTO_SMOKE_TOKEN"))

	// Channel to listen for errors
	errChan := make(chan error, 1)

	// Start server in goroutine
	go func() {
		s.logger.Info("starting server", slog.String("addr", s.cfg.Server.Address()))
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errChan <- err
		}
	}()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errChan:
		return fmt.Errorf("server error: %w", err)
	case sig := <-quit:
		s.logger.Info("received shutdown signal", slog.String("signal", sig.String()))
	}

	return s.Shutdown()
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	s.logger.Info("shutting down server")

	// Signal scheduler goroutines to exit.
	if s.lifecycleCancel != nil {
		s.lifecycleCancel()
	}
	// A running preload watches the lifecycle context; wait for it to exit.
	s.stopPreload()

	// Stop background scheduler before HTTP server so in-flight jobs finish cleanly.
	// Scheduler.Stop() closes its stopCh and waits on its WaitGroup.
	if s.scheduler != nil {
		s.logger.Info("stopping scheduler")
		s.scheduler.Stop()
		s.logger.Info("scheduler stopped")
	}

	s.stopSmoke(ctx)
	if s.stopProbes != nil {
		s.stopProbes()
	}

	// An Update now run watches the lifecycle context; it must be over before
	// the caller closes the pool its status write needs.
	if s.updatesSvc != nil {
		s.updatesSvc.Wait(ctx)
	}
	if s.backups != nil {
		s.backups.Wait(ctx)
	}

	// Shutdown HTTP server
	// A drain timeout must not skip the flushes below: the caller closes the
	// DB pool once Shutdown returns, so the error is returned at the end.
	var shutdownErr error
	if err := s.httpServer.Shutdown(ctx); err != nil {
		s.logger.Warn("http server drain incomplete", slog.String("error", err.Error()))
		shutdownErr = fmt.Errorf("server shutdown error: %w", err)
	}

	// Same for in-flight plugin hook actions, then free the modules.
	s.hooks.Wait()
	if s.plugins != nil {
		s.plugins.Close(context.Background())
	}

	// Stop the WordPress import jobs: an interrupted run stays resumable.
	if s.importManager != nil {
		s.importManager.Close()
	}

	// Stop redirect logger
	if s.redirectService != nil {
		s.redirectService.Close()
	}

	// The tailer feeds the writer, so it must stop first (lifecycleCtx is
	// already cancelled above).
	if s.agentLogTailer != nil {
		s.agentLogTailer.Wait()
	}

	// Stop the agent hit writer after httpServer.Shutdown so late handler
	// enqueues find a live (or closed, never panicking) writer; flushes what is buffered.
	if s.agentWriter != nil {
		s.agentWriter.Close()
	}
	// Flush buffered pageviews and AI referrals.
	if s.analyticsService != nil {
		flushCtx, cancelFlush := context.WithTimeout(context.Background(), 5*time.Second)
		s.analyticsService.Close(flushCtx)
		cancelFlush()
	}
	// After the writer: its final flush may still feed the tracker.
	if s.agentFirstCrawl != nil {
		s.agentFirstCrawl.Close()
	}

	// Shutdown infrastructure (cache, tracing)
	if s.infra != nil {
		if err := s.infra.Shutdown(); err != nil {
			s.logger.Warn("infrastructure shutdown error", slog.String("error", err.Error()))
		}
	}

	if shutdownErr != nil {
		return shutdownErr
	}
	s.logger.Info("server stopped")
	return nil
}

// requestIDMiddleware adds a unique request ID to each request.
func requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := r.Header.Get("X-Request-ID")
		if requestID == "" {
			requestID = generateRequestID()
		}
		w.Header().Set("X-Request-ID", requestID)
		next.ServeHTTP(w, r)
	})
}

func generateRequestID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// Fallback should never happen; crypto/rand reads from OS entropy.
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// unmatchedHandler answers a request no route took, behind a catch-all.
// The catch-alls match every path, so ServeMux's own 405 never runs for them:
// when the request's path has a core route under another method, answer 405
// with Allow (as chi did); otherwise 404.
func (s *Server) unmatchedHandler() http.Handler {
	methods := append([]string{http.MethodGet, http.MethodHead}, catchAllMethods[1:]...)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var allow []string
		for _, m := range methods {
			if m == r.Method {
				continue
			}
			probe := r.Clone(r.Context())
			probe.Method = m
			if _, pattern := s.mux.Handler(probe); pattern != "" && !s.isCatchAll(pattern) {
				allow = append(allow, m)
			}
		}
		if len(allow) == 0 {
			notFoundHandler(w, r)
			return
		}
		w.Header().Set("Allow", strings.Join(allow, ", "))
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	})
}

// isCatchAll reports whether pattern is one of the fixed catch-alls (404
// handlers and the render pages), which match paths that no core route owns.
func (s *Server) isCatchAll(pattern string) bool {
	_, path, _ := strings.Cut(pattern, " ")
	if p := s.cfg.PublicSite.APIPrefix; p != "" {
		switch path {
		case p, p + "/", p + "/{file}", p + "/{rest...}":
			return true
		}
	}
	switch path {
	case "/", "/{file}", "/{rest...}", catchAllAdminPrefix, catchAllPublicPrefix,
		catchAllPublicPrefix + "/{file}", catchAllAdminPrefix + "/{rest...}", catchAllPublicPrefix + "/{rest...}":
		return true
	}
	return false
}

// mountPublicAPI mounts the public API on r (the group at its prefix): the
// routes outside the tenant middleware, the public CMS routes, the sitemap
// children and the JSON 404 catch-all. prefix is the group's path.
func (s *Server) mountPublicAPI(r *httpx.Group, prefix string, notFound http.Handler) {
	// Routes that bypass publicTenantMiddleware:
	// agent-hit ingest. (The platform's share-token report
	// viewer mount was deleted along with the rest of the
	// AI-Visibility surface in Phase 6.)
	s.mountCorePublicMetaRoutes(r)

	// Public CMS routes - resolve tenant from host or header.
	s.mountCorePublicCMSRoutes(r.Group("", s.publicTenantMiddleware))

	// ServeMux wildcards span whole segments, so "sitemap-{sub}.xml" is
	// matched as "{file}": sitemap children go to the sitemap handler,
	// any other single segment is a 404.
	subSitemap := s.publicTenantMiddleware(http.HandlerFunc(s.handlePublicSubSitemap))
	r.HandleFunc("GET /{file}", func(w http.ResponseWriter, req *http.Request) {
		if _, ok := sitemapChild(req.PathValue("file")); ok {
			subSitemap.ServeHTTP(w, req)
			return
		}
		notFound.ServeHTTP(w, req)
	})
	mountCatchAll(r, prefix, notFound, catchAllMethods...)
}
