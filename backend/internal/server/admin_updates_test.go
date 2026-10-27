package server

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/updater"
)

type fakeRunner struct {
	mu      sync.Mutex
	calls   []updater.RunOpts
	status  updater.Status
	err     error
	release chan struct{} // when set, Run blocks until it is closed
	started chan struct{}
	done    chan struct{}
}

func (f *fakeRunner) Run(_ context.Context, o updater.RunOpts) (updater.Status, error) {
	f.mu.Lock()
	f.calls = append(f.calls, o)
	f.mu.Unlock()
	if f.started != nil {
		select {
		case f.started <- struct{}{}:
		default:
		}
	}
	if f.release != nil {
		<-f.release
	}
	if f.done != nil {
		defer close(f.done)
	}
	return f.status, f.err
}

func (f *fakeRunner) callList() []updater.RunOpts {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]updater.RunOpts(nil), f.calls...)
}

type fakeStore struct {
	set    updater.Settings
	status updater.Status
	saved  []updater.Settings // the stored policy after each save
	cols   [][]string         // the columns each save was allowed to write
}

func (f *fakeStore) Load(context.Context) (updater.Settings, updater.Status, error) {
	return f.set, f.status, nil
}
func (f *fakeStore) SavePolicy(_ context.Context, s updater.Settings, cols ...string) error {
	if err := s.Validate(); err != nil {
		return err
	}
	for _, c := range cols {
		switch c {
		case updater.ColMode:
			f.set.Mode = s.Mode
		case updater.ColChannel:
			f.set.Channel = s.Channel
		case updater.ColWindow:
			f.set.WindowStart, f.set.WindowEnd = s.WindowStart, s.WindowEnd
		case updater.ColDefer:
			f.set.DeferDays = s.DeferDays
		case updater.ColPin:
			f.set.Pin = s.Pin
		}
	}
	f.saved = append(f.saved, f.set)
	f.cols = append(f.cols, cols)
	return nil
}

type fakeLog struct {
	history  []admin.UpdatesHistoryRow
	off      *time.Time
	sets     int
	offReads int
	err      error
}

func (f *fakeLog) History(context.Context, int) ([]admin.UpdatesHistoryRow, error) {
	return f.history, f.err
}
func (f *fakeLog) OffSince(context.Context) (*time.Time, error) { f.offReads++; return f.off, f.err }
func (f *fakeLog) SetOffSince(_ context.Context, at *time.Time) error {
	f.sets++
	f.off = at
	return nil
}

var adapterNow = time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

type adapterRig struct {
	*adminUpdates
	run   *fakeRunner
	store *fakeStore
	log   *fakeLog
	env   map[string]string
	clock time.Time
}

func newAdapterRig() *adapterRig {
	r := &adapterRig{run: &fakeRunner{}, store: &fakeStore{}, log: &fakeLog{}, env: map[string]string{}, clock: adapterNow}
	r.adminUpdates = &adminUpdates{
		run: r.run, store: r.store, log: r.log, logger: slog.New(slog.DiscardHandler),
		env:     func(k string) string { return r.env[k] },
		now:     func() time.Time { return r.clock },
		running: func() string { return "v1.4.1" },
	}
	return r
}

