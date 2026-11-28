package server

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/safefetch"
	"hatamoto.dev/core/internal/staticexport"
	"hatamoto.dev/core/internal/statichost"
	"hatamoto.dev/core/internal/statictrigger"
)

//lint:ignore ST1005 shown to the owner verbatim as a sentence
var errHostingWorkerOff = errors.New("The worker is off, so exports cannot run.")

// hostingSettingsStore is the part of staticexport.SettingsStore the admin
// adapter uses.
type hostingSettingsStore interface {
	Get(ctx context.Context) (staticexport.Settings, bool, error)
	Save(ctx context.Context, set staticexport.Settings) error
}

type hostingRunLister interface {
	List(ctx context.Context, limit int) ([]staticexport.Run, error)
	ListLive(ctx context.Context, limit int) ([]staticexport.Run, error)
	ClearAbandoned(ctx context.Context) error
}

// hostingAdmin implements admin.HostingService over the engine's settings and
// run stores, the island inventory and the export trigger. It only maps: the
// engine validates, seals and stores.
type hostingAdmin struct {
	settings hostingSettingsStore
	runs     hostingRunLister
	key      []byte
	trigger  *statictrigger.Trigger // nil when the worker is off
	// refresh re-reads the island snapshot after a save (no restart needed).
	refresh  func(ctx context.Context)
	siteURL  func(ctx context.Context) string
	snapshot func() statichost.StaticHosting
	// refuse answers a safe error while static hosting cannot be switched on
	// (demo, Site access); nil means nothing refuses.
	refuse   func(ctx context.Context) error
	dirGuard staticexport.DirGuard
	// testExport runs one Test export (Server.StaticTestExport); nil = unavailable.
	testExport func(ctx context.Context, t staticexport.Target, hs statichost.StaticHosting, o staticexport.Options) (staticexport.Result, error)
	background func() context.Context // the server lifecycle context
	spawn      func(func())           // nil = go f(); tests run inline
	log        *slog.Logger
	testing    atomic.Bool         // a Test export is running in this process
	getenv     func(string) string // nil: no private-endpoint opt-in
	lookup     safefetch.Lookup    // nil: no save-time DNS lookup
	setup      hostingSetupDeps
	finished   func() // after a Test export ends (nil = nothing)
}

func (h *hostingAdmin) load(ctx context.Context) (staticexport.Settings, error) {
	set, found, err := h.settings.Get(ctx)
	if err != nil {
		return staticexport.Settings{}, err
	}
	if !found {
		return staticexport.Defaults(), nil
	}
	return set, nil
}

// hostingTargetSaved reports whether the saved target can be exported to.
func hostingTargetSaved(set staticexport.Settings) bool {
	if set.Target == staticexport.TargetS3 {
		return set.Bucket != ""
	}
	return set.DirPath != ""
}

func (h *hostingAdmin) View(ctx context.Context) (admin.HostingView, error) {
	set, err := h.load(ctx)
	if err != nil {
		return admin.HostingView{}, err
	}
	_, readable := set.Secret(h.key)
	v := admin.HostingView{
		Mode: set.Mode, Target: set.Target, Dir: set.DirPath, Service: set.Service, Endpoint: set.Endpoint,
		Bucket: set.Bucket, Prefix: set.Prefix, Region: set.Region, AccessKey: set.AccessKey, LiveOrigin: set.LiveOrigin,
		SecretSaved: readable, SecretLost: set.HasSecret() && !readable,
		WorkerOff: h.trigger == nil,
	}
	if set.Mode == staticexport.ModeStatic {
		if why := h.snapshot().InactiveReason(); why != "" {
			v.IslandsWarning = why // bare: the page adds the sentence around it
		}
	}
	return v, nil
}

func (h *hostingAdmin) Save(ctx context.Context, in admin.HostingInput) ([]ui.FieldError, error) {
	cur, err := h.load(ctx)
	if err != nil {
		return nil, err
	}
	next := cur // the mode is never changed here
	next.DirPath, next.Endpoint, next.Region = in.Dir, in.Endpoint, in.Region
	next.Bucket, next.Prefix, next.AccessKey, next.LiveOrigin = in.Bucket, in.Prefix, in.AccessKey, in.LiveOrigin
	if in.Target != "" {
		next.Target = in.Target
	}
	if in.Service != "" {
		next.Service = in.Service
	}
	if in.SecretKey != "" {
		sealed, err := staticexport.SealSecret(h.key, in.SecretKey)
		if err != nil {
			return nil, errors.New("could not store the secret key")
		}
		next.SecretSealed = sealed
	}
	check := next
	check.Mode = in.Mode
	// Before Validate: a link-local http endpoint would otherwise fail with the
	// generic https message. Built by hand, hostingFieldError would misroute it.
	if check.Target == staticexport.TargetS3 && !staticexport.AllowPrivateS3(h.getenv) {
		if err := staticexport.ResolveS3Endpoint(ctx, check.EffectiveEndpoint(), h.lookup); err != nil {
			return []ui.FieldError{{ID: "hosting-endpoint", Label: "Endpoint",
				Message: "This address is on a private network. Use the storage service's public endpoint, or set " + staticexport.EnvS3AllowPrivate + "=1 on the server for a storage service on your own network."}}, nil
		}
	}
	if err := check.Validate(); err != nil {
		return []ui.FieldError{hostingFieldError(err)}, nil
	}
	// The store validates with the mode it keeps; Save never changes it.
	if err := next.Validate(); err != nil {
		return []ui.FieldError{hostingFieldError(err)}, nil
	}
	if in.Mode == staticexport.ModeStatic {
		if errs := h.staticChecks(ctx, check); len(errs) > 0 {
			return errs, nil
		}
	}
	if err := h.settings.Save(ctx, next); err != nil {
		return nil, err
	}
	h.refresh(ctx)
	return nil, nil
}

