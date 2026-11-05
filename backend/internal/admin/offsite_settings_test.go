package admin

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/admin/a11y"
	"hatamoto.dev/core/internal/admin/ui"
)

const testSecretKey = "s3cr3t-key-value-0123456789" // gitleaks:allow

// fakeOffsite is an OffsiteService with a fixed view and call records.
type fakeOffsite struct {
	view      OffsiteView
	viewErr   error
	saveErrs  []ui.FieldError
	saved     []OffsiteInput
	tested    []OffsiteInput
	testErr   error
	runs      []string
	runErr    error
	runID     string
	saveFatal error
}

func (f *fakeOffsite) View(context.Context) (OffsiteView, error) { return f.view, f.viewErr }
func (f *fakeOffsite) Save(_ context.Context, in OffsiteInput) ([]ui.FieldError, error) {
	if f.saveFatal != nil || len(f.saveErrs) > 0 {
		return f.saveErrs, f.saveFatal
	}
	f.saved = append(f.saved, in)
	return nil, nil
}
func (f *fakeOffsite) Test(_ context.Context, in OffsiteInput) error {
	f.tested = append(f.tested, in)
	return f.testErr
}
func (f *fakeOffsite) RunNow(_ context.Context, actor string) (string, error) {
	f.runs = append(f.runs, actor)
	return f.runID, f.runErr
}

func defaultOffsiteView() OffsiteView {
	return OffsiteView{Service: OffsiteAWS, Schedule: OffsiteOff, Hour: 3, Keep: 7}
}

func enabledOffsiteView() OffsiteView {
	return OffsiteView{
		Enabled: true, Service: OffsiteCustom, Endpoint: "https://s3.example.com", Region: "eu-1", Bucket: "site-backups",
		Prefix: "prod/", AccessKey: "AKIAEXAMPLE", SecretSaved: true, Schedule: OffsiteDaily, Hour: 3, Keep: 7,
		NextRun: time.Date(2026, 10, 20, 3, 0, 0, 0, time.UTC),
	}
}

func newOffsiteRig(t *testing.T, o *fakeOffsite) *backupsRig {
	t.Helper()
	b := newBackupsRig(t, baseBackups())
	b.a.deps.Offsite = o
	return b
}

func offsiteForm(extra url.Values) url.Values {
	f := url.Values{
		"enabled": {"true"}, "service": {"custom"}, "endpoint": {"https://s3.example.com"}, "region": {"eu-1"},
		"bucket": {"site-backups"}, "prefix": {"prod/"}, "access_key": {"AKIAEXAMPLE"}, "secret_key": {testSecretKey},
		"schedule": {"daily"}, "hour": {"3"}, "weekday": {"0"}, "keep": {"7"},
	}
	for k, v := range extra {
		f[k] = v
	}
	return f
}

func TestOffsiteCardStates(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 10, 19, 3, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		name    string
		view    OffsiteView
		wants   []string
		absents []string
	}{
		{"disabled", defaultOffsiteView(), []string{
			"Off-site copy", "Backups are not copied off this server.", ">Off<", "Test connection", `autocomplete="off"`,
			`type="password"`, "private bucket with server-side encryption", `action="/admin/settings/backups/offsite"`,
		}, []string{"Back up and upload now", "Saved. Leave blank"}},
		{"enabled ok", func() OffsiteView {
			v := enabledOffsiteView()
			v.LastAt, v.LastOK, v.LastSize = at, true, 30<<20
			return v
		}(), []string{"Last upload", "30 MB", "checked in the bucket", ">OK<", "Next run:", "Back up and upload now", "Saved. Leave blank to keep the saved key.", `placeholder="Saved"`, `value="site-backups"`}, nil},
		{"last failed", func() OffsiteView {
			v := enabledOffsiteView()
			v.LastAt, v.LastError = at, "the bucket refused the upload (AccessDenied)"
			return v
		}(), []string{">Failed<", "(AccessDenied)"}, []string{">OK<"}},
		{"secret lost", func() OffsiteView { v := enabledOffsiteView(); v.SecretSaved, v.SecretLost = false, true; return v }(),
			[]string{"can no longer be read"}, []string{"Back up and upload now"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			g := newOffsiteRig(t, &fakeOffsite{view: c.view})
			body := g.get(t)
			for _, w := range c.wants {
				if !strings.Contains(body, w) {
					t.Errorf("page lacks %q", w)
				}
			}
			for _, x := range c.absents {
				if strings.Contains(body, x) {
					t.Errorf("page has %q", x)
				}
			}
			compareGolden(t, "pages/settings-backups-offsite-"+strings.ReplaceAll(c.name, " ", "_")+".html", normalizePage(body))
		})
	}
}

