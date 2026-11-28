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

	// HostingTargetSFTP publishes over SFTP (cPanel / shared hosting).
	HostingTargetSFTP       = "sftp"
	HostingSFTPAuthPassword = "password"
	HostingSFTPAuthKey      = "key"

	HostingRunOK      = "ok"
	HostingRunFailed  = "failed"
	HostingRunRunning = "running"

	// HostingTriggerTest marks a Test export run: it writes the saved target but
	// is never the live export (no banner, no mail, not the Last export line).
	HostingTriggerTest = "test"

	// Live islands choice: static files and islands on the site address, or
	// islands on a separate live origin.
	HostingLiveSame     = "same"
	HostingLiveSeparate = "separate"

	// Setup checklist states (folder and reachability).
	HostingCheckOK          = "ok"
	HostingCheckNotWritable = "not-writable"
	HostingCheckMissing     = "missing"
	HostingCheckFailed      = "failed"
	HostingCheckUnknown     = "unknown" // not checked
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
	// ErrHostingHostKeyChanged: the host presents another key than the one shown.
	//
	//lint:ignore ST1005 shown verbatim
	ErrHostingHostKeyChanged = errors.New("The host key changed since it was shown. Check it again before you trust it.")
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

	// SFTP target. The password or key is never part of the view.
	SFTPHost, SFTPPort, SFTPUser, SFTPAuth, SFTPDir string
	SFTPHostKey                                     string // trusted SHA256 fingerprint; "" = not trusted
	SFTPHTAccess                                    bool
	SFTPSecretSaved                                 bool // the saved password/key (kind = SFTPAuth) can be read
	SFTPSecretLost                                  bool // one is stored but cannot be read (the server SECRET changed)

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
	switch v.Target {
	case HostingTargetS3:
		return v.Bucket != ""
	case HostingTargetSFTP:
		return v.SFTPHost != "" && v.SFTPUser != "" && v.SFTPSecretSaved
	}
	return v.Dir != ""
}

// HostingInput is one submitted form. SecretKey is empty when the owner left
// it blank, which keeps the saved key.
type HostingInput struct {
	Mode, Target, Dir, Service, Endpoint, Bucket, Prefix, Region, AccessKey, SecretKey, LiveOrigin string
	// LiveMode is HostingLiveSame or HostingLiveSeparate; "" derives from LiveOrigin.
	LiveMode string

	// SFTP fields. SFTPPassword / SFTPKey empty = keep the saved one.
	SFTPHost, SFTPPort, SFTPUser, SFTPAuth, SFTPPassword, SFTPKey, SFTPDir string
	SFTPHTAccess                                                           bool
}

// HostingSetup is what the "Set up your web server" card shows, from the
// SAVED settings. It holds plain strings: the admin does not import the
// generator.
type HostingSetup struct {
	Saved    bool   // a target is saved
	Target   string // HostingTargetDir, HostingTargetS3 or HostingTargetSFTP
	SameHost bool   // no live origin saved
	Site     string
	Upstream string // host:port the generated config proxies to

	Caddy, Nginx string   // "" when not generated (S3 target or unsafe value)
	GenError     string   // safe text when generation was refused
	Skipped      []string // redirect rules left out of the config

	Folder string // HostingCheckOK, NotWritable, Missing or Unknown

	// Redirect rules of the last export: exact ones become meta-refresh pages
	// and wildcard ones are lost on a plain web server. RedirectsSplit is false
	// when only the total is known (S3 target).
	Exact, Wildcard int
	RedirectsSplit  bool
	RedirectsTotal  int

	Reach   string // HostingCheckOK, HostingCheckFailed or HostingCheckUnknown
	ReachAt time.Time

	HeaderNames []string // security headers the static host must send

	// SFTP target.
	HostKey       string // trusted fingerprint of the saved SFTP host ("" = not trusted)
	Remote        string // HostingCheckOK, Failed or Unknown: the last run to this SFTP target
	RemoteDir     string // saved remote folder, for the check text
	HTAccess      bool   // Write .htaccess is on
	HTAccessBlock string // preview of the Hatamoto block from the redirect rules ("" = no rule)
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
	// Runs is the log: the newest runs of every trigger, Test export included.
	Runs(ctx context.Context, limit int) ([]HostingRun, error)
	// LiveRuns is the newest runs that are not Test export runs, however many
	// test runs are newer.
	LiveRuns(ctx context.Context, limit int) ([]HostingRun, error)
	// LastRun is the newest FINISHED run (ok or failed); a running row is skipped.
	LastRun(ctx context.Context) (HostingRun, bool, error)
	// Setup describes the web-server setup for the saved settings. A missing
	// export is not an error.
	Setup(ctx context.Context) (HostingSetup, error)
	// ProbeHostKey connects to the SAVED SFTP host (key exchange only, no
	// credentials sent) and returns the SHA256 fingerprint it presents.
	// ErrHostingNoTarget when no SFTP host is saved. Errors are safe to show.
	ProbeHostKey(ctx context.Context) (string, error)
	// TrustHostKey stores fingerprint as the trusted host key after checking
	// the host still presents it; ErrHostingHostKeyChanged otherwise.
	TrustHostKey(ctx context.Context, fingerprint string) error
	// Islands: live = islands answered by the live origin, stays = names of
	// the ones that stay on Hatamoto.
	Islands() (live []HostingIsland, stays []string)
}

// SetHosting hands the admin the hosting service once the engine is known.
// Call it before serving starts, and only with a non-nil service.
func (a *Admin) SetHosting(svc HostingService) { a.deps.Hosting = svc }
