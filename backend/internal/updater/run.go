package updater

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"hatamoto.dev/core/internal/release"
	"hatamoto.dev/core/internal/supervisor"
	"hatamoto.dev/core/internal/worker"
)

// Further Status.Decision values, next to those Decide produces.
const (
	KindStaged = "staged"
	KindOff    = "off"
	KindNotify = "notify"
	// KindRollbackPending: a check found the running release withdrawn and
	// changed nothing; the next scheduled run rolls the site back.
	KindRollbackPending = "rollback_pending"
	KindBusy            = "busy"
	// KindFailed is a managed apply that stopped with an error (a download or
	// signature failure, a withdrawn target): the owner's page shows it.
	KindFailed = "update_failed"
)

// Status.LastResult values.
const (
	ResultOK      = "ok"
	ResultError   = "error"
	ResultSkipped = "skipped"
)

const (
	checkEvery  = 6 * time.Hour
	checkJitter = 30 * time.Minute
	runLimit    = 45 * time.Minute // fetch, download and switch together
	jobID       = "update_check"
)

// Env is what the updater needs from the process around it. The updater
// imports neither the app nor the server; they hand in these pieces.
type Env struct {
	Data        string // absolute data dir
	Version     string // the running version
	Platform    string // "" = this build's platform
	Supervisor  supervisor.Config
	Store       *Store // nil: no stored policy or status (the offline CLI)
	Plugins     PluginSource
	Imports     ImportProbe
	Backups     Backuper // nil blocks the update unless the backup is skipped
	Site        func(ctx context.Context) *time.Location
	DatabaseURL string
	BaseURL     string // "" = DefaultBaseURL
	Log         *slog.Logger
	Now         func() time.Time
}

// RunOpts shapes one Run.
type RunOpts struct {
	DryRun bool   // decide only; nothing is stored or staged
	Apply  string // a named version: the explicit operator route
	Force  bool   // skip the plugin preflight
	// CheckOnly is a dry run whose outcome is stored like a scheduled check:
	// fetch, decide and preflight, never download, stage or switch.
	CheckOnly bool
	// Managed makes a named Apply behave like a scheduled install where it
	// matters to the owner: it waits for a running check instead of giving up,
	// takes the pre-update database backup and stores its outcome whatever it
	// is. The CLI leaves it off.
	Managed bool
	// SkipBackup skips the pre-update backup for this run only.
	SkipBackup bool
}

// EnvBackup set to "0" turns the pre-update backup off.
const EnvBackup = "HATAMOTO_UPDATE_BACKUP"

// Backuper takes the backup that must exist before a switch. An error blocks
// the update; the note is added to the status text.
type Backuper interface {
	PreUpdate(ctx context.Context, target string) (note string, err error)
}

type switchFn func(supervisor.Config, supervisor.ApplyOpts) (supervisor.Result, error)

// Updater runs update checks, one at a time.
type Updater struct {
	env Env
	mu  sync.Mutex

	fetcher    *Fetcher
	stageFn    switchFn
	applyFn    switchFn
	rollbackFn func(supervisor.Config) (supervisor.Result, error)
	supervised func(supervisor.Config) bool
	free       func(dir string) (uint64, error)
}

// New validates the base URL and returns an Updater.
func New(env Env) (*Updater, error) {
	if env.Log == nil {
		env.Log = slog.Default()
	}
	if env.Platform == "" {
		env.Platform = runtime.GOOS + "-" + runtime.GOARCH
	}
	if env.Now == nil {
		env.Now = time.Now
	}
	now := env.Now
	if off := clockOffset(); off > 0 {
		env.Now = func() time.Time { return now().Add(off) }
	}
	f, err := NewFetcher(env.BaseURL)
	if err != nil {
		return nil, err
	}
	return &Updater{env: env, fetcher: f, stageFn: supervisor.Stage, applyFn: supervisor.Apply,
		rollbackFn: supervisor.Rollback, supervised: supervisor.Running, free: FreeDisk}, nil
}

// Schedule registers the periodic check: every six hours give or take half an
// hour, the first one a few minutes after start. The run detaches from the
// scheduler's 10 minute cap, which is shorter than a slow download, but not
// from its cancellation: the scheduler's context is the server's lifecycle
// context, so a shutdown stops the download and the pg_dump.
func (u *Updater) Schedule(s *worker.Scheduler) error {
	every, jitter, first := checkEvery, checkJitter, time.Duration(1+rand.IntN(9))*time.Minute
	if e, f, ok := testInterval(); ok {
		every, jitter, first = e, 0, f
	}
	return s.ScheduleJitter(jobID, every, jitter, first, u.job)
}