func TestOffsiteCardHiddenWithoutService(t *testing.T) {
	t.Parallel()
	body := newBackupsRig(t, baseBackups()).get(t)
	if strings.Contains(body, "Off-site copy") {
		t.Error("the card shows without a service")
	}
}

func TestOffsiteSaveNeverEchoesOrAuditsTheSecretKey(t *testing.T) {
	t.Parallel()

	f := &fakeOffsite{view: defaultOffsiteView()}
	g := newOffsiteRig(t, f)
	rec := g.do("POST", backupsPath+"/offsite", "owner-token", offsiteForm(nil), nil)
	if rec.Code != 303 || !strings.HasPrefix(rec.Header().Get("Location"), backupsPath) {
		t.Fatalf("save = %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if len(f.saved) != 1 || f.saved[0].SecretKey != testSecretKey || !f.saved[0].Enabled || f.saved[0].Keep != 7 || f.saved[0].Schedule != OffsiteDaily {
		t.Fatalf("saved = %+v", f.saved)
	}
	if len(g.events) != 1 || g.events[0].Action != "backups.offsite.update" {
		t.Fatalf("audit = %+v", g.events)
	}
	d := g.events[0].Detail
	if d["secret_changed"] != true {
		t.Errorf("detail = %v", d)
	}
	for _, e := range g.events {
		for k, v := range e.Detail {
			if strings.Contains(k, testSecretKey) || strings.Contains(strings.TrimSpace(toText(v)), testSecretKey) {
				t.Errorf("audit leaks the key: %v", e.Detail)
			}
		}
	}
	if body := followFlashTo(g.ownerRig, backupsPath, rec); !strings.Contains(body, "Off-site settings saved.") || strings.Contains(body, testSecretKey) {
		t.Error("flash missing or page leaks the key")
	}
}

func toText(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []string:
		return strings.Join(x, ",")
	}
	return ""
}

