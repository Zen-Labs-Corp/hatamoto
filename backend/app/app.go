// Package app is the hatamoto entry point: `serve` and the operator commands.
// cmd/hatamoto calls Main.
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"runtime/debug"
	"strings"

	"hatamoto.dev/core/internal/adminpagestate"
	"hatamoto.dev/core/internal/adminpath"
	"hatamoto.dev/core/internal/agentpolicystate"
	"hatamoto.dev/core/internal/auditstate"
	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/authstate"
	"hatamoto.dev/core/internal/authzstate"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/editionstate"
	"hatamoto.dev/core/internal/media"
	"hatamoto.dev/core/internal/middleware"
	"hatamoto.dev/core/internal/modhost"
	"hatamoto.dev/core/internal/offsite"
	"hatamoto.dev/core/internal/pluginhost"
	"hatamoto.dev/core/internal/purgestate"
	"hatamoto.dev/core/internal/reviewstate"
	"hatamoto.dev/core/internal/runenv"
	"hatamoto.dev/core/internal/server"
	"hatamoto.dev/core/internal/siteid"
	"hatamoto.dev/core/internal/user"
)

type command int

const (
	cmdServe command = iota
	cmdMigrate
	cmdMigrateStatus
	cmdUserCreate
	cmdUserResetPassword
	cmdUserReset2FA
	cmdUserSignOutAll
	cmdPluginsList
	cmdSanitizeHTMLSections
	cmdModule
	cmdRoutes
	cmdVersion
	cmdHealthcheck
	cmdSupervise
	cmdUpdate   // apply, rollback (update.go)
	cmdOps      // entity, redirect, analytics, export, import, backup (ops.go)
	cmdPackages // keys, theme, plugin, package (packages.go)
	cmdBuilder  // builder convert (builder_convert.go)
	cmdLocales  // locales convert (locales_convert.go)
	cmdAdminURL // admin-url, admin-url set|random (admin_url.go)
	cmdDoctor   // doctor json (doctor_json.go)
	cmdMedia    // media migrate, sweep, adopt (media_cmd.go)
	cmdStatic   // static export (static_cmd.go)
	cmdHelp
)

const usage = `usage: hatamoto <command>

commands:
  serve                            migrate the database, then run the server (site, API, admin, worker; HATAMOTO_WORKER=off disables the worker)
  migrate [up]                     apply pending database migrations
  migrate status                   list migrations and whether they are applied
  user create --email E [--name N] [--role owner|editor|author|contributor|reviewer|read-only] [--password P | --password-stdin]
                                   create a user (a temporary password is printed unless one is given)
  user reset-password --email E [--password P | --password-stdin]
                                   set a new password (temporary one printed unless given), unlock, sign out
  user reset-2fa --email E         remove the authenticator app, passkeys and recovery codes of a user (password sign-in only afterwards)
  user sign-out-all --yes          sign out every user and revoke every API key, unused invite/reset link and preview link (passwords are kept)
  admin-url                        print the admin address
  admin-url set PATH [--i-understand-weak]
                                   store a new admin address (weak ones need the flag; applies after a restart)
  admin-url random                 store a new random admin address (applies after a restart)
  routes                           list the core HTTP routes
  plugins list                     list the plugins in HATAMOTO_PLUGIN_DIR (the development override)
` + mediaUsage + "\n" + staticUsage + `
  sanitize-html-sections [--dry-run]
` + opsUsage + "\n" + packagesUsage + "\n" + builderUsage + "\n" + localesUsage + "\n" + doctorUsage + `
  supervise                        run serve as a child, switch to verified releases in $HATAMOTO_DATA/releases and roll back when one fails its health gate
  update apply --file F --record R [--sig S]
                                   verify a release binary against its signed record, stage it and switch to it (no download)
  update rollback                  switch back to the previous release
  update status                    show the update mode, channel, pin and the last check
  update check [--dry-run]         check the channel now (installs under policy; --dry-run only decides)
  update pin vX.Y | unpin          stay on one minor line (security releases still apply) or release the pin
  update apply vX.Y.Z [--force] [--no-backup]  download, verify and install a named release (majors too; --force skips the plugin check; --no-backup skips the database backup)
  healthcheck                      exit 0 when the local server answers /health (Docker HEALTHCHECK)
  version [--json]                 print version and commit (--json adds sdk and schema head)

environment: DATABASE_URL (required); DATABASE_DIRECT_URL (optional, bypasses a connection pooler for migrations,
backups and locks);
DATABASE_UNIX_SOCKET=off stops a loopback DATABASE_URL from being tried over the local unix socket first;
DATABASE_SOCKET_DIRS (colon- or comma-separated) replaces the socket directories searched (default
/var/run/postgresql:/run/postgresql); HATAMOTO_SECRET (or its old name JWT_SECRET) is generated and
persisted in $HATAMOTO_SECRETS_DIR (default ./data/secrets) when not set.`