// job is one scheduled check.
func (u *Updater) job(ctx context.Context) error {
	ctx, cancel := detachDeadline(ctx, runLimit)
	defer cancel()
	st, err := u.Run(ctx, RunOpts{})
	if err != nil {
		return err
	}
	u.env.Log.Info("update check", "decision", st.Decision, "target", st.Target, "reason", st.Reason)
	return nil
}

// Run does one check and, under policy, the update. It makes exactly one
// request before a target exists: the channel manifest.
func (u *Updater) Run(ctx context.Context, o RunOpts) (Status, error) {
	if !u.lock(ctx, o) {
		return Status{LastResult: ResultSkipped, Decision: KindBusy, Reason: "another update check is running"}, nil
	}
	defer u.mu.Unlock()
	if o.CheckOnly {
		o.DryRun = true
	}
	persist := o.CheckOnly || (!o.DryRun && (o.Apply == "" || o.Managed))

	var set Settings
	var prev Status
	if u.env.Store != nil {
		var err error
		if set, prev, err = u.env.Store.Load(ctx); err != nil {
			return Status{}, err
		}
	}
	set = set.Effective(os.Getenv)
	now := u.env.Now()

	st, err := u.run(ctx, o, set, prev, now)
	if st.LastCheckAt == nil {
		st.LastCheckAt = &now
	}
	if err != nil {
		st.LastResult, st.Reason = ResultError, err.Error()
		st.Decision, st.Target, st.AvailableMajor, st.FirstSeen = prev.Decision, prev.Target, prev.AvailableMajor, prev.FirstSeen
		if o.Managed && o.Apply != "" {
			st.Decision, st.Target = KindFailed, o.Apply
		}
	}
	if o.Managed && o.Apply != "" && st.Decision == KindBusy {
		st.Target = o.Apply // the owner can try again
	}
	if u.env.Store != nil && persist {
		save := st
		if o.CheckOnly {
			save = checkOnlyStored(st)
		}
		if serr := u.env.Store.SaveStatus(ctx, save); serr != nil {
			u.env.Log.Warn("could not store the update status", "error", serr)
		}
	}
	return st, err
}

// lock takes the run lock. A managed apply started by the owner waits for a
// running check to finish (until ctx ends); everything else gives up at once.
func (u *Updater) lock(ctx context.Context, o RunOpts) bool {
	if u.mu.TryLock() {
		return true
	}
	if !(o.Managed && o.Apply != "") {
		return false
	}
	t := time.NewTicker(250 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-t.C:
			if u.mu.TryLock() {
				return true
			}
		}
	}
}

// checkOnlyStored is what a check that installs nothing leaves in the status:
// a release that is due reads as scheduled (the next scheduled run installs
// it, or the owner does), and a rollback that was only decided is stored as
// rollback_pending, never as a rollback that happened and never as an update.
func checkOnlyStored(st Status) Status {
	switch st.Decision {
	case KindApplying:
		st.Decision, st.Reason = KindScheduled, st.Target+" is ready to install"
	case KindRolledBackYank:
		st.Decision, st.Reason = KindRollbackPending, st.Reason+"; check only, not switching"
	}
	return st
}

