package admin

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/admin/a11y"
	"hatamoto.dev/core/internal/admin/ui"
)

const testHostingSecret = "hosting-s3cr3t-0123456789" // gitleaks:allow

type hostingExportCall struct {
	full    bool
	trigger string
}

// fakeHosting is a HostingService with a fixed view and call records.
type fakeHosting struct {
	view      HostingView
	viewErr   error
	saveErrs  []ui.FieldError
	saveFatal error
	saved     []HostingInput
	modes     []string
	modeErr   error
	exports   []hostingExportCall
	exportErr error
	tests     int
	testErr   error
	runs      []HostingRun
	live      []HostingIsland
	stays     []string
}

func (f *fakeHosting) View(context.Context) (HostingView, error) { return f.view, f.viewErr }
func (f *fakeHosting) Save(_ context.Context, in HostingInput) ([]ui.FieldError, error) {
	if f.saveFatal != nil || len(f.saveErrs) > 0 {
		return f.saveErrs, f.saveFatal
	}
	f.saved = append(f.saved, in)
	return nil, nil
}
func (f *fakeHosting) SetMode(_ context.Context, mode string) error {
	if f.modeErr != nil {
		return f.modeErr
	}
	f.modes = append(f.modes, mode)
	return nil
}
func (f *fakeHosting) Export(_ context.Context, full bool, trigger string) error {
	if f.exportErr != nil {
		return f.exportErr
	}
	f.exports = append(f.exports, hostingExportCall{full, trigger})
	return nil
}
func (f *fakeHosting) TestExport(context.Context) error {
	if f.testErr != nil {
		return f.testErr
	}
	f.tests++
	return nil
}
func (f *fakeHosting) Runs(context.Context, int) ([]HostingRun, error) { return f.runs, nil }
func (f *fakeHosting) LastRun(context.Context) (HostingRun, bool, error) {
	for _, r := range f.runs {
		if r.Result != HostingRunRunning && r.Trigger != HostingTriggerTest {
			return r, true, nil
		}
	}
	return HostingRun{}, false, nil
}
func (f *fakeHosting) Islands() ([]HostingIsland, []string) { return f.live, f.stays }

func servedHosting() *fakeHosting {
	return &fakeHosting{
		view:  HostingView{Mode: HostingServed, Target: HostingTargetDir, Service: OffsiteAWS},
		live:  []HostingIsland{{Label: "Forms", Route: "/forms/*", Status: "live"}, {Label: "Search", Route: "/search", Status: "live"}},
		stays: []string{"admin", "API"},
	}
}

func staticHosting() *fakeHosting {
	f := servedHosting()
	f.view = HostingView{
		Mode: HostingStatic, Target: HostingTargetS3, Service: OffsiteR2, Endpoint: "https://acct.r2.cloudflarestorage.com", Bucket: "site",
		AccessKey: "AKIAEXAMPLE", LiveOrigin: "https://live.example.com", SecretSaved: true,
	}
	at := time.Now().Add(-2 * time.Minute)
	f.runs = []HostingRun{
		{Started: at, Trigger: "manual", Pages: 214, Bytes: 38 << 20, Changed: 5, Duration: 9 * time.Second, Result: HostingRunOK},
		{Started: at.Add(-time.Hour), Trigger: "change", Pages: 0, Errors: 1, FirstError: "upload refused", Result: HostingRunFailed},
	}
	return f
}

func newHostingRig(t *testing.T, f *fakeHosting) *perfRig {
	t.Helper()
	p := newPerfRig(t, nil)
	if f != nil {
		p.a.deps.Hosting = f
	}
	return p
}

const hostingURL = "/admin/settings/hosting"

func TestHostingPageServed(t *testing.T) {
	t.Parallel()

	p := newHostingRig(t, servedHosting())
	rec := p.do("GET", hostingURL, "owner-token", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("GET = %d", rec.Code)
	}
	body := rec.Body.String()
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
	for _, want := range []string{"Served by Hatamoto", "Static files &#43; live islands", "Forms", "/search", "Stays on Hatamoto: admin, API.", "No exports yet.", `id="hosting-form"`, `value="served" checked`} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	for _, not := range []string{"Export now", "Rebuild all", `id="export-form"`, `id="hosting-crawlers"`, `id="hosting-islands-inactive"`} {
		if strings.Contains(body, not) {
			t.Errorf("served page has %q", not)
		}
	}
}