// usageText is the help text plus the commands modules registered, if any.
func usageText() string {
	if mu := modhost.CommandUsage(); mu != "" {
		return usage + "\n\nmodule commands:\n" + strings.TrimRight(mu, "\n")
	}
	return usage
}

// parseArgs maps CLI arguments (without argv[0]) to a command.
func parseArgs(args []string) (command, error) {
	bad := fmt.Errorf("unknown command %q\n%s", args, usageText())
	if len(args) == 0 {
		return cmdHelp, fmt.Errorf("%s", usageText())
	}
	if packageCommands[args[0]] {
		if len(args) < 2 {
			return cmdHelp, bad
		}
		return cmdPackages, nil
	}
	if opsCommands[args[0]] {
		if len(args) < 2 {
			return cmdHelp, bad
		}
		return cmdOps, nil
	}
	switch args[0] {
	case "builder":
		if len(args) >= 2 && args[1] == "convert" {
			return cmdBuilder, nil
		}
	case "doctor":
		if len(args) == 2 && args[1] == "json" {
			return cmdDoctor, nil
		}
	case "locales":
		if len(args) >= 2 && args[1] == "convert" {
			return cmdLocales, nil
		}
	case "serve":
		if len(args) == 1 {
			return cmdServe, nil
		}
	case "supervise":
		if len(args) == 1 {
			return cmdSupervise, nil
		}
	case "update":
		if _, err := parseUpdateArgs(args[1:]); err == nil {
			return cmdUpdate, nil
		}
	case "migrate":
		switch {
		case len(args) == 1, len(args) == 2 && args[1] == "up":
			return cmdMigrate, nil
		case len(args) == 2 && args[1] == "status":
			return cmdMigrateStatus, nil
		}
	case "user":
		if len(args) >= 2 && args[1] == "create" {
			return cmdUserCreate, nil
		}
		if len(args) >= 2 && args[1] == "reset-password" {
			return cmdUserResetPassword, nil
		}
		if len(args) >= 2 && args[1] == "reset-2fa" {
			return cmdUserReset2FA, nil
		}
		if len(args) >= 2 && args[1] == "sign-out-all" {
			return cmdUserSignOutAll, nil
		}
	case "admin-url":
		if _, err := parseAdminURLArgs(args[1:]); err == nil {
			return cmdAdminURL, nil
		}
	case "media":
		if len(args) >= 2 && args[1] == "migrate" {
			if _, err := parseMediaMigrateArgs(args[2:]); err == nil {
				return cmdMedia, nil
			}
		}
		if len(args) >= 2 && args[1] == "sweep" {
			if _, err := parseMediaSweepArgs(args[2:]); err == nil {
				return cmdMedia, nil
			}
		}
		if len(args) >= 2 && args[1] == "adopt" {
			if _, err := parseMediaAdoptArgs(args[2:]); err == nil {
				return cmdMedia, nil
			}
		}
	case "static":
		if len(args) >= 2 && args[1] == "export" {
			if _, err := parseStaticArgs(args[2:]); err == nil {
				return cmdStatic, nil
			}
		}
	case "routes":
		if len(args) == 1 {
			return cmdRoutes, nil
		}
	case "plugins":
		if len(args) == 2 && args[1] == "list" {
			return cmdPluginsList, nil
		}
	case "version":
		if len(args) == 1 || (len(args) == 2 && args[1] == "--json") {
			return cmdVersion, nil
		}
	case "healthcheck":
		if len(args) == 1 {
			return cmdHealthcheck, nil
		}
	case "help", "-h", "--help":
		return cmdHelp, nil
	case "sanitize-html-sections":
		if len(args) == 1 || (len(args) == 2 && args[1] == "--dry-run") {
			return cmdSanitizeHTMLSections, nil
		}
	default:
		// Core words always win; a module command only fills a gap.
		if moduleCommand(args) {
			return cmdModule, nil
		}
	}
	return cmdHelp, bad
}

