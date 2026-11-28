package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/safefetch"
	"hatamoto.dev/core/internal/staticexport"
	"hatamoto.dev/core/internal/statictrigger"
)

const (
	// exportRunsKeep is how long finished export runs stay in the log.
	exportRunsKeep = 90 * 24 * time.Hour
	// exportTimeout bounds one run; the engine has no timeout of its own.
	exportTimeout = 30 * time.Minute
)

// exportRunStore is the part of staticexport.RunStore a run uses.
type exportRunStore interface {
	Start(ctx context.Context, trigger, target string) (uuid.UUID, error)
	Finish(ctx context.Context, id uuid.UUID, r staticexport.RunResult) error
	PruneRuns(ctx context.Context, before time.Time) (int64, error)
}

// staticExportRunner is the statictrigger.Run behind the Hosting page and the
// change hooks. The fields are seams: production wires the engine and the
// live server, tests stand in.
type staticExportRunner struct {
	settings func(ctx context.Context) (staticexport.Settings, bool, error)
	runs     exportRunStore
	// target builds the publish target from the saved settings.
	target func(set staticexport.Settings) (staticexport.Target, error)
	export func(ctx context.Context, t staticexport.Target, o staticexport.Options) (staticexport.Result, error)
	notify func(ctx context.Context, runID uuid.UUID, ok bool, at time.Time, firstErr string)
	now    func() time.Time
	log    *slog.Logger
	// finished runs after a run ends and is reported (nil = nothing).
	finished func()
}

// modeOn is the trigger's mode gate. A restored database reads 'served' here
// (the restore switches it), so a restored copy never exports into the
// original's target.
func (r *staticExportRunner) modeOn(ctx context.Context) (bool, error) {
	set, found, err := r.settings(ctx)
	return found && set.Mode == staticexport.ModeStatic, err
}

// run is statictrigger.Run: one export to the saved target.
func (r *staticExportRunner) run(ctx context.Context, full bool, reason string) error {
	ctx, cancel := context.WithTimeout(ctx, exportTimeout)
	defer cancel()
	set, found, err := r.settings(ctx)
	if err != nil {
		return err
	}
	if !found || set.Mode != staticexport.ModeStatic {
		return nil // switched to served since the change was noted
	}
	t, terr := r.target(set)
	if terr != nil {
		// No run row exists yet: record one so the log, the banner and the mail see it.
		if id, serr := r.runs.Start(ctx, reason, "unresolved"); serr == nil {
			_ = r.runs.Finish(ctx, id, staticexport.RunResult{Errors: []string{terr.Error()}})
			r.notify(ctx, id, false, r.now(), terr.Error())
		}
		return terr
	}
	res, err := r.export(ctx, t, staticexport.Options{Trigger: reason, Full: full})
	if errors.Is(err, staticexport.ErrBusy) {
		return fmt.Errorf("%w: %v", statictrigger.ErrRetry, err) // another process holds the run
	}
	first := ""
	if err != nil {
		first = err.Error()
	}
	r.notify(ctx, res.RunID, err == nil, r.now(), first)
	if r.finished != nil {
		r.finished()
	}
	if _, perr := r.runs.PruneRuns(ctx, r.now().Add(-exportRunsKeep)); perr != nil {
		r.log.Warn("static export: prune runs", slog.String("error", perr.Error()))
	}
	return err
}

// previousLiveFailed reports whether the newest finished export before cur
// failed. The running run and test runs are skipped: a test run is not the
// live export.
func previousLiveFailed(list []staticexport.Run, cur uuid.UUID) bool {
	for _, r := range list {
		if r.ID == cur || r.Status == "running" || r.Trigger == admin.HostingTriggerTest {
			continue
		}
		return r.Status == "failed"
	}
	return false
}

