package admin

import (
	"context"
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
func (f *fakeHosting) Runs(context.Context, int) ([]HostingRun, error) { return f.runs, nil }
func (f *fakeHosting) LastRun(context.Context) (HostingRun, bool, error) {
	for _, r := range f.runs {
		if r.Result != HostingRunRunning {
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