func TestHostingPageStatic(t *testing.T) {
	t.Parallel()

	f := staticHosting()
	p := newHostingRig(t, f)
	body := p.do("GET", hostingURL, "owner-token", nil, nil).Body.String()
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
	for _, want := range []string{"Export now", "Rebuild all", `id="export-form"`, `id="rebuild-form"`, `id="hosting-crawlers"`, "214 pages", "2 minutes ago", "https://live.example.com", "upload refused"} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if !strings.Contains(body, "Failed") || !strings.Contains(body, "hm-badge--danger") {
		t.Error("the failed run has no danger badge")
	}
	if strings.Contains(body, `id="hosting-islands-inactive"`) {
		t.Error("islands warning without a reason")
	}
	if strings.Contains(body, testHostingSecret) {
		t.Error("the page leaks the secret")
	}

	f.view.IslandsWarning = "the live origin is the site address"
	body = p.do("GET", hostingURL, "owner-token", nil, nil).Body.String()
	if !strings.Contains(body, `id="hosting-islands-inactive"`) || !strings.Contains(body, "the live origin is the site address") {
		t.Error("no islands warning in static mode")
	}
	f.view.Mode = HostingServed
	if body = p.do("GET", hostingURL, "owner-token", nil, nil).Body.String(); strings.Contains(body, `id="hosting-islands-inactive"`) {
		t.Error("islands warning in served mode")
	}

	f.view.Mode = HostingStatic
	p.a.deps.AgentLogIngest = true
	if body = p.do("GET", hostingURL, "owner-token", nil, nil).Body.String(); strings.Contains(body, `id="hosting-crawlers"`) {
		t.Error("crawlers warning with ingest configured")
	}
}

func TestHostingPageOwnerOnlyAndUnavailable(t *testing.T) {
	t.Parallel()

	f := staticHosting()
	p := newHostingRig(t, f)
	if rec := p.do("GET", hostingURL, "editor-token", nil, nil); rec.Code != 403 {
		t.Errorf("editor GET = %d, want 403", rec.Code)
	}
	if rec := p.do("GET", hostingURL, "", nil, nil); rec.Code != 303 {
		t.Errorf("anonymous GET = %d, want redirect", rec.Code)
	}
	if rec := newHostingRig(t, nil).do("GET", hostingURL, "owner-token", nil, nil); rec.Code != 503 {
		t.Errorf("no service GET = %d, want 503", rec.Code)
	}
}

func hostingForm(extra url.Values) url.Values {
	f := url.Values{
		"mode": {"static"}, "target": {"s3"}, "dir": {""}, "service": {"r2"}, "endpoint": {"https://acct.r2.cloudflarestorage.com"},
		"bucket": {"site"}, "prefix": {"out/"}, "region": {"auto"}, "access_key": {"AKIAEXAMPLE"}, "secret_key": {testHostingSecret},
		"live_origin": {"https://live.example.com"},
	}
	for k, v := range extra {
		f[k] = v
	}
	return f
}

func newHostingSaveRig(t *testing.T, f *fakeHosting) *perfRig {
	t.Helper()
	p := newHostingRig(t, f)
	p.a.deps.BaseURL = func(*http.Request) string { return "https://example.com" }
	return p
}

func TestHostingSaveValidation(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		extra url.Values
		want  string
	}{
		{"bad mode", url.Values{"mode": {"edge"}}, "Choose how pages are served."},
		{"bad target", url.Values{"target": {"ftp"}}, "Choose a local directory or S3-compatible storage."},
		{"bad service", url.Values{"service": {"gcs"}}, "Choose one of the listed services."},
		{"relative dir", url.Values{"target": {"dir"}, "dir": {"site/out"}}, "Enter an absolute path"},
		{"no bucket", url.Values{"bucket": {""}}, "Enter a bucket name."},
		{"no endpoint", url.Values{"endpoint": {""}}, "Enter the endpoint of the storage service."},
		{"no live origin", url.Values{"live_origin": {""}}, "Enter the address where the live islands answer."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := servedHosting()
			p := newHostingSaveRig(t, f)
			rec := p.do("POST", hostingURL, "owner-token", hostingForm(tc.extra), nil)
			body := rec.Body.String()
			if rec.Code != 422 || !strings.Contains(body, tc.want) {
				t.Fatalf("code %d, body lacks %q", rec.Code, tc.want)
			}
			if len(f.saved) != 0 || len(p.events) != 0 {
				t.Errorf("saved %v, audit %v", f.saved, p.events)
			}
			if strings.Contains(body, testHostingSecret) {
				t.Error("the refused page echoes the secret key")
			}
			if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
				t.Errorf("a11y: %v", issues)
			}
		})
	}
}

func TestHostingSaveNeedsSiteAddress(t *testing.T) {
	t.Parallel()

	f := servedHosting()
	p := newHostingRig(t, f)
	p.a.deps.BaseURL = func(*http.Request) string { return "" }
	rec := p.do("POST", hostingURL, "owner-token", hostingForm(nil), nil)
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), "Set the site address in Site settings first.") || len(f.saved) != 0 {
		t.Fatalf("code %d, saved %v", rec.Code, f.saved)
	}
}