func (u *Updater) run(ctx context.Context, o RunOpts, set Settings, prev Status, now time.Time) (Status, error) {
	skip := func(kind, reason string) (Status, error) {
		return Status{LastResult: ResultSkipped, Decision: kind, Reason: reason, FirstSeen: prev.FirstSeen, AvailableMajor: prev.AvailableMajor}, nil
	}
	if o.Apply == "" && set.Mode == ModeOff {
		return skip(KindOff, "updates are off")
	}
	layout := supervisor.NewLayout(u.env.Data)
	state, err := supervisor.LoadState(layout.StatePath())
	if err != nil {
		return Status{}, fmt.Errorf("supervisor state: %w", err)
	}
	// What the supervisor runs, which after a self-update is the current
	// release and not the binary this process was built as.
	running := supervisor.RunningVersion(layout, state, u.env.Version)
	// Under a running release this process is not the bootstrap, so its own
	// version says nothing about it: without state.bootstrap_version the
	// bootstrap's version is unknown, never the child's.
	sup := u.env.Supervisor
	if state.Run == supervisor.RunRelease {
		sup.BootstrapVersion = ""
	}
	if o.Apply == "" && !release.ValidVersion(running) {
		return skip(KindDev, "dev build, updates disabled")
	}
	switch {
	case state.Pending != nil:
		return skip(KindBusy, fmt.Sprintf("a switch to %s is in progress", state.Pending.Version))
	case state.Phase == "probation":
		return skip(KindBusy, "the running release is still on probation")
	case state.Unproven != "":
		return skip(KindBusy, fmt.Sprintf("%s has not been proven yet", state.Unproven))
	}
	if u.env.Imports != nil {
		running, err := u.env.Imports.ImportRunning(ctx)
		if err != nil {
			u.env.Log.Warn("could not tell whether an import runs", "error", err)
		}
		if running {
			return skip(KindBusy, "a content import is running; will retry next check")
		}
	}

	ropts := u.env.Supervisor.Release
	ropts.Channel, ropts.Now = set.Channel, u.env.Now
	doc, err := u.fetcher.Manifest(ctx, set.Channel)
	if err != nil {
		return Status{}, fmt.Errorf("fetch channel %s: %w", set.Channel, err)
	}
	man, err := release.VerifyManifest(doc, ropts)
	if err != nil {
		return Status{}, fmt.Errorf("channel %s: %w", set.Channel, err)
	}
	u.dropInvalid(man)

	records := &recordCache{u: u, ctx: ctx, opts: ropts, m: map[string]fetched{}, store: !o.DryRun,
		sigs: map[string]string{}}
	for _, e := range man.Releases {
		records.sigs[e.Version] = e.RecordSig
	}
	if records.store {
		records.prune(man)
	}
	var dec Decision
	if o.Apply != "" {
		if dec, err = u.named(man, state, records, o.Apply, running); err != nil {
			return Status{}, err
		}
	} else {
		in := Input{
			Running: running, Manifest: man, Settings: set, Now: now, FirstSeen: prev.FirstSeen,
			Record: records.get,
			Bad:    state.IsBad,
			BadReason: func(v string) string {
				for _, b := range state.Bad {
					if b.Version == v {
						return b.Reason
					}
				}
				return ""
			},
		}
		if u.env.Site != nil {
			in.Loc = u.env.Site(ctx)
		}
		// The same choice supervisor.Rollback makes: a usable previous link,
		// else the bootstrap when the first update came from it.
		if prev := layout.Link("previous"); prev != "" && (layout.Valid(prev) || !state.PreviousIsBootstrap) {
			in.Previous = prev
		} else if state.PreviousIsBootstrap {
			in.Previous, in.PreviousIsBootstrap = supervisor.BootstrapVersionOf(state, sup.BootstrapVersion), true
		}
		id, err := InstallID(u.env.Data)
		if err != nil {
			return Status{}, fmt.Errorf("install_id unreadable: %w", err)
		}
		in.Bucket = func(v string) int { return Bucket(id, v) }
		if state.Run == supervisor.RunRelease {
			if rec, err := supervisor.StoredRecord(u.env.Supervisor, running); err == nil {
				in.RunningRecord = rec
			}
		}
		if dec, err = Decide(in); err != nil {
			return Status{}, err
		}
	}
	if dec.Action == ActionApply && dec.Target == running { // never reinstall what runs
		return Status{LastResult: ResultOK, Decision: KindUpToDate, Reason: running + " is already running",
			FirstSeen: dec.FirstSeen, AvailableMajor: dec.AvailableMajor}, nil
	}
	st := Status{LastResult: ResultOK, Decision: dec.Kind, Reason: dec.Reason, Target: dec.Target,
		AvailableMajor: dec.AvailableMajor, FirstSeen: dec.FirstSeen}
	if o.Apply != "" {
		st.FirstSeen, st.AvailableMajor = prev.FirstSeen, prev.AvailableMajor
	}

	switch dec.Action {
	case ActionRollback:
		if set.Mode == ModeNotify { // like a yank-to: reported, never switched
			st.Decision, st.Reason = KindNotify, st.Reason+"; mode notify, not switching"
			return st, nil
		}
		return u.rollback(st, o, sup)
	case ActionApply:
	default:
		return st, nil
	}

	if reason := u.preflight(ctx, dec, o); reason != "" {
		if fb := dec.Fallback; fb != nil {
			// The running release is withdrawn and its fix cannot be installed
			// now: do not stay on the withdrawn one, go back instead.
			st.Decision, st.Target = fb.Kind, fb.Target
			st.Reason = fmt.Sprintf("%s; %s is blocked (%s)", fb.Reason, dec.Target, reason)
			if set.Mode == ModeNotify {
				st.Decision, st.Reason = KindNotify, st.Reason+"; mode notify, not switching"
				return st, nil
			}
			return u.rollback(st, o, sup)
		}
		st.Decision, st.Reason = KindBlocked, reason
		return st, nil
	}
	if o.DryRun {
		if !o.CheckOnly {
			st.Reason += " (dry run: nothing installed)"
		}
		return st, nil
	}
	if u.env.Imports != nil { // a second look right before touching anything
		if running, _ := u.env.Imports.ImportRunning(ctx); running {
			st.LastResult, st.Decision, st.Reason = ResultSkipped, KindBusy, "a content import is running; will retry next check"
			return st, nil
		}
	}
	return u.install(ctx, st, dec, records, o)
}