// staticChecks are the checks a static configuration must pass: the target
// directory guard and the live origin rule. An empty live origin is the same
// host (the Site URL above is all it needs); a non-empty one must be a plain
// origin on another host.
func (h *hostingAdmin) staticChecks(ctx context.Context, set staticexport.Settings) []ui.FieldError {
	site := h.siteURL(ctx)
	if site == "" {
		return []ui.FieldError{{ID: "hosting-mode", Label: "Mode", Message: "Set the site address in Site settings first."}}
	}
	var errs []ui.FieldError
	if set.Target == staticexport.TargetDir {
		if _, err := staticexport.NewDirTarget(set.DirPath, h.dirGuard); err != nil {
			errs = append(errs, ui.FieldError{ID: "hosting-dir", Label: "Directory", Message: capitalize(fullStopText(err.Error()))})
		}
	}
	if raw := strings.TrimSpace(set.LiveOrigin); raw != "" {
		live, _ := statichost.NormalizeOrigin(raw) // "" for an invalid origin: the live origin message
		if why := (statichost.StaticHosting{Static: true, Live: live, Site: site}).InactiveReason(); why != "" {
			errs = append(errs, ui.FieldError{ID: "hosting-live-origin", Label: "Live origin", Message: capitalize(fullStopText(why))})
		}
	}
	return errs
}

func (h *hostingAdmin) SetMode(ctx context.Context, mode string) error {
	cur, err := h.load(ctx)
	if err != nil {
		return err
	}
	if mode == staticexport.ModeStatic {
		if h.refuse != nil {
			if err := h.refuse(ctx); err != nil {
				return err
			}
		}
		cur.Mode = mode
		if err := cur.Validate(); err != nil {
			return errors.New(capitalize(fullStopText(err.Error())))
		}
		if cur.Target == staticexport.TargetDir {
			if _, err := staticexport.NewDirTarget(cur.DirPath, h.dirGuard); err != nil {
				return errors.New(capitalize(fullStopText(err.Error())))
			}
		} else if _, err := cur.S3Target(h.key); err != nil {
			return errors.New(capitalize(fullStopText(err.Error())))
		}
	} else {
		cur.Mode = staticexport.ModeServed
	}
	if err := h.settings.Save(ctx, cur); err != nil {
		return err
	}
	h.refresh(ctx)
	h.trigger.Refresh()
	return nil
}

func (h *hostingAdmin) Export(ctx context.Context, full bool, trigger string) error {
	if h.trigger == nil {
		return errHostingWorkerOff
	}
	set, err := h.load(ctx)
	if err != nil {
		return err
	}
	if set.Mode != staticexport.ModeStatic {
		return admin.ErrHostingNotStatic
	}
	if err := h.trigger.Now(trigger, full); errors.Is(err, statictrigger.ErrQueued) {
		return admin.ErrHostingExportQueued
	} else if err != nil {
		return err
	}
	return nil
}

func hostingRun(r staticexport.Run) admin.HostingRun {
	out := admin.HostingRun{
		Started: r.StartedAt, Trigger: r.Trigger, Pages: r.Pages, Bytes: r.Bytes, Changed: r.Changed,
		Removed: r.Removed, Duration: r.Duration, Errors: len(r.Errors), Result: r.Status,
	}
	if len(r.Errors) > 0 {
		out.FirstError = r.Errors[0]
	}
	return out
}

