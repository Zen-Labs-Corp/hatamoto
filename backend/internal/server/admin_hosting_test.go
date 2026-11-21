package server

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/staticexport"
	"hatamoto.dev/core/internal/statichost"
	"hatamoto.dev/core/internal/statictrigger"
)

type fakeHostingStore struct {
	set     staticexport.Settings
	found   bool
	saves   int
	saveErr error
}

func (f *fakeHostingStore) Get(context.Context) (staticexport.Settings, bool, error) {
	return f.set, f.found, nil
}

func (f *fakeHostingStore) Save(_ context.Context, s staticexport.Settings) error {
	if f.saveErr != nil {
		return f.saveErr
	}
	if err := s.Validate(); err != nil { // the real store validates too
		return err
	}
	f.set, f.found = s, true
	f.saves++
	return nil
}

type fakeHostingRuns struct{ runs []staticexport.Run }

func (f *fakeHostingRuns) List(_ context.Context, limit int) ([]staticexport.Run, error) {
	if limit < len(f.runs) {
		return f.runs[:limit], nil
	}
	return f.runs, nil
}

type hostingFixture struct {
	h       *hostingAdmin
	store   *fakeHostingStore
	runs    *fakeHostingRuns
	refresh int
	site    string
	refuse  error
}

func newHostingFixture(t *testing.T) *hostingFixture {
	t.Helper()
	fx := &hostingFixture{store: &fakeHostingStore{}, runs: &fakeHostingRuns{}, site: "https://www.example.com"}
	fx.h = &hostingAdmin{
		settings: fx.store, runs: fx.runs, key: staticexport.SecretKey("test-secret"),
		refresh: func(context.Context) { fx.refresh++ },
		siteURL: func(context.Context) string { return fx.site },
		snapshot: func() statichost.StaticHosting {
			return statichost.StaticHosting{Static: true, Live: "https://live.example.com", Site: fx.site}
		},
		refuse: func(context.Context) error { return fx.refuse },
	}
	return fx
}

func dirInput(dir, mode string) admin.HostingInput {
	return admin.HostingInput{Mode: mode, Target: admin.HostingTargetDir, Dir: dir, LiveOrigin: "https://live.example.com"}
}

func s3Input(secret string) admin.HostingInput {
	return admin.HostingInput{Mode: admin.HostingServed, Target: admin.HostingTargetS3, Service: "minio", Endpoint: "https://s3.example.com",
		Region: "us-east-1", Bucket: "my-site", AccessKey: "AKIAEXAMPLE", SecretKey: secret}
}

func TestHostingAdmin_SecretSealedAndBlankKeeps(t *testing.T) {
	fx := newHostingFixture(t)
	const secret = "very-secret-value"
	if errs, err := fx.h.Save(context.Background(), s3Input(secret)); err != nil || len(errs) > 0 {
		t.Fatalf("save: %v %v", errs, err)
	}
	sealed := append([]byte(nil), fx.store.set.SecretSealed...)
	if len(sealed) == 0 || bytes.Contains(sealed, []byte(secret)) {
		t.Fatal("secret must be stored sealed")
	}
	if got, ok := fx.store.set.Secret(fx.h.key); !ok || got != secret {
		t.Fatalf("round trip = %q %v", got, ok)
	}
	in := s3Input("")
	in.Bucket = "other-bucket"
	if errs, err := fx.h.Save(context.Background(), in); err != nil || len(errs) > 0 {
		t.Fatalf("save blank: %v %v", errs, err)
	}
	if !bytes.Equal(fx.store.set.SecretSealed, sealed) || fx.store.set.Bucket != "other-bucket" {
		t.Fatal("blank secret must keep the sealed bytes and still save the rest")
	}
	v, err := fx.h.View(context.Background())
	if err != nil || !v.SecretSaved || v.SecretLost {
		t.Fatalf("view = %+v %v", v, err)
	}
	// a different server secret cannot read it
	fx.h.key = staticexport.SecretKey("another-secret")
	if v, _ = fx.h.View(context.Background()); v.SecretSaved || !v.SecretLost {
		t.Fatalf("lost view = %+v", v)
	}
}