func TestHostingSaveNeverEchoesOrAuditsTheSecretKey(t *testing.T) {
	t.Parallel()

	f := staticHosting()
	p := newHostingSaveRig(t, f)
	rec := p.do("POST", hostingURL, "owner-token", hostingForm(nil), nil)
	if rec.Code != 303 || rec.Header().Get("Location") != hostingURL {
		t.Fatalf("save = %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if len(f.saved) != 1 || f.saved[0].SecretKey != testHostingSecret || f.saved[0].Mode != HostingStatic || f.saved[0].Prefix != "out/" {
		t.Fatalf("saved = %+v", f.saved)
	}
	if len(f.modes) != 0 {
		t.Errorf("Save set the mode: %v", f.modes)
	}
	if len(p.events) != 1 || p.events[0].Action != "hosting.update" || p.events[0].Detail["secret_changed"] != true {
		t.Fatalf("audit = %+v", p.events)
	}
	for k, v := range p.events[0].Detail {
		if strings.Contains(k, testHostingSecret) || strings.Contains(toText(v), testHostingSecret) {
			t.Errorf("audit leaks the key: %v", p.events[0].Detail)
		}
	}
	if body := followFlashTo(p.ownerRig, hostingURL, rec); !strings.Contains(body, "Hosting settings saved.") || strings.Contains(body, testHostingSecret) {
		t.Error("flash missing or page leaks the key")
	}
}

func TestHostingSaveRefusedByServiceDropsTheSecret(t *testing.T) {
	t.Parallel()

	f := staticHosting()
	f.saveErrs = []ui.FieldError{{ID: "hosting-live-origin", Label: "Live origin", Message: "Use a different address from the site."}}
	p := newHostingSaveRig(t, f)
	rec := p.do("POST", hostingURL, "owner-token", hostingForm(url.Values{"bucket": {"posted-bucket"}}), nil)
	body := rec.Body.String()
	if rec.Code != 422 || !strings.Contains(body, "Use a different address from the site.") || !strings.Contains(body, `href="#hosting-live-origin"`) {
		t.Fatalf("code %d, body lacks the error", rec.Code)
	}
	if strings.Contains(body, testHostingSecret) {
		t.Fatal("the refused page echoes the secret key")
	}
	if !strings.Contains(body, `value="posted-bucket"`) {
		t.Error("the posted bucket is not shown again")
	}
	if !strings.Contains(body, "Leave blank to keep the saved key.") {
		t.Error("the saved-secret hint is gone")
	}
	if len(p.events) != 0 {
		t.Errorf("a refused save was audited: %+v", p.events)
	}
}

func TestHostingSaveBlankSecretKeepsTheStoredKey(t *testing.T) {
	t.Parallel()

	f := staticHosting()
	p := newHostingSaveRig(t, f)
	rec := p.do("POST", hostingURL, "owner-token", hostingForm(url.Values{"secret_key": {""}}), nil)
	if rec.Code != 303 || len(f.saved) != 1 || f.saved[0].SecretKey != "" {
		t.Fatalf("code %d, saved %+v", rec.Code, f.saved)
	}
	if p.events[0].Detail["secret_changed"] != false {
		t.Errorf("detail = %v", p.events[0].Detail)
	}
}

func TestHostingSaveModeChangeGoesToConfirm(t *testing.T) {
	t.Parallel()

	f := servedHosting()
	p := newHostingSaveRig(t, f)
	rec := p.do("POST", hostingURL, "owner-token", hostingForm(nil), nil)
	if rec.Code != 303 || rec.Header().Get("Location") != hostingURL+"/mode?to=static" {
		t.Fatalf("save = %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if len(f.saved) != 1 || len(f.modes) != 0 {
		t.Errorf("saved %d, modes %v", len(f.saved), f.modes)
	}
}

func TestHostingSaveServiceFailureIsAnErrorPage(t *testing.T) {
	t.Parallel()

	f := staticHosting()
	f.saveFatal = errors.New("db down " + testHostingSecret)
	p := newHostingSaveRig(t, f)
	rec := p.do("POST", hostingURL, "owner-token", hostingForm(nil), nil)
	if rec.Code != 500 || strings.Contains(rec.Body.String(), testHostingSecret) || strings.Contains(rec.Body.String(), "db down") {
		t.Fatalf("code %d", rec.Code)
	}
}

func TestHostingSaveOwnerOnlyAndCSRF(t *testing.T) {
	t.Parallel()

	f := servedHosting()
	p := newHostingSaveRig(t, f)
	if rec := p.do("POST", hostingURL, "editor-token", hostingForm(nil), nil); rec.Code != 403 {
		t.Errorf("editor POST = %d, want 403", rec.Code)
	}
	req := httptest.NewRequest("POST", hostingURL, strings.NewReader(hostingForm(nil).Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "hatamoto_session", Value: "owner-token"})
	if rec := serve(p.h, req, nil); rec.Code != 403 {
		t.Errorf("POST without CSRF = %d, want 403", rec.Code)
	}
	if len(f.saved) != 0 {
		t.Errorf("saved %v", f.saved)
	}
}