// TestExport writes one export to the saved target in the background, with the
// island snapshot of static mode applied to that export only (s.hosting is
// never touched). The run is recorded with the test trigger; it sends no mail
// and prunes nothing, because a test run is never the live export.
func (h *hostingAdmin) TestExport(ctx context.Context) error {
	if h.testExport == nil {
		return errors.New("test export is not available on this server")
	}
	set, err := h.load(ctx)
	if err != nil {
		return err
	}
	if !hostingTargetSaved(set) {
		return admin.ErrHostingNoTarget
	}
	if h.refuse != nil {
		if err := h.refuse(ctx); err != nil {
			return err
		}
	}
	t, err := staticexport.SavedTarget(set, h.key, h.dirGuard, os.Getenv)
	if err != nil {
		return err
	}
	// A crashed run keeps its running row until something reaps it; a served
	// site never calls Start, so reap here or Test export stays blocked.
	if err := h.runs.ClearAbandoned(ctx); err != nil {
		return err
	}
	if runs, err := h.runs.List(ctx, 1); err == nil && len(runs) > 0 && runs[0].Status == admin.HostingRunRunning {
		return admin.ErrHostingTestBusy
	}
	if !h.testing.CompareAndSwap(false, true) {
		return admin.ErrHostingTestBusy
	}
	// Test export runs as if static, same host included: SameHost comes from the
	// raw saved live origin, so force the mode on a copy before mapping.
	asStatic := set
	asStatic.Mode = staticexport.ModeStatic
	hs := hostingSnapshot(asStatic, true, h.siteURL(ctx))
	run := func() {
		defer h.testing.Store(false)
		ctx, cancel := context.WithTimeout(h.background(), exportTimeout)
		defer cancel()
		if _, err := h.testExport(ctx, t, hs, staticexport.Options{Trigger: admin.HostingTriggerTest}); err != nil && h.log != nil {
			h.log.Warn("static test export", slog.String("error", err.Error()))
		}
		if h.finished != nil {
			h.finished()
		}
	}
	if h.spawn != nil {
		h.spawn(run)
	} else {
		go run()
	}
	return nil
}

func (h *hostingAdmin) Runs(ctx context.Context, limit int) ([]admin.HostingRun, error) {
	runs, err := h.runs.List(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]admin.HostingRun, 0, len(runs))
	for _, r := range runs {
		out = append(out, hostingRun(r))
	}
	return out, nil
}

func (h *hostingAdmin) LiveRuns(ctx context.Context, limit int) ([]admin.HostingRun, error) {
	runs, err := h.runs.ListLive(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]admin.HostingRun, 0, len(runs))
	for _, r := range runs {
		out = append(out, hostingRun(r))
	}
	return out, nil
}

func (h *hostingAdmin) LastRun(ctx context.Context) (admin.HostingRun, bool, error) {
	runs, err := h.runs.ListLive(ctx, 5)
	if err != nil {
		return admin.HostingRun{}, false, err
	}
	for _, r := range runs {
		if r.Status != admin.HostingRunRunning && r.Trigger != admin.HostingTriggerTest {
			return hostingRun(r), true, nil
		}
	}
	return admin.HostingRun{}, false, nil
}

// ProbeHostKey and TrustHostKey are placeholders until the SFTP adapter lands.
func (h *hostingAdmin) ProbeHostKey(context.Context) (string, error) {
	return "", errors.New("host key checks are not available on this server")
}

func (h *hostingAdmin) TrustHostKey(context.Context, string) error {
	return errors.New("host key checks are not available on this server")
}

func (h *hostingAdmin) Islands() ([]admin.HostingIsland, []string) {
	var live []admin.HostingIsland
	var stays []string
	status := "live"
	if hs := h.snapshot(); !hs.Active() && hs.IslandsOn() {
		status = "via proxy" // same host: the web server passes these paths here
	}
	for _, it := range statichost.Inventory() {
		if it.Where != statichost.Live {
			stays = append(stays, it.Name)
			continue
		}
		route := it.Path
		if route == "" && len(it.Routes) > 0 {
			route = it.Routes[0]
		}
		live = append(live, admin.HostingIsland{Label: it.Name, Route: route, Status: status})
	}
	return live, stays
}

// hostingFieldError ties an engine validation message to the field it is about.
func hostingFieldError(err error) ui.FieldError {
	msg := err.Error()
	low := strings.ToLower(msg)
	for _, f := range []struct{ word, id, label string }{
		{"directory", "hosting-dir", "Directory"}, {"secret key", "hosting-secret-key", "Secret key"},
		{"service", "hosting-service", "Storage service"}, {"region", "hosting-region", "Region"},
		{"bucket", "hosting-bucket", "Bucket"}, {"prefix", "hosting-prefix", "Folder prefix"},
		{"access key", "hosting-access-key", "Access key"}, {"endpoint", "hosting-endpoint", "Endpoint"},
		{"mode", "hosting-mode", "Mode"}, {"target", "hosting-target", "Publish to"},
	} {
		if strings.Contains(low, f.word) {
			return ui.FieldError{ID: f.id, Label: f.label, Message: capitalize(fullStopText(msg))}
		}
	}
	return ui.FieldError{ID: "hosting-endpoint", Label: "Endpoint", Message: capitalize(fullStopText(msg))}
}