func TestHostingAdmin_SaveKeepsModeAndRefreshesOnce(t *testing.T) {
	fx := newHostingFixture(t)
	if errs, err := fx.h.Save(context.Background(), dirInput(filepath.Join(t.TempDir(), "site"), admin.HostingServed)); err != nil || len(errs) > 0 {
		t.Fatalf("save: %v %v", errs, err)
	}
	if fx.store.set.Mode != staticexport.ModeServed || fx.refresh != 1 {
		t.Fatalf("mode %q refresh %d", fx.store.set.Mode, fx.refresh)
	}
	// even when the form says static, Save never switches the mode
	fx.store.set.Mode = staticexport.ModeServed
	if errs, err := fx.h.Save(context.Background(), dirInput(filepath.Join(t.TempDir(), "site"), admin.HostingStatic)); err != nil || len(errs) > 0 {
		t.Fatalf("save static: %v %v", errs, err)
	}
	if fx.store.set.Mode != staticexport.ModeServed || fx.refresh != 2 {
		t.Fatalf("mode %q refresh %d", fx.store.set.Mode, fx.refresh)
	}
	bad := dirInput("relative/dir", admin.HostingStatic)
	if errs, _ := fx.h.Save(context.Background(), bad); len(errs) == 0 || fx.refresh != 2 || fx.store.saves != 2 {
		t.Fatalf("field error must not save or refresh: %v refresh %d saves %d", errs, fx.refresh, fx.store.saves)
	}
}