func TestAdapterViewReportsEffectivePolicyAndLocks(t *testing.T) {
	r := newAdapterRig()
	start, end := 2, 5
	r.store.set = updater.Settings{Mode: "notify", Channel: "stable", WindowStart: &start, WindowEnd: &end, Pin: "v1.4", DeferDays: 3}
	r.store.status = updater.Status{Decision: "scheduled", Target: "v1.4.2", LastResult: "ok"}
	r.log.history = []admin.UpdatesHistoryRow{{From: "v1.4.0", To: "v1.4.1", Phase: "success"}}

	v, err := r.View(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if v.Running != "v1.4.1" || v.Dev || v.Mode != "notify" || v.Channel != "stable" || v.Pin != "v1.4" || v.DeferDays != 3 || *v.WindowStart != 2 || *v.WindowEnd != 5 {
		t.Errorf("view = %+v", v)
	}
	if v.Locks != (admin.UpdatesLocks{}) {
		t.Errorf("locks without env: %+v", v.Locks)
	}
	if v.Status.Decision != "scheduled" || v.Status.Target != "v1.4.2" || len(v.History) != 1 {
		t.Errorf("status/history = %+v %+v", v.Status, v.History)
	}

	// Environment wins and locks the field; an invalid channel does not lock.
	r.env[updater.EnvMode] = "off"
	r.env[updater.EnvChannel] = "edge"
	v, _ = r.View(context.Background())
	if v.Mode != "off" || v.Channel != "edge" || !v.Locks.Mode || !v.Locks.Channel || v.Locks.ModeVar != "HATAMOTO_UPDATES" || v.Locks.ChannelVar != "HATAMOTO_UPDATE_CHANNEL" {
		t.Errorf("env view = %+v", v)
	}
	r.env[updater.EnvChannel] = "canary"
	if v, _ = r.View(context.Background()); v.Channel != "canary" || !v.Locks.Channel {
		t.Errorf("env canary not applied and locked: %+v", v)
	}
	r.env[updater.EnvChannel] = "nightly"
	if v, _ = r.View(context.Background()); v.Locks.Channel || v.Channel != "stable" {
		t.Errorf("invalid env channel locked or applied: %+v", v)
	}
	// An unreadable mode fails closed to off and still locks.
	r.env[updater.EnvMode] = "maybe"
	if v, _ = r.View(context.Background()); v.Mode != "off" || !v.Locks.Mode {
		t.Errorf("bad env mode: %+v", v)
	}
}

func TestAdapterViewDevBuild(t *testing.T) {
	r := newAdapterRig()
	r.running = func() string { return "dev" }
	if v, _ := r.View(context.Background()); !v.Dev {
		t.Error("a non-release version must read as a dev build")
	}
}

func TestAdapterOffSinceStampAndClear(t *testing.T) {
	r := newAdapterRig()
	ctx := context.Background()
	r.store.set.Mode = "off"
	v, _ := r.View(ctx)
	if r.log.off == nil || !r.log.off.Equal(adapterNow) || v.OffSince == nil || !v.OffSince.Equal(adapterNow) {
		t.Fatalf("first sight of off must stamp now: %v / %+v", r.log.off, v.OffSince)
	}
	// Idempotent: the stamp keeps its first value and is not written again.
	r.clock = adapterNow.Add(24 * time.Hour)
	r.View(ctx)
	if r.log.sets != 1 || !r.log.off.Equal(adapterNow) {
		t.Errorf("stamp rewritten: sets=%d off=%v", r.log.sets, r.log.off)
	}
	// Env-off counts too, with the stored mode on.
	r2 := newAdapterRig()
	r2.env[updater.EnvMode] = "off"
	r2.View(ctx)
	if r2.log.off == nil {
		t.Error("env-off was not stamped")
	}
	// Back on: cleared.
	r.store.set.Mode = "auto"
	if v, _ = r.View(ctx); r.log.off != nil || v.OffSince != nil {
		t.Errorf("stamp not cleared when updates are on: %v", r.log.off)
	}
}

func TestAdapterSaveAppliesFormAndHonoursLocks(t *testing.T) {
	r := newAdapterRig()
	r.store.set = updater.Settings{Mode: "auto", Channel: "stable", Pin: "v1.4"}
	start, end := 1, 4
	form := admin.UpdatesForm{Mode: "notify", Channel: "edge", WindowStart: &start, WindowEnd: &end, DeferDays: 7}
	if errs, err := r.Save(context.Background(), form); err != nil || len(errs) != 0 {
		t.Fatalf("save = %v %v", errs, err)
	}
	got := r.store.saved[0]
	if got.Mode != "notify" || got.Channel != "edge" || got.DeferDays != 7 || *got.WindowStart != 1 || got.Pin != "v1.4" {
		t.Errorf("saved %+v (the pin must survive a save)", got)
	}

	r.store.saved = nil
	r.env[updater.EnvMode] = "off"
	r.env[updater.EnvChannel] = "stable"
	form.Mode, form.Channel = "auto", "edge"
	if _, err := r.Save(context.Background(), form); err != nil {
		t.Fatal(err)
	}
	if got := r.store.saved[0]; got.Mode != "notify" || got.Channel != "edge" {
		t.Errorf("locked fields were overwritten: stored mode/channel now %q/%q, want unchanged notify/edge", got.Mode, got.Channel)
	}
}

func TestAdapterSaveAcceptsCanary(t *testing.T) {
	r := newAdapterRig()
	r.store.set = updater.Settings{Mode: "auto", Channel: "stable"}
	form := admin.UpdatesForm{Mode: "auto", Channel: "canary"}
	if errs, err := r.Save(context.Background(), form); err != nil || len(errs) != 0 {
		t.Fatalf("save = %v %v", errs, err)
	}
	if got := r.store.saved[0]; got.Channel != "canary" {
		t.Errorf("saved %+v", got)
	}
	r.store.saved = nil
	r.store.set = updater.Settings{Mode: "auto", Channel: "stable"}
	r.env[updater.EnvChannel] = "canary"
	form.Channel = "edge"
	if _, err := r.Save(context.Background(), form); err != nil {
		t.Fatal(err)
	}
	if got := r.store.saved[0]; got.Channel != "stable" {
		t.Errorf("a canary env lock must stop the form changing the stored channel: %+v", got)
	}
}

func TestAdapterSaveMapsValidationToFields(t *testing.T) {
	r := newAdapterRig()
	for field, form := range map[string]admin.UpdatesForm{
		"mode":         {Mode: "yolo", Channel: "stable"},
		"channel":      {Mode: "auto", Channel: "nightly"},
		"window-start": {Mode: "auto", Channel: "stable", WindowStart: ptrI(30), WindowEnd: ptrI(2)},
	} {
		errs, err := r.Save(context.Background(), form)
		if err != nil || errs[field] == "" {
			t.Errorf("%s: errs=%v err=%v", field, errs, err)
		}
	}
	if len(r.store.saved) != 0 {
		t.Error("an invalid policy was saved")
	}
}

func ptrI(n int) *int { return &n }

func TestAdapterUnpinKeepsTheRest(t *testing.T) {
	r := newAdapterRig()
	r.store.set = updater.Settings{Mode: "notify", Channel: "edge", Pin: "v1.4", DeferDays: 2}
	if err := r.Unpin(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := r.store.saved[0]; got.Pin != "" || got.Mode != "notify" || got.Channel != "edge" || got.DeferDays != 2 {
		t.Errorf("saved %+v", got)
	}
}

func TestAdapterCheckStoresButNeverInstalls(t *testing.T) {
	r := newAdapterRig()
	r.run.status = updater.Status{Decision: "scheduled", Target: "v1.4.2"}
	res, err := r.Check(context.Background())
	if err != nil || res.Status.Decision != "scheduled" || res.Busy {
		t.Fatalf("check = %+v %v", res, err)
	}
	if calls := r.run.callList(); len(calls) != 1 || calls[0] != (updater.RunOpts{CheckOnly: true}) {
		t.Errorf("Run calls = %+v, want one check-only run", calls)
	}
	r.run.status = updater.Status{Decision: updater.KindBusy}
	if res, _ := r.Check(context.Background()); !res.Busy {
		t.Error("a busy updater is not reported")
	}
	r.run.err = errors.New("timeout")
	if _, err := r.Check(context.Background()); err == nil {
		t.Error("the run error is swallowed")
	}
}

func TestAdapterApplyDerivesTheTarget(t *testing.T) {
	for _, c := range []struct {
		name   string
		status updater.Status
		want   string
	}{
		{"major", updater.Status{Decision: updater.KindAvailableMajor, AvailableMajor: "v2.0.0", Target: "v1.4.2"}, "v2.0.0"},
		{"scheduled skips the ramp", updater.Status{Decision: updater.KindScheduled, Target: "v1.4.2", AvailableMajor: "v2.0.0"}, "v1.4.2"},
		{"waiting window", updater.Status{Decision: updater.KindWaitingWindow, Target: "v1.4.2"}, "v1.4.2"},
		{"waiting defer", updater.Status{Decision: updater.KindWaitingDefer, Target: "v1.4.2"}, "v1.4.2"},
		{"notify mode", updater.Status{Decision: updater.KindNotify, Target: "v1.4.2"}, "v1.4.2"},
		{"retry after a failed update", updater.Status{Decision: "update_failed", Target: "v1.4.2"}, "v1.4.2"},
		{"retry after a busy one", updater.Status{Decision: updater.KindBusy, Target: "v1.4.2"}, "v1.4.2"},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newAdapterRig()
			r.store.status = c.status
			r.run.done = make(chan struct{})
			res, err := r.Apply(context.Background())
			if err != nil || !res.Started || res.Target != c.want {
				t.Fatalf("apply = %+v %v", res, err)
			}
			<-r.run.done
			calls := r.run.callList()
			if len(calls) != 1 || calls[0] != (updater.RunOpts{Apply: c.want, Managed: true}) {
				t.Errorf("Run calls = %+v, want one with Apply=%s", calls, c.want)
			}
		})
	}
}