// dropInvalid removes entries and yanks whose versions are not valid.
func (u *Updater) dropInvalid(m *release.ChannelManifest) {
	rel := m.Releases[:0]
	for _, e := range m.Releases {
		if release.ValidVersion(e.Version) {
			rel = append(rel, e)
		} else {
			u.env.Log.Warn("ignoring a channel entry with an invalid version", "version", e.Version)
		}
	}
	m.Releases = rel
	yk := m.Yanked[:0]
	for _, y := range m.Yanked {
		if release.ValidVersion(y.Version) && (y.To == "" || release.ValidVersion(y.To)) {
			yk = append(yk, y)
		} else {
			u.env.Log.Warn("ignoring a malformed yank", "version", y.Version)
		}
	}
	m.Yanked = yk
}

// named builds the decision for `update apply vX.Y.Z`: the operator route,
// which ignores ramp, window, defer and pin but refuses a yanked or bad
// version and verifies everything.
func (u *Updater) named(m *release.ChannelManifest, state *supervisor.State, records *recordCache, version, running string) (Decision, error) {
	if !release.ValidVersion(version) {
		return Decision{}, fmt.Errorf("%q is not a release version", version)
	}
	listed := false
	for _, e := range m.Releases {
		listed = listed || e.Version == version
	}
	if !listed {
		return Decision{}, fmt.Errorf("%s is not on channel %s", version, m.Channel)
	}
	for _, y := range m.Yanked {
		if y.Version == version {
			return Decision{}, fmt.Errorf("%s was withdrawn (%s)", version, y.Reason)
		}
	}
	if state.IsBad(version) {
		return Decision{}, fmt.Errorf("%s is on this box's bad list (it failed health gating before)", version)
	}
	if version == running {
		return Decision{}, fmt.Errorf("%s is already running", version)
	}
	rec, err := records.get(version)
	if err != nil {
		return Decision{}, err
	}
	return Decision{Action: ActionApply, Kind: KindApplying, Target: version, Rec: rec,
		Reason: fmt.Sprintf("updating %s to %s", running, version)}, nil
}

// preflight returns why the target cannot be installed now, or "".
func (u *Updater) preflight(ctx context.Context, dec Decision, o RunOpts) string {
	art, ok := dec.Rec.Artifacts[u.env.Platform]
	if !ok {
		return fmt.Sprintf("%s has no build for %s", dec.Target, u.env.Platform)
	}
	if o.Force {
		return ""
	}
	if u.env.Plugins != nil {
		ranges, err := u.env.Plugins.EnabledSDKRanges(ctx)
		if err != nil {
			return fmt.Sprintf("cannot read the installed plugins to check %s: %v", dec.Target, err)
		}
		if b := SDKBlockers(dec.Rec, ranges); len(b) > 0 {
			return b[0] + moreSuffix(len(b))
		}
	}
	return DiskBlocker(u.free, u.env.Data, art.Size)
}

func moreSuffix(n int) string {
	if n <= 1 {
		return ""
	}
	return fmt.Sprintf(" (and %d more)", n-1)
}