// printPlugins writes "name version sdk" per plugin found in dir, with the
// reason after a tab when its manifest is unusable. It reads manifests only;
// nothing is compiled or run.
func printPlugins(w io.Writer, dir string) {
	sts, err := pluginhost.ListDir(dir)
	if err != nil {
		fmt.Fprintf(w, "cannot read %s: %v\n", dir, err)
		return
	}
	for _, st := range sts {
		if st.Error != "" {
			fmt.Fprintf(w, "%s\t(%s)\n", st.Name, st.Error)
			continue
		}
		m := st.Manifest
		fmt.Fprintf(w, "%s %s %s\n", m.Name, m.Version, m.SDK)
	}
}

// printRoutes writes "PATTERN<TAB>handler" per registered core route.
func printRoutes(w io.Writer) error {
	table, err := server.RouteTable()
	if err != nil {
		return err
	}
	for _, r := range table {
		fmt.Fprintf(w, "%s\t%s\n", r.Pattern, r.Handler)
	}
	return nil
}

func newLogger() *slog.Logger {
	logLevel := slog.LevelInfo
	if os.Getenv("LOG_LEVEL") == "debug" {
		logLevel = slog.LevelDebug
	}
	var handler slog.Handler
	if os.Getenv("ENVIRONMENT") == "production" {
		handler = slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel})
	} else {
		handler = slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel})
	}
	return slog.New(handler)
}