func TestOffsiteSaveRefusedShowsFieldErrorsWithoutTheKey(t *testing.T) {
	t.Parallel()

	f := &fakeOffsite{view: defaultOffsiteView(), saveErrs: []ui.FieldError{{ID: "offsite-bucket", Label: "Bucket", Message: "Enter a bucket name."}}}
	g := newOffsiteRig(t, f)
	rec := g.do("POST", backupsPath+"/offsite", "owner-token", offsiteForm(url.Values{"bucket": {"x"}}), nil)
	body := rec.Body.String()
	if rec.Code != 422 || !strings.Contains(body, "Enter a bucket name.") || !strings.Contains(body, `href="#offsite-bucket"`) {
		t.Fatalf("code %d, body lacks the error", rec.Code)
	}
	if strings.Contains(body, testSecretKey) {
		t.Fatal("the refused page echoes the secret key")
	}
	if !strings.Contains(body, `value="x"`) {
		t.Error("the posted bucket is not shown again")
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
	if len(g.events) != 0 {
		t.Errorf("a refused save was audited: %+v", g.events)
	}
}

func TestOffsiteSaveRejectsBadNumbers(t *testing.T) {
	t.Parallel()

	f := &fakeOffsite{view: defaultOffsiteView()}
	g := newOffsiteRig(t, f)
	rec := g.do("POST", backupsPath+"/offsite", "owner-token", offsiteForm(url.Values{"keep": {"0"}, "hour": {"24"}}), nil)
	body := rec.Body.String()
	if rec.Code != 422 || !strings.Contains(body, "Copies to keep") || !strings.Contains(body, "Hour") || len(f.saved) != 0 {
		t.Fatalf("code %d, saved %v", rec.Code, f.saved)
	}
}

func TestOffsiteTest(t *testing.T) {
	t.Parallel()

	f := &fakeOffsite{view: defaultOffsiteView()}
	g := newOffsiteRig(t, f)
	rec := g.do("POST", backupsPath+"/offsite/test", "owner-token", offsiteForm(nil), nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "The bucket answered") || len(f.tested) != 1 || len(f.saved) != 0 {
		t.Fatalf("test ok = %d, tested %d, saved %d", rec.Code, len(f.tested), len(f.saved))
	}
	f.testErr = errors.New("the bucket refused the keys (SignatureDoesNotMatch)")
	rec = g.do("POST", backupsPath+"/offsite/test", "owner-token", offsiteForm(nil), nil)
	body := rec.Body.String()
	if rec.Code != 422 || !strings.Contains(body, "SignatureDoesNotMatch") || !strings.Contains(body, "Nothing was saved.") || strings.Contains(body, testSecretKey) {
		t.Fatalf("test failed = %d", rec.Code)
	}
	if len(g.events) != 0 {
		t.Errorf("a connection test was audited: %+v", g.events)
	}
}

func TestOffsiteRun(t *testing.T) {
	t.Parallel()

	f := &fakeOffsite{view: enabledOffsiteView(), runID: "20261019T091200Z-manual"}
	g := newOffsiteRig(t, f)
	rec := g.do("POST", backupsPath+"/offsite/run", "owner-token", url.Values{}, nil)
	if rec.Code != 303 || len(f.runs) != 1 || f.runs[0] == "" {
		t.Fatalf("run = %d, %v", rec.Code, f.runs)
	}
	if len(g.events) != 1 || g.events[0].Action != "backups.offsite.run" || g.events[0].Target != f.runID {
		t.Fatalf("audit = %+v", g.events)
	}
	f.runErr = ErrBackupRunning
	rec = g.do("POST", backupsPath+"/offsite/run", "owner-token", url.Values{}, nil)
	if body := followFlashTo(g.ownerRig, backupsPath, rec); !strings.Contains(body, "A backup is already running.") {
		t.Error("no already-running flash")
	}
	if len(g.events) != 1 {
		t.Errorf("a refused run was audited: %+v", g.events)
	}
}

func TestOffsitePostsAreOwnerOnlyAndNeedCSRF(t *testing.T) {
	t.Parallel()

	f := &fakeOffsite{view: enabledOffsiteView()}
	g := newOffsiteRig(t, f)
	for _, target := range []string{backupsPath + "/offsite", backupsPath + "/offsite/test", backupsPath + "/offsite/run"} {
		if rec := g.do("POST", target, "editor-token", offsiteForm(nil), nil); rec.Code != 403 {
			t.Errorf("editor POST %s = %d, want 403", target, rec.Code)
		}
		if rec := g.do("POST", target, "owner-token", offsiteForm(nil), map[string]string{"X-CSRF-Token": ""}); rec.Code != 403 {
			t.Errorf("no CSRF POST %s = %d, want 403", target, rec.Code)
		}
		if rec := g.do("POST", target, "", offsiteForm(nil), nil); rec.Code == 303 && !strings.Contains(rec.Header().Get("Location"), "login") {
			t.Errorf("anonymous POST %s = %d %s", target, rec.Code, rec.Header().Get("Location"))
		}
	}
	if len(f.saved)+len(f.tested)+len(f.runs) != 0 || len(g.events) != 0 {
		t.Errorf("a refused request reached the service: %v %v %v, audit %v", f.saved, f.tested, f.runs, g.events)
	}
}

func TestOffsiteUnavailableWithoutService(t *testing.T) {
	t.Parallel()
	g := newBackupsRig(t, baseBackups())
	if rec := g.do("POST", backupsPath+"/offsite", "owner-token", offsiteForm(nil), nil); rec.Code != 503 {
		t.Errorf("save without a service = %d", rec.Code)
	}
}

func TestOffsiteCheckGrades(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	const day = 24 * time.Hour
	for _, tc := range []struct {
		name  string
		v     OffsiteView
		level string // "" means no row
	}{
		{"not enabled", OffsiteView{}, ""},
		{"never uploaded", OffsiteView{Enabled: true, Schedule: OffsiteDaily}, healthWarn},
		{"daily fresh", OffsiteView{Enabled: true, Schedule: OffsiteDaily, LastOK: true, LastAt: now.Add(-20 * time.Hour)}, healthOK},
		{"daily at 36h", OffsiteView{Enabled: true, Schedule: OffsiteDaily, LastOK: true, LastAt: now.Add(-36 * time.Hour)}, healthOK},
		{"daily late", OffsiteView{Enabled: true, Schedule: OffsiteDaily, LastOK: true, LastAt: now.Add(-36*time.Hour - time.Minute)}, healthWarn},
		{"weekly at 10d", OffsiteView{Enabled: true, Schedule: OffsiteWeekly, LastOK: true, LastAt: now.Add(-10 * day)}, healthOK},
		{"weekly late", OffsiteView{Enabled: true, Schedule: OffsiteWeekly, LastOK: true, LastAt: now.Add(-10*day - time.Minute)}, healthWarn},
		{"failed", OffsiteView{Enabled: true, Schedule: OffsiteDaily, LastOK: false, LastAt: now.Add(-time.Hour)}, healthBad},
		{"manual only is never late", OffsiteView{Enabled: true, Schedule: OffsiteOff, LastOK: true, LastAt: now.Add(-90 * day)}, healthOK},
	} {
		got := offsiteCheck(&healthCheck{}, tc.v, now)
		switch {
		case tc.level == "" && got != nil:
			t.Errorf("%s: got a row %+v", tc.name, got)
		case tc.level != "" && (got == nil || got.Level != tc.level):
			t.Errorf("%s: got %+v, want level %s", tc.name, got, tc.level)
		}
	}
}

func TestDashboardHealthOffsiteRow(t *testing.T) {
	t.Parallel()

	v := enabledOffsiteView()
	v.LastAt, v.LastOK = time.Now().Add(-time.Hour), true
	fb := &fakeBackups{state: BackupsState{Rows: []BackupInfo{{State: BackupOK, Created: time.Now().Add(-time.Hour)}}}}
	g := newRig(t, func(d *Deps) { d.Backups = fb; d.Offsite = &fakeOffsite{view: v} })
	items, _ := healthOf(t, g)
	it, ok := items["Off-site backup"]
	if !ok || it.Badge.Kind != "success" || it.Href != "" {
		t.Fatalf("row = %+v, %v", it, ok)
	}
	v.LastOK = false
	g = newRig(t, func(d *Deps) { d.Backups = fb; d.Offsite = &fakeOffsite{view: v} })
	items, _ = healthOf(t, g)
	if it := items["Off-site backup"]; it.Badge.Kind != "danger" || it.Badge.Label != "Failed" || it.Href != "/admin/settings/backups#offsite" {
		t.Fatalf("failed row = %+v", it)
	}
	g = newRig(t, func(d *Deps) { d.Backups = fb })
	if items, _ = healthOf(t, g); items["Off-site backup"].Label != "" {
		t.Error("a row shows without a service")
	}
	g = newRig(t, func(d *Deps) { d.Backups = fb; d.Offsite = &fakeOffsite{view: defaultOffsiteView()} })
	if items, _ = healthOf(t, g); items["Off-site backup"].Label != "" {
		t.Error("a row shows while off-site is not enabled")
	}
}

func TestBackupsTableShowsTheOffsiteColumn(t *testing.T) {
	t.Parallel()

	f := baseBackups()
	f.state.Rows = []BackupInfo{
		{ID: "20261018T141900Z-scheduled", Kind: BackupScheduled, State: BackupOK, Size: 1 << 20, Offsite: OffsiteUploaded, Created: time.Date(2026, 10, 18, 3, 0, 0, 0, time.UTC)},
		{ID: "20261017T030000Z-scheduled", Kind: BackupScheduled, State: BackupOK, Size: 1 << 20, Offsite: OffsiteFailed, OffsiteError: "AccessDenied", Created: time.Date(2026, 10, 17, 3, 0, 0, 0, time.UTC)},
	}
	body := newBackupsRig(t, f).get(t)
	for _, w := range []string{">Off-site<", ">Uploaded<", "Scheduled", "Off-site copy failed: AccessDenied"} {
		if !strings.Contains(body, w) {
			t.Errorf("page lacks %q", w)
		}
	}
}