func TestHostingAdmin_SaveFieldErrors(t *testing.T) {
	fx := newHostingFixture(t)
	foreign := t.TempDir()
	if err := os.WriteFile(filepath.Join(foreign, "notes.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	errs, err := fx.h.Save(context.Background(), dirInput(foreign, admin.HostingStatic))
	if err != nil || len(errs) != 1 || errs[0].ID != "hosting-dir" {
		t.Fatalf("non-export directory: %v %v", errs, err)
	}

	in := dirInput(filepath.Join(t.TempDir(), "site"), admin.HostingStatic)
	in.LiveOrigin = "https://www.example.com"
	if errs, _ = fx.h.Save(context.Background(), in); len(errs) != 1 || errs[0].ID != "hosting-live-origin" {
		t.Fatalf("live origin on the site host: %v", errs)
	}

	fx.site = ""
	in.LiveOrigin = "https://live.example.com"
	if errs, _ = fx.h.Save(context.Background(), in); len(errs) != 1 || errs[0].ID != "hosting-mode" {
		t.Fatalf("no site URL: %v", errs)
	}
	if fx.store.saves != 0 || fx.refresh != 0 {
		t.Fatalf("nothing saved: saves %d refresh %d", fx.store.saves, fx.refresh)
	}
}

func TestHostingAdmin_SetMode(t *testing.T) {
	fx := newHostingFixture(t)
	dir := filepath.Join(t.TempDir(), "site")
	if _, err := fx.h.Save(context.Background(), dirInput(dir, admin.HostingServed)); err != nil {
		t.Fatal(err)
	}
	fx.refresh = 0

	fx.refuse = errors.New("Turn off Site access before switching to static files.")
	if err := fx.h.SetMode(context.Background(), admin.HostingStatic); err == nil || fx.store.set.Mode != staticexport.ModeServed || fx.refresh != 0 {
		t.Fatalf("refused: err %v mode %q refresh %d", err, fx.store.set.Mode, fx.refresh)
	}

	fx.refuse = nil
	if err := fx.h.SetMode(context.Background(), admin.HostingStatic); err != nil {
		t.Fatal(err)
	}
	if fx.store.set.Mode != staticexport.ModeStatic || fx.refresh != 1 {
		t.Fatalf("mode %q refresh %d", fx.store.set.Mode, fx.refresh)
	}

	// an incomplete target never goes static
	fx.store.set = staticexport.Defaults()
	fx.store.found = true
	if err := fx.h.SetMode(context.Background(), admin.HostingStatic); err == nil || fx.store.set.Mode != staticexport.ModeServed {
		t.Fatalf("incomplete: %v", err)
	}

	fx.store.set.Mode = staticexport.ModeStatic
	fx.refresh = 0
	if err := fx.h.SetMode(context.Background(), admin.HostingServed); err != nil {
		t.Fatal(err)
	}
	if fx.store.set.Mode != staticexport.ModeServed || fx.refresh != 1 {
		t.Fatalf("served: mode %q refresh %d", fx.store.set.Mode, fx.refresh)
	}
}

type countingTimer struct{}

func (countingTimer) Stop() bool { return true }

func TestHostingAdmin_SetModeServedStopsTrigger(t *testing.T) {
	fx := newHostingFixture(t)
	fx.store.found = true
	fx.store.set = staticexport.Defaults()
	fx.store.set.Mode, fx.store.set.DirPath = staticexport.ModeStatic, filepath.Join(t.TempDir(), "site")
	armed := 0
	fx.h.trigger = statictrigger.New(func() context.Context { return context.Background() },
		func(context.Context, bool, string) error { return nil },
		func(context.Context) (bool, error) { return fx.store.set.Mode == staticexport.ModeStatic, nil },
		slog.New(slog.DiscardHandler))
	fx.h.trigger.SetClock(func(time.Duration, func()) statictrigger.Timer { armed++; return countingTimer{} }, time.Now)
	t.Cleanup(fx.h.trigger.Close)

	fx.h.trigger.Notify("change", false)
	if armed != 1 {
		t.Fatalf("static: armed %d timers, want 1", armed)
	}
	if err := fx.h.SetMode(context.Background(), admin.HostingServed); err != nil {
		t.Fatal(err)
	}
	fx.h.trigger.Notify("change", false)
	if armed != 1 {
		t.Fatalf("served: armed %d timers, want still 1", armed)
	}
}

func TestHostingAdmin_Export(t *testing.T) {
	fx := newHostingFixture(t)
	if err := fx.h.Export(context.Background(), false, "manual"); err == nil || !strings.Contains(err.Error(), "worker is off") {
		t.Fatalf("nil trigger: %v", err)
	}

	release := make(chan struct{})
	started := make(chan string, 4)
	fx.h.trigger = statictrigger.New(func() context.Context { return context.Background() },
		func(_ context.Context, full bool, reason string) error {
			started <- reason
			<-release
			return nil
		},
		func(context.Context) (bool, error) { return true, nil }, slog.New(slog.DiscardHandler))
	t.Cleanup(func() { fx.h.trigger.Close() })

	if err := fx.h.Export(context.Background(), false, "manual"); !errors.Is(err, admin.ErrHostingNotStatic) {
		t.Fatalf("served: %v", err)
	}
	fx.store.found, fx.store.set = true, staticexport.Defaults()
	fx.store.set.Mode = staticexport.ModeStatic
	if err := fx.h.Export(context.Background(), false, "manual"); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-started:
		if got != "manual" {
			t.Fatalf("reason %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run did not start")
	}
	if err := fx.h.Export(context.Background(), true, "rebuild"); !errors.Is(err, admin.ErrHostingExportQueued) {
		t.Fatalf("while running: %v", err)
	}
	close(release)
}

func TestHostingAdmin_RunsAndLastRun(t *testing.T) {
	fx := newHostingFixture(t)
	now := time.Now()
	fx.runs.runs = []staticexport.Run{
		{Status: "running", Trigger: "change", StartedAt: now},
		{Status: "failed", Trigger: "manual", StartedAt: now.Add(-time.Hour), Errors: []string{"render 500 on /x", "second"}, Pages: 3},
		{Status: "ok", Trigger: "change", StartedAt: now.Add(-2 * time.Hour), Pages: 9, Changed: 2, Removed: 1, Duration: time.Second},
	}
	last, ok, err := fx.h.LastRun(context.Background())
	if err != nil || !ok || last.Result != admin.HostingRunFailed || last.Errors != 2 || last.FirstError != "render 500 on /x" {
		t.Fatalf("last = %+v %v %v", last, ok, err)
	}
	runs, err := fx.h.Runs(context.Background(), 10)
	if err != nil || len(runs) != 3 || runs[0].Result != admin.HostingRunRunning || runs[2].Pages != 9 || runs[2].FirstError != "" {
		t.Fatalf("runs = %+v %v", runs, err)
	}
	fx.runs.runs = fx.runs.runs[:1]
	if _, ok, _ := fx.h.LastRun(context.Background()); ok {
		t.Fatal("a running row is not a last run")
	}
}

func TestHostingAdmin_Islands(t *testing.T) {
	fx := newHostingFixture(t)
	live, stays := fx.h.Islands()
	wantLive, wantStays := 0, map[string]bool{}
	for _, it := range statichost.Inventory() {
		if it.Where == statichost.Live {
			wantLive++
		} else {
			wantStays[it.Name] = true
		}
	}
	if len(live) != wantLive || len(stays) != len(wantStays) {
		t.Fatalf("live %d/%d stays %d/%d", len(live), wantLive, len(stays), len(wantStays))
	}
	for _, n := range stays {
		if !wantStays[n] {
			t.Fatalf("unexpected stays row %q", n)
		}
	}
	for _, l := range live {
		if l.Route == "" || l.Status != "live" {
			t.Fatalf("live row = %+v", l)
		}
	}
}

func TestHostingAdmin_ViewIslandsWarning(t *testing.T) {
	fx := newHostingFixture(t)
	fx.store.found, fx.store.set = true, staticexport.Defaults()
	fx.store.set.Mode = staticexport.ModeStatic
	fx.h.snapshot = func() statichost.StaticHosting { return statichost.StaticHosting{Static: true, Site: fx.site} }
	v, err := fx.h.View(context.Background())
	// The page wraps this in "islands are off: %s.", so it must be the bare reason.
	if err != nil || v.IslandsWarning != fx.h.snapshot().InactiveReason() || strings.Contains(v.IslandsWarning, "islands are off") || strings.HasSuffix(v.IslandsWarning, ".") {
		t.Fatalf("view = %+v %v", v, err)
	}
	fx.store.set.Mode = staticexport.ModeServed
	if v, _ = fx.h.View(context.Background()); v.IslandsWarning != "" {
		t.Fatalf("served view warns: %q", v.IslandsWarning)
	}
}

func TestHostingAdmin_NoSecretInErrors(t *testing.T) {
	fx := newHostingFixture(t)
	const secret = "very-secret-value"
	in := s3Input(secret)
	in.Bucket = "x" // too short: a validation error
	errs, err := fx.h.Save(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range errs {
		if strings.Contains(e.Message, secret) || strings.Contains(e.Label, secret) {
			t.Fatalf("field error leaks the secret: %+v", e)
		}
	}
	fx.store.saveErr = errors.New("db down")
	_, err = fx.h.Save(context.Background(), s3Input(secret))
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("err = %v", err)
	}
}

// Saving while static, asking for served, with the directory cleared: the
// store validates with the stored (static) mode, so this must come back as a
// field error and not as a failed save.
func TestHostingAdmin_SaveServedRequestWithInvalidStaticTargetIsFieldError(t *testing.T) {
	fx := newHostingFixture(t)
	fx.store.found, fx.store.set = true, staticexport.Defaults()
	fx.store.set.Mode, fx.store.set.Target, fx.store.set.DirPath = staticexport.ModeStatic, staticexport.TargetDir, "/srv/site"
	in := dirInput("", admin.HostingServed)
	errs, err := fx.h.Save(context.Background(), in)
	if err != nil {
		t.Fatalf("save returned an error (a 500 page): %v", err)
	}
	if len(errs) == 0 || fx.store.saves != 0 {
		t.Fatalf("errs=%v saves=%d, want a field error and nothing saved", errs, fx.store.saves)
	}
}

type testExportCall struct {
	target staticexport.Target
	hs     statichost.StaticHosting
	opts   staticexport.Options
}

func testExportFixture(t *testing.T, calls *[]testExportCall) (*hostingFixture, string) {
	t.Helper()
	fx := newHostingFixture(t)
	dir := filepath.Join(t.TempDir(), "public")
	fx.store.set = staticexport.Settings{Mode: staticexport.ModeServed, Target: staticexport.TargetDir, DirPath: dir, LiveOrigin: "https://live.example.com"}
	fx.store.found = true
	fx.h.spawn = func(f func()) { f() }
	fx.h.background = context.Background
	fx.h.log = slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	fx.h.testExport = func(_ context.Context, tg staticexport.Target, hs statichost.StaticHosting, o staticexport.Options) (staticexport.Result, error) {
		*calls = append(*calls, testExportCall{tg, hs, o})
		return staticexport.Result{}, nil
	}
	return fx, dir
}

func TestHostingAdmin_TestExportServedDir(t *testing.T) {
	var calls []testExportCall
	fx, dir := testExportFixture(t, &calls)
	if err := fx.h.TestExport(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 {
		t.Fatalf("calls = %d", len(calls))
	}
	c := calls[0]
	if c.opts.Trigger != admin.HostingTriggerTest || !c.hs.Static || c.hs.Live != "https://live.example.com" || c.hs.Site != fx.site {
		t.Errorf("call = %+v", c)
	}
	if !strings.Contains(c.target.Key(), dir) {
		t.Errorf("target key %q does not refer to %q", c.target.Key(), dir)
	}
	if fx.refresh != 0 || fx.store.saves != 0 {
		t.Errorf("test export refreshed or saved (refresh=%d saves=%d)", fx.refresh, fx.store.saves)
	}
	if fx.h.testing.Load() {
		t.Error("single-flight flag not released")
	}
}

func TestHostingAdmin_TestExportRefusals(t *testing.T) {
	var calls []testExportCall
	fx, _ := testExportFixture(t, &calls)
	ctx := context.Background()

	fx.runs.runs = []staticexport.Run{{Status: admin.HostingRunRunning}}
	if err := fx.h.TestExport(ctx); !errors.Is(err, admin.ErrHostingTestBusy) {
		t.Errorf("running run: %v", err)
	}
	fx.runs.runs = nil
	fx.h.testing.Store(true)
	if err := fx.h.TestExport(ctx); !errors.Is(err, admin.ErrHostingTestBusy) {
		t.Errorf("single-flight: %v", err)
	}
	fx.h.testing.Store(false)
	fx.refuse = errors.New("demo")
	if err := fx.h.TestExport(ctx); err == nil || err.Error() != "demo" {
		t.Errorf("refuse: %v", err)
	}
	fx.refuse = nil
	fx.store.set.DirPath = ""
	if err := fx.h.TestExport(ctx); !errors.Is(err, admin.ErrHostingNoTarget) {
		t.Errorf("empty dir: %v", err)
	}
	fx.h.testExport = nil
	if err := fx.h.TestExport(ctx); err == nil {
		t.Error("nil testExport must error")
	}
	if len(calls) != 0 {
		t.Errorf("export ran %d times", len(calls))
	}
}

func TestHostingAdmin_LastRunSkipsTestRuns(t *testing.T) {
	fx := newHostingFixture(t)
	fx.runs.runs = []staticexport.Run{
		{Trigger: admin.HostingTriggerTest, Status: admin.HostingRunFailed},
		{Trigger: "manual", Status: "ok", Pages: 7},
	}
	r, ok, err := fx.h.LastRun(context.Background())
	if err != nil || !ok || r.Trigger != "manual" || r.Pages != 7 {
		t.Fatalf("last = %+v %v %v", r, ok, err)
	}
	fx.runs.runs = fx.runs.runs[:1]
	if _, ok, _ := fx.h.LastRun(context.Background()); ok {
		t.Error("only a test run: LastRun must be empty")
	}
}

func TestPreviousLiveFailedIgnoresTestRuns(t *testing.T) {
	cur := uuid.New()
	list := []staticexport.Run{
		{ID: cur, Status: "ok"},
		{Trigger: admin.HostingTriggerTest, Status: "failed"},
		{Trigger: "manual", Status: "ok"},
	}
	if previousLiveFailed(list, cur) {
		t.Error("a failed test run counted as the previous live failure")
	}
	list[2].Status = "failed"
	if !previousLiveFailed(list, cur) {
		t.Error("previous live failure missed")
	}
}