// rollback runs a yank-back, only under a running supervisor: an offline
// switch would leave the release ungated.
func (u *Updater) rollback(st Status, o RunOpts, sup supervisor.Config) (Status, error) {
	if o.DryRun {
		if !o.CheckOnly {
			st.Reason += " (dry run)"
		}
		return st, nil
	}
	if !u.supervised(u.env.Supervisor) {
		st.Decision = KindBlocked
		st.Reason += "; not rolled back here because no supervisor is running (run `hatamoto update rollback`)"
		return st, nil
	}
	res, err := u.rollbackFn(sup)
	if err != nil {
		return Status{}, fmt.Errorf("roll back to %s: %w", st.Target, err)
	}
	st.Reason = fmt.Sprintf("%s (%s)", st.Reason, res.Message)
	return st, nil
}

// install writes the verified inputs to a temp dir, optionally takes a
// database dump, then hands the files to the supervisor path.
func (u *Updater) install(ctx context.Context, st Status, dec Decision, records *recordCache, o RunOpts) (Status, error) {
	supervised := u.supervised(u.env.Supervisor)
	if o.Apply == "" && !supervised && u.alreadyStaged(dec) {
		// Nothing will run it without a supervisor: staging again would only
		// repeat the download and the database dump every check.
		st.Decision, st.Reason = KindStaged, stagedOnly(dec.Target)
		return st, nil
	}
	f, ok := records.m[dec.Target]
	if !ok {
		return Status{}, fmt.Errorf("no record kept for %s", dec.Target)
	}
	releases := filepath.Join(u.env.Data, "releases")
	if err := os.MkdirAll(releases, 0o755); err != nil {
		return Status{}, err
	}
	dir, err := os.MkdirTemp(releases, ".tmp-dl-")
	if err != nil {
		return Status{}, err
	}
	defer os.RemoveAll(dir)
	files := supervisor.ApplyOpts{
		File: filepath.Join(dir, "hatamoto"), Record: filepath.Join(dir, "release.json"),
		Sig: filepath.Join(dir, "release.json.sig"), Release: u.env.Supervisor.Release, Platform: u.env.Platform,
	}
	if err := os.WriteFile(files.Record, f.rb, 0o600); err != nil {
		return Status{}, err
	}
	if err := os.WriteFile(files.Sig, append(f.sig, '\n'), 0o600); err != nil {
		return Status{}, err
	}
	if err := u.fetcher.Download(ctx, dec.Target, u.env.Platform, files.File, dec.Rec.Artifacts[u.env.Platform].Size); err != nil {
		return Status{}, fmt.Errorf("download %s: %w", dec.Target, err)
	}

	note, err := u.backup(ctx, dec.Target, o)
	if err != nil {
		// Keep the target: the owner can retry or update without a backup.
		st.Decision, st.Target, st.LastResult, st.Reason = KindBackupFailed, dec.Target, ResultError, err.Error()
		return st, nil
	}
	u.env.Log.Info("pre-update backup", "note", note)
	if err := ctx.Err(); err != nil { // shutting down: do not switch on the way out
		return Status{}, fmt.Errorf("update of %s cancelled: %w", dec.Target, err)
	}

	switch {
	case o.Apply != "" || supervised:
		res, err := u.applyFn(u.env.Supervisor, files)
		if err != nil {
			return Status{}, fmt.Errorf("apply %s: %w", dec.Target, err)
		}
		st.Decision, st.Reason = KindApplying, res.Message
	default:
		if _, err := u.stageFn(u.env.Supervisor, files); err != nil {
			return Status{}, fmt.Errorf("stage %s: %w", dec.Target, err)
		}
		st.Decision, st.Reason = KindStaged, stagedOnly(dec.Target)
	}
	if note != "" {
		st.Reason += "; " + note
	}
	return st, nil
}

// backup takes the pre-update backup. The returned note goes into the status
// text; an error blocks the update.
func (u *Updater) backup(ctx context.Context, target string, o RunOpts) (string, error) {
	switch {
	case os.Getenv(EnvBackup) == "0" || o.SkipBackup:
		return "backup skipped", nil
	case u.env.Backups == nil:
		return "", errors.New("no backup engine")
	}
	return u.env.Backups.PreUpdate(ctx, target)
}