// setupStaticExport builds the trigger (worker process only), the failure
// notifier and the Hosting adapter. Run it once the mail service and the
// site settings exist; the admin may not be mounted yet (see hostingSvc).
func (s *Server) setupStaticExport() {
	if s.db == nil || s.cfg == nil {
		return
	}
	settings := staticexport.NewSettingsStore(s.db.Pool())
	runs := staticexport.NewRunStore(s.db.Pool())
	key := staticexport.SecretKey(s.cfg.Secret)
	guard := staticexport.DirGuard{Forbidden: []string{config.ResolveDataDir(), s.cfg.Server.MediaUploadDir}}

	if s.emailService != nil && s.userRepo != nil {
		s.exportNotify = &exportFailNotifier{
			configured: s.emailService.IsConfigured, owners: s.ownerEmails, site: liveAdminSite(s.digestSite, settings.Get),
			location: s.siteLocation, send: s.emailService.SendStaticExportFailed, log: s.logger,
			previousFailed: func(ctx context.Context, cur uuid.UUID) (bool, error) {
				list, err := runs.ListLive(ctx, 5)
				if err != nil {
					return false, err
				}
				return previousLiveFailed(list, cur), nil
			},
		}
	}
	siteURL := pluginEntityPaths{s}.BaseURL
	probe := newReachProbe()
	afterRun := func() { probe.start(s.lifecycleCtx, siteURL) } // not the run's ctx: it is cancelled when the run returns
	runner := &staticExportRunner{
		settings: settings.Get, runs: runs, export: s.StaticExport, now: func() time.Time { return time.Now().UTC() }, log: s.logger,
		finished: afterRun,
		target: func(set staticexport.Settings) (staticexport.Target, error) {
			return staticexport.SavedTarget(set, key, guard, os.Getenv)
		},
		notify: func(ctx context.Context, id uuid.UUID, ok bool, at time.Time, firstErr string) {
			s.exportNotify.RunFinished(ctx, id, ok, at, firstErr) // nil-safe
		},
	}
	if s.cfg.Server.EnableWorker {
		s.staticTrigger = statictrigger.New(func() context.Context { return s.lifecycleCtx }, runner.run, runner.modeOn, s.logger)
	}
	s.hostingSvc = &hostingAdmin{
		settings: settings, runs: runs, key: key, trigger: s.staticTrigger,
		refresh: s.refreshStaticHosting, siteURL: siteURL, snapshot: s.staticHosting,
		refuse: s.staticRefusal, dirGuard: guard, getenv: os.Getenv, lookup: safefetch.DefaultLookup,
		testExport: s.StaticTestExport, background: func() context.Context { return s.lifecycleCtx }, log: s.logger, finished: afterRun,
		setup: hostingSetupDeps{
			adminPath: s.adminPathValue, apiPrefix: s.cfg.PublicSite.APIPrefix,
			upstream: upstreamAddr(s.cfg.Server.Host, s.cfg.Server.Port), maxBodyMB: uploadLimitMB(s.cfg.Server.MediaMaxSizeMB), adminMB: adminUploadLimitMB(s.cfg.Server.MediaMaxSizeMB), headers: s.staticPageHeaders,
			folder: folderState, reach: probe.result, rules: (&staticSource{s: s}).Redirects,
		},
	}
	if s.admin != nil { // already mounted: hand it over directly
		s.admin.SetHosting(s.hostingSvc)
	}
}

// liveAdminSite points the mail's admin link at the live origin. In static
// mode the Site URL is the CDN or directory that holds the exported files; the
// admin answers on the live origin, so a link built on the Site URL would land
// on the exported 404 page. Same host (no live origin): the admin answers on
// the Site URL, which is all there is.
func liveAdminSite(site func(ctx context.Context) (name, base, adminPath string),
	settings func(ctx context.Context) (staticexport.Settings, bool, error)) func(ctx context.Context) (string, string, string) {
	return func(ctx context.Context) (string, string, string) {
		name, base, adminPath := site(ctx)
		if set, found, err := settings(ctx); err == nil && found && set.Mode == staticexport.ModeStatic && set.LiveOrigin != "" {
			base = set.LiveOrigin
		}
		return name, base, adminPath
	}
}

// staticRefusal is the Hosting adapter's pre-check for switching to static;
// it mirrors the engine source's refusals (static_source.go).
func (s *Server) staticRefusal(ctx context.Context) error {
	if s.demoOn() {
		return errHostingDemo
	}
	if s.siteAccessSettings(ctx).On() {
		return errHostingSiteAccess
	}
	return nil
}

var (
	//lint:ignore ST1005 shown to the owner verbatim as a sentence
	errHostingDemo = errors.New("Static files are off in the demo.")
	//lint:ignore ST1005 shown to the owner verbatim as a sentence
	errHostingSiteAccess = errors.New("Turn off Site access before switching to static files.")
)

// siteLocation is the site time zone, UTC when unset or unreadable.
func (s *Server) siteLocation(ctx context.Context) *time.Location {
	if s.siteConfig != nil {
		if c, err := s.siteConfig.Get(ctx); err == nil && c != nil && c.Timezone != "" {
			if l, err := time.LoadLocation(c.Timezone); err == nil {
				return l
			}
		}
	}
	return time.UTC
}