func TestAdapterApplyWithoutBackupSkipsOnlyThatRun(t *testing.T) {
	r := newAdapterRig()
	r.store.status = updater.Status{Decision: updater.KindBackupFailed, Target: "v1.4.2"}
	r.run.done = make(chan struct{})
	if res, err := r.ApplyWithoutBackup(context.Background()); err != nil || !res.Started || res.Target != "v1.4.2" {
		t.Fatalf("apply without backup = %+v %v", res, err)
	}
	<-r.run.done
	r.Wait(context.Background())

	// A plain Apply afterwards backs up again.
	r.run.done = make(chan struct{})
	if _, err := r.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-r.run.done
	r.Wait(context.Background())
	calls := r.run.callList()
	want := []updater.RunOpts{{Apply: "v1.4.2", Managed: true, SkipBackup: true}, {Apply: "v1.4.2", Managed: true}}
	if len(calls) != 2 || calls[0] != want[0] || calls[1] != want[1] {
		t.Errorf("Run calls = %+v, want %+v", calls, want)
	}
}

func TestAdapterApplyWithoutBackupRefusesOutsideBackupFailed(t *testing.T) {
	r := newAdapterRig()
	r.store.status = updater.Status{Decision: "update_failed", Target: "v1.4.2"}
	if _, err := r.ApplyWithoutBackup(context.Background()); !errors.Is(err, admin.ErrNoUpdate) {
		t.Fatalf("err = %v, want ErrNoUpdate", err)
	}
	if n := len(r.run.callList()); n != 0 {
		t.Errorf("Run called %d times", n)
	}
}

