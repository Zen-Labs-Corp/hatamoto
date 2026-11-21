package admin

import (
	"context"
	"errors"
	"time"

	"hatamoto.dev/core/internal/admin/ui"
)

// Hosting modes and targets as the form offers them. They mirror
// staticexport.ModeServed/ModeStatic/TargetDir/TargetS3 and the run statuses
// 'running'/'ok'/'failed' of hatamoto_static_export_runs; the admin does not
// import the engine, so the server adapter maps.
const (
	HostingServed    = "served"
	HostingStatic    = "static"
	HostingTargetDir = "dir"
	HostingTargetS3  = "s3"

	HostingRunOK      = "ok"
	HostingRunFailed  = "failed"
	HostingRunRunning = "running"

	// HostingTriggerTest marks a Test export run: it writes the saved target but
	// is never the live export (no banner, no mail, not the Last export line).
	HostingTriggerTest = "test"
)

var (
	// ErrHostingExportQueued: a run is in progress; another starts when it ends.
	ErrHostingExportQueued = errors.New("an export is running; another starts when it finishes")
	// ErrHostingNotStatic: export actions need static mode.
	ErrHostingNotStatic = errors.New("exports run only in static mode")
	// ErrHostingNoTarget: the saved target is incomplete (no directory / no bucket).
	ErrHostingNoTarget = errors.New("save a target first")
	// ErrHostingTestBusy: an export is running; the test does not queue.
	ErrHostingTestBusy = errors.New("an export is running; try the test export again when it finishes")
)

// HostingView is the saved hosting configuration. It never carries the secret.
type HostingView struct {
	Mode, Target string
	Dir          string
	Service      string // OffsiteAWS..OffsiteCustom (same presets as off-site)
	Endpoint     string
	Bucket       string
	Prefix       string
	Region       string
	AccessKey    string
	LiveOrigin   string

	// SecretSaved: a secret key is stored and can be read. SecretLost: one is
	// stored but cannot be read (the server SECRET changed).
	SecretSaved bool
	SecretLost  bool

	// IslandsWarning is non-empty when the mode is static but the island
	// snapshot is inactive, so pages are still served as in served mode. Safe
	// to show.
	IslandsWarning string

	// WorkerOff: this process runs no export worker, so Export now and Rebuild
	// all cannot start.
	WorkerOff bool
}

// hostingTargetSaved reports whether the saved target can be exported to.
func hostingTargetSaved(v HostingView) bool {
	if v.Target == HostingTargetS3 {
		return v.Bucket != ""
	}
	return v.Dir != ""
}

// HostingInput is one submitted form. SecretKey is empty when the owner left
// it blank, which keeps the saved key.
type HostingInput struct {
	Mode, Target, Dir, Service, Endpoint, Bucket, Prefix, Region, AccessKey, SecretKey, LiveOrigin string
}

// HostingRun is one row of the export log.
type HostingRun struct {
	Started    time.Time
	Trigger    string // "manual", "rebuild", "mode", "change", "theme", ...
	Pages      int
	Bytes      int64
	Changed    int
	Removed    int
	Duration   time.Duration
	Errors     int
	FirstError string // safe to show: engine errors never hold the secret
	Result     string // HostingRunOK, HostingRunFailed or HostingRunRunning
}

// HostingIsland is one island answered by the live origin: the rows of the
// island inventory that are served live.
type HostingIsland struct{ Label, Route, Status string }

// HostingService is the static export engine behind Settings > Hosting.
type HostingService interface {
	View(ctx context.Context) (HostingView, error)
	// Save validates in against in.Mode and stores target + live origin (never
	// the mode). Field errors come back as the list and nothing is saved.
	Save(ctx context.Context, in HostingInput) ([]ui.FieldError, error)
	// SetMode switches served/static. Errors are safe to show (site access on,
	// target incomplete).
	SetMode(ctx context.Context, mode string) error
	// Export starts an export now (full = Rebuild all) without waiting for it.
	// ErrHostingNotStatic or ErrHostingExportQueued when it cannot start.
	Export(ctx context.Context, full bool, trigger string) error
	// TestExport starts a Test export of the SAVED target in any mode without
	// waiting and without changing the mode or what visitors get.
	// ErrHostingNoTarget, ErrHostingTestBusy or a safe refusal error when it
	// cannot start.
	TestExport(ctx context.Context) error
	Runs(ctx context.Context, limit int) ([]HostingRun, error)
	// LastRun is the newest FINISHED run (ok or failed); a running row is skipped.
	LastRun(ctx context.Context) (HostingRun, bool, error)
	// Islands: live = islands answered by the live origin, stays = names of
	// the ones that stay on Hatamoto.
	Islands() (live []HostingIsland, stays []string)
}

// SetHosting hands the admin the hosting service once the engine is known.
// Call it before serving starts, and only with a non-nil service.
func (a *Admin) SetHosting(svc HostingService) { a.deps.Hosting = svc }