// Main runs hatamoto's server or one of its subcommands and exits on failure.
func Main() {
	// Registration by a wrapper module (edition, audit sink, cache purge, agent policy) happens in init; nothing registers after this.
	editionstate.Freeze()
	modhost.Freeze()
	modhost.SetSecretSource(serverSecret)
	adminpagestate.Freeze()
	auditstate.Freeze()
	authstate.Freeze()
	reviewstate.Freeze()
	authzstate.Freeze()
	purgestate.Freeze()
	agentpolicystate.Freeze()
	logger := newLogger()
	slog.SetDefault(logger)

	args := os.Args[1:]
	cmd, err := parseArgs(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if err := run(context.Background(), cmd, args, logger); err != nil {
		var ee exitError
		if errors.As(err, &ee) {
			os.Exit(ee.code)
		}
		logger.Error(err.Error())
		var oe operationalError
		if errors.As(err, &oe) {
			os.Exit(2)
		}
		os.Exit(1)
	}
}

func run(ctx context.Context, cmd command, args []string, logger *slog.Logger) error {
	switch cmd {
	case cmdHelp:
		fmt.Println(usageText())
		return nil
	case cmdVersion:
		if len(args) == 2 {
			return printVersionJSON(os.Stdout)
		}
		printVersion(os.Stdout)
		return nil
	case cmdHealthcheck:
		return healthcheck(http.DefaultClient, fmt.Sprintf("http://127.0.0.1:%s/health", getPort()))
	case cmdPluginsList:
		if dir := config.LoadPluginConfig().Dir; dir != "" {
			printPlugins(os.Stdout, dir)
		} else {
			fmt.Println("no HATAMOTO_PLUGIN_DIR set: plugins come from installed packages (Admin > Plugins, or `hatamoto package install`)")
		}
		return nil
	case cmdRoutes:
		return printRoutes(os.Stdout)
	case cmdSanitizeHTMLSections:
		dryRun := len(args) > 1 // parseArgs only admits --dry-run here
		return runSanitizeHTMLSections(ctx, os.Getenv("DATABASE_URL"), dryRun, os.Stdout)
	case cmdServe:
		return serve(ctx, logger)
	case cmdSupervise:
		return runSupervise(ctx, logger)
	case cmdUpdate:
		return runUpdate(ctx, args[1:], os.Stdout)
	case cmdModule:
		return runModule(ctx, args, logger)
	case cmdOps:
		return runOps(ctx, args, logger, os.Stdout)
	case cmdPackages:
		return runPackages(ctx, args, logger, os.Stdout)
	case cmdBuilder:
		return runBuilderConvert(ctx, os.Getenv("DATABASE_URL"), args[2:], os.Stdout)
	case cmdDoctor:
		return runDoctorJSON(ctx, os.Getenv("DATABASE_URL"), os.Stdout)
	case cmdMedia:
		return runMedia(ctx, os.Getenv("DATABASE_URL"), args, os.Stdout)
	case cmdStatic:
		return runStatic(ctx, args[2:], os.Stdout)
	case cmdLocales:
		return runLocalesConvert(ctx, os.Getenv("DATABASE_URL"), args[2:], os.Stdout, os.Stderr)
	}

	// Refuse before opening the database.
	if cmd == cmdUserSignOutAll {
		yes, err := parseSignOutFlags(args[2:])
		if err != nil {
			return err
		}
		if !yes {
			fmt.Fprintln(os.Stderr, signOutRefusal)
			return exitError{1}
		}
	}

	// Remaining commands only need the database.
	var (
		uf  *userFlags
		err error
	)
	switch cmd {
	case cmdUserCreate:
		if uf, err = parseUserFlags("create", args[2:], true); err != nil {
			return err
		}
	case cmdUserResetPassword:
		if uf, err = parseUserFlags("reset-password", args[2:], false); err != nil {
			return err
		}
	case cmdUserReset2FA:
		if uf, err = parseUserFlags("reset-2fa", args[2:], false); err != nil {
			return err
		}
	}
	db, err := openDB(ctx)
	if err != nil {
		return err
	}
	defer db.Close()
	switch cmd {
	case cmdMigrate:
		if err := migrateUpDirect(ctx, db, logger); err != nil {
			return err
		}
		return migrateModulesDirect(ctx, db, logger)
	case cmdMigrateStatus:
		if err := migrateStatusDirect(ctx, db, os.Stdout); err != nil {
			return err
		}
		return migrateModulesStatus(ctx, db, os.Stdout)
	case cmdAdminURL:
		base := adminBaseURL(db.Pool(), os.Getenv)
		site := loadSQLSite(ctx, db.Pool(), base(ctx))
		if cfg, err := config.Load(); err == nil {
			site.apiPrefix = cfg.PublicSite.APIPrefix
		}
		return runAdminURL(ctx, adminpath.NewStore(db.Pool()), site, args[1:], os.Getenv, base, os.Stdout)
	case cmdUserCreate:
		return runUserCreate(ctx, db, uf, os.Stdin, os.Stdout)
	case cmdUserResetPassword:
		return runUserResetPassword(ctx, db, uf, os.Stdin, os.Stdout)
	case cmdUserReset2FA:
		return runUserReset2FA(ctx, db, uf, os.Stdout)
	case cmdUserSignOutAll:
		return runUserSignOutAll(ctx, db, os.Stdout)
	}
	return fmt.Errorf("unhandled command %d", cmd)
}

// openDB connects using DATABASE_URL and the other database settings only.
func openDB(ctx context.Context) (*database.DB, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("load configuration: %w", err)
	}
	if cfg.Database.URL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}
	db, err := database.New(ctx, cfg.Database)
	if err != nil {
		return nil, fmt.Errorf("connect to database: %w", err)
	}
	return db, nil
}