func TestAdapterApplyRefusesWithoutATarget(t *testing.T) {
	for name, set := range map[string]func(*adapterRig){
		"up to date": func(r *adapterRig) { r.store.status = updater.Status{Decision: updater.KindUpToDate, Target: "v1.4.2"} },
		"blocked":    func(r *adapterRig) { r.store.status = updater.Status{Decision: updater.KindBlocked, Target: "v1.4.2"} },
		"nothing":    func(r *adapterRig) {},
		"dev build": func(r *adapterRig) {
			r.running = func() string { return "dev" }
			r.store.status = updater.Status{Decision: updater.KindScheduled, Target: "v1.4.2"}
		},
		"env switches updates off": func(r *adapterRig) {
			r.env[updater.EnvMode] = "off"
			r.store.status = updater.Status{Decision: updater.KindScheduled, Target: "v1.4.2"}
		},
		"notify about a rollback": func(r *adapterRig) {
			r.store.status = updater.Status{Decision: updater.KindNotify, Target: "v1.4.0", Reason: "v1.4.1 was withdrawn; rolling back"}
		},
		"scheduled busy skip has no target": func(r *adapterRig) {
			r.store.status = updater.Status{Decision: updater.KindBusy}
		},
		"failed update, then the operator installed a newer release": func(r *adapterRig) {
			r.running = func() string { return "v1.5.1" }
			r.store.status = updater.Status{Decision: "update_failed", Target: "v1.5.0"}
		},
		"major older than running": func(r *adapterRig) {
			r.running = func() string { return "v2.1.0" }
			r.store.status = updater.Status{Decision: updater.KindAvailableMajor, AvailableMajor: "v2.0.0"}
		},
		"target already running": func(r *adapterRig) {
			r.store.status = updater.Status{Decision: updater.KindScheduled, Target: "v1.4.1"}
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := newAdapterRig()
			set(r)
			if _, err := r.Apply(context.Background()); !errors.Is(err, admin.ErrNoUpdate) {
				t.Errorf("err = %v, want ErrNoUpdate", err)
			}
			if calls := r.run.callList(); len(calls) != 0 {
				t.Errorf("the updater ran: %+v", calls)
			}
		})
	}
}