func stagedOnly(version string) string {
	return fmt.Sprintf("staged %s; run under `hatamoto supervise` to apply", version)
}

// alreadyStaged reports whether releases/<target> holds this exact release:
// a binary, and a stored record that verifies and names the same artifact.
func (u *Updater) alreadyStaged(dec Decision) bool {
	if !supervisor.NewLayout(u.env.Data).Valid(dec.Target) {
		return false
	}
	rec, err := supervisor.StoredRecord(u.env.Supervisor, dec.Target)
	return err == nil && rec.Artifacts[u.env.Platform].SHA256 == dec.Rec.Artifacts[u.env.Platform].SHA256
}

// recordCache fetches and verifies release records once each and keeps the
// signed bytes for staging.
type recordCache struct {
	u     *Updater
	ctx   context.Context
	opts  release.Options
	m     map[string]fetched
	store bool              // keep verified records under releases/.records
	sigs  map[string]string // record_sig of each listed version, from the verified manifest
}

type fetched struct {
	rec     *release.Record
	rb, sig []byte // the signed record bytes and its signature, trimmed
}

func (c *recordCache) get(v string) (*release.Record, error) {
	if f, ok := c.m[v]; ok {
		return f.rec, nil
	}
	if f, ok := c.load(v); ok {
		c.m[v] = f
		return f.rec, nil
	}
	rb, sig, err := c.u.fetcher.Record(c.ctx, v)
	if err != nil {
		return nil, fmt.Errorf("record %s: %w", v, err)
	}
	sig = bytes.TrimSpace(sig)
	rec, err := release.VerifyRecord(rb, string(sig), c.opts)
	if err != nil {
		return nil, fmt.Errorf("record %s: %w", v, err)
	}
	if rec.Version != v {
		return nil, fmt.Errorf("record %s: %w: it names %s", v, release.ErrRecordMismatch, rec.Version)
	}
	c.m[v] = fetched{rec: rec, rb: rb, sig: sig}
	if c.store {
		c.save(v, rb, sig)
	}
	return rec, nil
}

// The record cache keeps what was fetched and verified, so a box that waits
// (window, defer) or is blocked does not fetch the same record every check.
// Entries are verified again on every read, so the directory is not trusted,
// and an entry is used only while its signature is the manifest's record_sig:
// a re-signed record replaces the cached one.
func (c *recordCache) dir() string { return filepath.Join(c.u.env.Data, "releases", ".records") }

func (c *recordCache) load(v string) (fetched, bool) {
	d := filepath.Join(c.dir(), v)
	rb, err := os.ReadFile(filepath.Join(d, "release.json"))
	if err != nil || len(rb) > MaxRecordSize {
		return fetched{}, false
	}
	sb, err := os.ReadFile(filepath.Join(d, "release.json.sig"))
	if err != nil || len(sb) > MaxSigSize {
		return fetched{}, false
	}
	sig := bytes.TrimSpace(sb)
	rec, err := release.VerifyRecord(rb, string(sig), c.opts)
	if err != nil || rec.Version != v || c.sigs[v] == "" || string(sig) != c.sigs[v] {
		return fetched{}, false
	}
	return fetched{rec: rec, rb: rb, sig: sig}, true
}

func (c *recordCache) save(v string, rb, sig []byte) {
	if err := os.MkdirAll(c.dir(), 0o700); err != nil {
		return
	}
	tmp, err := os.MkdirTemp(c.dir(), ".tmp-")
	if err != nil {
		return
	}
	defer os.RemoveAll(tmp)
	if os.WriteFile(filepath.Join(tmp, "release.json"), rb, 0o600) != nil ||
		os.WriteFile(filepath.Join(tmp, "release.json.sig"), append(sig, '\n'), 0o600) != nil {
		return
	}
	final := filepath.Join(c.dir(), v)
	_ = os.RemoveAll(final)
	_ = os.Rename(tmp, final)
}

// prune drops cached records of versions the channel no longer lists.
func (c *recordCache) prune(m *release.ChannelManifest) {
	listed := map[string]bool{}
	for _, e := range m.Releases {
		listed[e.Version] = true
	}
	ents, _ := os.ReadDir(c.dir())
	for _, e := range ents {
		if !listed[e.Name()] {
			_ = os.RemoveAll(filepath.Join(c.dir(), e.Name()))
		}
	}
}