// serve is the one-process boot: secrets, migrations, then the server (which loads the plugins)
// (which starts the background worker and renders the site).
func serve(ctx context.Context, logger *slog.Logger) error {
	applyDefaultMemLimit(logger)
	if os.Getenv("HATAMOTO_SMOKE_ADDR") != "" {
		// Under the supervisor: count render errors for its smoke endpoint.
		logger = slog.New(server.NewCountingHandler(logger.Handler()))
		slog.SetDefault(logger)
	}
	secretsDir := os.Getenv("HATAMOTO_SECRETS_DIR")
	if secretsDir == "" {
		secretsDir = "./data/secrets"
	}
	if err := ensureSecrets(secretsDir, logger); err != nil {
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}
	for _, w := range cfg.Warnings {
		logger.Warn("configuration: " + w)
	}
	if cfg.RunMode == runenv.ModeStaging {
		logger.Warn(runenv.Summary())
		warnStagingMedia(logger, os.Getenv)
	}
	// Missing or invalid object storage settings stop the start; an
	// unreachable bucket does not (no network call is made here).
	if _, err := media.ConfigFromServer(cfg.Server, os.Getenv, nil); err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}

	db, err := database.New(ctx, cfg.Database)
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	defer db.Close()
	// Modules see the pool until shutdown; cleared after services stop, before Close.
	defer publishModulePool(db.Pool())()
	logger.Info("connected to database")
	for _, note := range db.TransportNotes() {
		logger.Info(note)
	}

	if err := migrateUpDirect(ctx, db, logger); err != nil {
		return err
	}
	// A failed module migration does not stop the server: the module's
	// services are skipped and the error is logged.
	_ = migrateModulesDirect(ctx, db, logger)
	editionstate.SetSiteIDSource(siteid.New(db.Pool()).Get)
	if err := checkMediaOffsiteOverlap(ctx, os.Getenv, offsite.NewStore(db.Pool()).Get); err != nil {
		return err
	}
	faultAfterMigrate()
	// Switch events the supervisor recorded; never blocks the boot.
	if err := importUpdateEvents(ctx, db.Pool(), config.ResolveDataDir(), logger); err != nil {
		logger.Warn("cannot import update events", "error", err)
	}
	// The supervisor writes a switch's success after this child has booted.
	watchCtx, stopWatch := context.WithCancel(ctx)
	defer stopWatch()
	go watchUpdateEvents(watchCtx, db.Pool(), config.ResolveDataDir(), logger, updateEventsEvery)
	if err := firstBoot(ctx, db.Pool(), cfg.Hatamoto.SiteName, logger); err != nil {
		return err
	}
	logUnconvertedPages(ctx, db.Pool(), logger)
	if n, err := user.NewPostgresRepository(db).Count(ctx); err == nil && n == 0 {
		logger.Warn("no users yet: create the first admin with `hatamoto user create --email you@example.com`")
	}

	users := user.NewPostgresRepository(db)
	adminAddr, err := bootAdminPath(ctx, cfg.Demo.Enabled, adminpath.NewStore(db.Pool()), loadSQLSite(ctx, db.Pool(), adminBaseURL(db.Pool(), os.Getenv)(ctx)),
		func(ctx context.Context) (bool, error) { n, err := users.Count(ctx); return n > 0, err },
		cfg.Admin.Path, logger)
	if err != nil {
		return err
	}
	cfg.Admin.Resolved = adminAddr
	cfg.Admin.Version, _ = buildInfo()
	if err := config.CheckPrefixAgainstAdmin(cfg.PublicSite.APIPrefix, adminAddr); err != nil {
		return err
	}
	if err := server.CheckModuleRoutes(adminAddr, cfg.PublicSite.APIPrefix); err != nil {
		return err
	}
	// Once, before any request: neutral cookie names (a rename logs everyone out).
	useNeutralCookieNames(cfg.AdminCookieSecure)
	logger.Info("session cookie names set", "secure", cfg.AdminCookieSecure)

	srv := server.New(cfg, db, logger)
	// Boot garbage (parsed templates, migrations) is dead now: hand it back once.
	debug.FreeOSMemory()
	startUpdater(srv, db.Pool(), db.Direct(), cfg.Server.MediaUploadDir, cfg.PublicSite.BaseURL, cfg.Demo.Enabled, cfg.Server.EnableWorker, logger)
	svcCtx, stopServices := context.WithCancel(ctx)
	waitServices := startModuleServices(svcCtx, logger)
	defer func() { stopServices(); waitServices(moduleServiceStopTimeout) }()
	if err := srv.Start(); err != nil {
		return fmt.Errorf("server error: %w", err)
	}
	return nil
}

// useNeutralCookieNames switches the session and CSRF cookies to names that do
// not identify the software. Called once at boot, before any request.
func useNeutralCookieNames(secure bool) {
	auth.UseNeutralCookieNames(secure)
	middleware.UseNeutralCSRFCookieName(secure)
}

// warnStagingMedia says that staging mode still uploads media to the
// configured object storage bucket. Only deletions are suppressed.
func warnStagingMedia(logger *slog.Logger, getenv func(string) string) {
	if kind, _ := media.StorageFromEnv(getenv); kind == "s3" {
		logger.Warn("staging mode: media uploads still go to the configured object storage bucket; give this copy its own bucket")
	}
}