func TestAdapterApplyDoubleClickRunsOnce(t *testing.T) {
	r := newAdapterRig()
	r.store.status = updater.Status{Decision: updater.KindScheduled, Target: "v1.4.2"}
	r.run.release = make(chan struct{})
	r.run.started = make(chan struct{}, 1)
	r.run.done = make(chan struct{})
	ctx := context.Background()
	first, err := r.Apply(ctx)
	if err != nil || !first.Started {
		t.Fatalf("first apply = %+v %v", first, err)
	}
	<-r.run.started
	if v, _ := r.View(ctx); !v.Applying {
		t.Error("the page cannot tell an update is running")
	}
	second, err := r.Apply(ctx)
	if err != nil || second.Started || !second.Busy {
		t.Errorf("second apply = %+v %v, want busy and not started", second, err)
	}
	close(r.run.release)
	<-r.run.done
	if calls := r.run.callList(); len(calls) != 1 {
		t.Errorf("Run calls = %d, want 1", len(calls))
	}
	// Once finished the guard is released.
	waitFor(t, func() bool { return !r.applying.Load() })
}

func TestAdapterApplyOutlivesTheRequest(t *testing.T) {
	r := newAdapterRig()
	r.store.status = updater.Status{Decision: updater.KindScheduled, Target: "v1.4.2"}
	var seen context.Context
	r.adminUpdates.run = runnerFunc(func(ctx context.Context, o updater.RunOpts) (updater.Status, error) {
		seen = ctx
		return updater.Status{}, nil
	})
	reqCtx, cancel := context.WithCancel(context.Background())
	if _, err := r.Apply(reqCtx); err != nil {
		t.Fatal(err)
	}
	cancel() // the redirect has been sent
	waitFor(t, func() bool { return !r.applying.Load() })
	if seen == nil {
		t.Fatal("Run never ran")
	}
	if _, ok := seen.Deadline(); !ok {
		t.Error("the background run has no deadline")
	}
}

type runnerFunc func(context.Context, updater.RunOpts) (updater.Status, error)

func (f runnerFunc) Run(ctx context.Context, o updater.RunOpts) (updater.Status, error) {
	return f(ctx, o)
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestAdapterBannerIsCached(t *testing.T) {
	r := newAdapterRig()
	ctx := context.Background()
	r.store.status = updater.Status{Decision: updater.KindBlocked, Reason: "plugin hello needs SDK ^0.8"}
	b, err := r.Banner(ctx)
	if err != nil || b == nil || b.Kind != "blocked" {
		t.Fatalf("banner = %+v %v", b, err)
	}
	reads := r.log.offReads
	r.store.status = updater.Status{Decision: updater.KindUpToDate}
	r.clock = adapterNow.Add(10 * time.Second)
	if b, _ := r.Banner(ctx); b == nil || r.log.offReads != reads {
		t.Error("the banner was recomputed inside the cache window")
	}
	r.clock = adapterNow.Add(31 * time.Second)
	if b, _ := r.Banner(ctx); b != nil {
		t.Errorf("stale banner after the window: %+v", b)
	}
}

func TestAdapterBannerSavesDropTheCache(t *testing.T) {
	r := newAdapterRig()
	ctx := context.Background()
	r.store.status = updater.Status{Decision: updater.KindBlocked, Reason: "x"}
	r.Banner(ctx)
	r.store.status = updater.Status{}
	if _, err := r.Save(ctx, admin.UpdatesForm{Mode: "auto", Channel: "stable"}); err != nil {
		t.Fatal(err)
	}
	if b, _ := r.Banner(ctx); b != nil {
		t.Errorf("banner survived a save: %+v", b)
	}
}

func TestAdapterGlanceIsCachedAndReadOnly(t *testing.T) {
	r := newAdapterRig()
	ctx := context.Background()
	r.store.set.Mode = "off" // a banner would stamp off-since; a glance must not
	r.store.status = updater.Status{Decision: updater.KindUpToDate}
	r.log.history = []admin.UpdatesHistoryRow{{From: "v1.4.0", To: "v1.4.1", Phase: "success"}}
	g, err := r.Glance(ctx)
	if err != nil || g.Running != "v1.4.1" || g.Mode != "off" || len(g.History) != 1 {
		t.Fatalf("glance = %+v %v", g, err)
	}
	if r.log.sets != 0 || r.log.off != nil {
		t.Errorf("glance wrote off-since: sets %d", r.log.sets)
	}
	reads := r.log.offReads
	r.store.status = updater.Status{Decision: updater.KindBlocked}
	r.clock = adapterNow.Add(10 * time.Second)
	if g, _ := r.Glance(ctx); g.Status.Decision != updater.KindUpToDate || r.log.offReads != reads {
		t.Error("the glance was recomputed inside the cache window")
	}
	r.clock = adapterNow.Add(31 * time.Second)
	if g, _ := r.Glance(ctx); g.Status.Decision != updater.KindBlocked {
		t.Errorf("stale glance after the window: %+v", g.Status)
	}
}

func TestAdapterGlanceDropsTheCacheOnSaveAndUnpin(t *testing.T) {
	r := newAdapterRig()
	ctx := context.Background()
	r.Glance(ctx)
	r.store.status = updater.Status{Decision: updater.KindBlocked}
	if _, err := r.Save(ctx, admin.UpdatesForm{Mode: "auto", Channel: "stable"}); err != nil {
		t.Fatal(err)
	}
	if g, _ := r.Glance(ctx); g.Status.Decision != updater.KindBlocked {
		t.Errorf("glance survived a save: %+v", g.Status)
	}
	r.store.status = updater.Status{Decision: updater.KindUpToDate}
	if err := r.Unpin(ctx); err != nil {
		t.Fatal(err)
	}
	if g, _ := r.Glance(ctx); g.Status.Decision != updater.KindUpToDate {
		t.Errorf("glance survived an unpin: %+v", g.Status)
	}
}

func TestAdapterGlanceKeepsServingWithoutHistory(t *testing.T) {
	r := newAdapterRig()
	r.log.err = errors.New("history down")
	g, err := r.Glance(context.Background())
	if err != nil || g.History != nil {
		t.Errorf("glance = %+v %v", g, err)
	}
}

func TestAdapterBannerErrorMeansNoBanner(t *testing.T) {
	r := newAdapterRig()
	r.log.err = errors.New("no such column")
	r.store.set.Mode = "off"
	if b, err := r.Banner(context.Background()); b != nil || err == nil {
		t.Errorf("banner = %+v err = %v", b, err)
	}
}

func TestAdapterBannerNoneOnDevBuild(t *testing.T) {
	r := newAdapterRig()
	r.running = func() string { return "dev" }
	r.store.status = updater.Status{Decision: updater.KindRolledBack, Target: "v1.4.2"}
	if b, _ := r.Banner(context.Background()); b != nil {
		t.Errorf("dev build banner: %+v", b)
	}
}

func TestRefreshAdminUpdatesWithoutAdminOrAgent(t *testing.T) {
	s := &Server{}
	s.NoUpdater(errors.New("update URL must be https")) // no admin mounted: must not panic
	svc, why := s.adminUpdatesDeps()
	if svc != nil || why != "update URL must be https" {
		t.Errorf("deps = %v, %q", svc, why)
	}
}

// ctxRunner blocks until its context ends, as a download would.
type ctxRunner struct {
	started chan struct{}
	err     chan error
}

func (c *ctxRunner) Run(ctx context.Context, _ updater.RunOpts) (updater.Status, error) {
	c.started <- struct{}{}
	<-ctx.Done()
	c.err <- ctx.Err()
	return updater.Status{}, ctx.Err()
}

func TestAdapterApplyEndsWithTheServerAndWaitJoinsIt(t *testing.T) {
	r := newAdapterRig()
	life, cancel := context.WithCancel(context.Background())
	r.base = func() context.Context { return life }
	cr := &ctxRunner{started: make(chan struct{}, 1), err: make(chan error, 1)}
	r.adminUpdates.run = cr
	r.store.status = updater.Status{Decision: updater.KindScheduled, Target: "v1.4.2"}
	// The request context ends with the request; the run must not.
	reqCtx, endRequest := context.WithCancel(context.Background())
	if res, err := r.Apply(reqCtx); err != nil || !res.Started {
		t.Fatalf("apply = %+v %v", res, err)
	}
	<-cr.started
	endRequest()
	select {
	case err := <-cr.err:
		t.Fatalf("the run ended with the request: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	cancel() // the server shuts down
	waited := make(chan struct{})
	go func() { r.Wait(context.Background()); close(waited) }()
	select {
	case <-waited:
	case <-time.After(2 * time.Second):
		t.Fatal("Wait did not return after the lifecycle context ended")
	}
	if err := <-cr.err; !errors.Is(err, context.Canceled) {
		t.Fatalf("run ended with %v, want the shutdown", err)
	}
}

// The banner points at the Updates page, so it must agree with what the page
// offers for the same stored state.
func TestAdapterBannerAgreesWithTheUpdatesPage(t *testing.T) {
	old := adapterNow.Add(-100 * 24 * time.Hour)
	for name, c := range map[string]struct {
		mode   string
		status updater.Status
		want   string // banner kind, "" for none
	}{
		"major offered (the chip's)":        {"auto", updater.Status{Decision: updater.KindAvailableMajor, AvailableMajor: "v2.0.0"}, ""},
		"updates off 100 days (the chip's)": {"off", updater.Status{Decision: updater.KindAvailableMajor, AvailableMajor: "v2.0.0"}, ""},
		"minor scheduled, major also seen":  {"auto", updater.Status{Decision: updater.KindScheduled, Target: "v1.4.2", AvailableMajor: "v2.0.0"}, ""},
		"pinned, major also seen":           {"auto", updater.Status{Decision: updater.KindUpToDate, AvailableMajor: "v2.0.0"}, ""},
		"withdrawn release rolled back":     {"auto", updater.Status{Decision: updater.KindRolledBackYank, Target: "v1.4.0", Reason: "v1.4.1 was withdrawn"}, "rolled_back"},
	} {
		t.Run(name, func(t *testing.T) {
			r := newAdapterRig()
			r.store.set.Mode = c.mode
			r.store.status = c.status
			if c.mode == "off" {
				r.log.off = &old
			}
			b, err := r.Banner(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			got := ""
			if b != nil {
				got = b.Kind
			}
			if got != c.want {
				t.Fatalf("banner = %q, want %q", got, c.want)
			}
		})
	}
}

func TestAdapterSaveWritesOnlyWhatChanged(t *testing.T) {
	r := newAdapterRig()
	// Nothing stored: every policy column is at its default.
	form := admin.UpdatesForm{Mode: "auto", Channel: "stable", DeferDays: 3}
	if errs, err := r.Save(context.Background(), form); err != nil || len(errs) != 0 {
		t.Fatalf("save = %v %v", errs, err)
	}
	if got := r.store.cols[0]; len(got) != 1 || got[0] != updater.ColDefer {
		t.Errorf("wrote columns %v, want only defer_days (the form posts the effective mode and channel)", got)
	}
	if r.store.set.Mode != "" || r.store.set.Channel != "" {
		t.Errorf("a default became a stored value: %+v", r.store.set)
	}
	// A save that changes nothing writes nothing but still succeeds.
	r.store.cols = nil
	if _, err := r.Save(context.Background(), form); err != nil {
		t.Fatal(err)
	}
	if len(r.store.cols) != 1 || len(r.store.cols[0]) != 0 {
		t.Errorf("columns written for an unchanged form: %v", r.store.cols)
	}
}

func TestAdapterSaveNeverTouchesThePin(t *testing.T) {
	r := newAdapterRig()
	r.store.set = updater.Settings{Mode: "auto", Pin: "v1.4"}
	form := admin.UpdatesForm{Mode: "notify", Channel: "stable"}
	if _, err := r.Save(context.Background(), form); err != nil {
		t.Fatal(err)
	}
	for _, c := range r.store.cols[0] {
		if c == updater.ColPin {
			t.Fatalf("Save wrote the pin: %v", r.store.cols[0])
		}
	}
}

func TestAdapterUnpinWritesOnlyThePin(t *testing.T) {
	r := newAdapterRig()
	r.store.set = updater.Settings{Mode: "notify", Pin: "v1.4"}
	if err := r.Unpin(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := r.store.cols[0]; len(got) != 1 || got[0] != updater.ColPin {
		t.Errorf("columns %v", got)
	}
}

func TestAdapterViewNeverOffersAnOlderTarget(t *testing.T) {
	r := newAdapterRig()
	r.running = func() string { return "v1.5.1" }
	r.store.status = updater.Status{Decision: "update_failed", Target: "v1.5.0", AvailableMajor: "v1.0.0"}
	st, err := r.state(context.Background())
	if err != nil || st.Status.Target != "" || st.Status.AvailableMajor != "" || st.ApplyTarget() != "" {
		t.Fatalf("state = %+v %v: a target that is not newer than running must be cleared", st.Status, err)
	}
}

func TestAdapterGlanceDropsTheCacheOnCheckAndApplyStart(t *testing.T) {
	r := newAdapterRig()
	ctx := context.Background()
	r.Glance(ctx)
	r.store.status = updater.Status{Decision: updater.KindScheduled, Target: "v1.4.2"}
	if _, err := r.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if g, _ := r.Glance(ctx); g.Status.Decision != updater.KindScheduled {
		t.Errorf("glance survived a check: %+v", g.Status)
	}

	// Apply drops it when the run starts, not only when it ends.
	r.run.release = make(chan struct{})
	r.run.started = make(chan struct{}, 1)
	r.run.done = make(chan struct{})
	if res, err := r.Apply(ctx); err != nil || !res.Started {
		t.Fatalf("apply = %+v %v", res, err)
	}
	<-r.run.started
	// The store still holds the pre-run status, as in production until the
	// run ends: only the live flag says an update is running.
	if g, _ := r.Glance(ctx); !g.Applying || g.Status.Decision != updater.KindScheduled {
		t.Errorf("glance during an apply = applying %v, %+v", g.Applying, g.Status)
	}
	// A cached glance must not hide the flag either.
	if g, _ := r.Glance(ctx); !g.Applying {
		t.Error("cached glance lost the live applying flag")
	}
	close(r.run.release)
	<-r.run.done
	r.Wait(ctx)
}

func TestAdapterGlanceDropsTheCacheWhenCheckFails(t *testing.T) {
	r := newAdapterRig()
	ctx := context.Background()
	r.Glance(ctx)
	r.store.status = updater.Status{Decision: "check_failed", LastResult: "error"}
	r.run.err = errors.New("offline")
	if _, err := r.Check(ctx); err == nil {
		t.Fatal("check should fail")
	}
	if g, _ := r.Glance(ctx); g.Status.Decision != "check_failed" {
		t.Errorf("glance survived a failed check: %+v", g.Status)
	}
}
