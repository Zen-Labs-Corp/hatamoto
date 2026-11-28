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
	setup     HostingSetup
	probe     string
	probeErr  error
	trustErr  error
	trusted   []string
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
func (f *fakeHosting) Runs(_ context.Context, limit int) ([]HostingRun, error) {
	return f.runs[:min(limit, len(f.runs))], nil
}
func (f *fakeHosting) LiveRuns(_ context.Context, limit int) ([]HostingRun, error) {
	var out []HostingRun
	for _, r := range f.runs {
		if r.Trigger != HostingTriggerTest && len(out) < limit {
			out = append(out, r)
		}
	}
	return out, nil
}
func (f *fakeHosting) LastRun(context.Context) (HostingRun, bool, error) {
	for _, r := range f.runs {
		if r.Result != HostingRunRunning && r.Trigger != HostingTriggerTest {
			return r, true, nil
		}
	}
	return HostingRun{}, false, nil
}
func (f *fakeHosting) Setup(context.Context) (HostingSetup, error)  { return f.setup, nil }
func (f *fakeHosting) ProbeHostKey(context.Context) (string, error) { return f.probe, f.probeErr }
func (f *fakeHosting) TrustHostKey(_ context.Context, fp string) error {
	if f.trustErr != nil {
		return f.trustErr
	}
	f.trusted = append(f.trusted, fp)
	return nil
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
	for _, want := range []string{"Served by Hatamoto", "Static files &#43; live islands", "Forms", "/search", "Stays on Hatamoto: admin, API.", "No exports yet.", `id="hosting-form"`, `value="served" checked`,
		"Test export", "Export now", "Rebuild all", `id="test-form"`, `id="export-form"`, `id="rebuild-form"`,
		"Save a target first.", "Needs static mode. Use Test export first.", "These apply once the site is in static mode."} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	for _, not := range []string{"Nothing has been exported yet.", ">live<", `id="hosting-crawlers"`, `id="hosting-islands-inactive"`, "These parts of the site still call this server."} {
		if strings.Contains(body, not) {
			t.Errorf("served page has %q", not)
		}
	}
	if n := strings.Count(body, "No exports yet."); n != 1 {
		t.Errorf("No exports yet. appears %d times, want 1", n)
	}
	for _, id := range []string{"test-form", "export-form", "rebuild-form"} {
		if n := strings.Count(body, `disabled="disabled" form="`+id+`"`); n != 1 {
			t.Errorf("button for %s: %d disabled, want 1", id, n)
		}
	}
}

// With a saved directory the Test export works in served mode; Export now still needs static mode.
func TestHostingPageServedWithTarget(t *testing.T) {
	t.Parallel()

	f := servedHosting()
	f.view.Dir = "/srv/site/public"
	body := newHostingRig(t, f).do("GET", hostingURL, "owner-token", nil, nil).Body.String()
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
	if strings.Contains(body, `aria-describedby="hosting-test-why"`) || strings.Contains(body, "Save a target first.") {
		t.Error("Test export is disabled with a saved target")
	}
	if !strings.Contains(body, `aria-describedby="hosting-export-why"`) || !strings.Contains(body, "Needs static mode.") {
		t.Error("Export now is not disabled in served mode")
	}
}

func TestHostingPageWorkerOff(t *testing.T) {
	t.Parallel()

	f := staticHosting()
	f.view.WorkerOff = true
	body := newHostingRig(t, f).do("GET", hostingURL, "owner-token", nil, nil).Body.String()
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
	if !strings.Contains(body, "The export worker is off on this server.") || strings.Count(body, `aria-describedby="hosting-export-why"`) != 2 {
		t.Error("Export now and Rebuild all are not disabled with the worker reason")
	}
	if strings.Contains(body, `hosting-test-why">The export worker`) {
		t.Error("Test export carries the worker reason although it needs no worker")
	}
}

func TestHostingPageStatic(t *testing.T) {
	t.Parallel()

	f := staticHosting()
	p := newHostingRig(t, f)
	body := p.do("GET", hostingURL, "owner-token", nil, nil).Body.String()
	if !strings.Contains(body, `aria-describedby="hosting-test-why"`) || !strings.Contains(body, "In static mode, use Export now.") {
		t.Error("Test export is not disabled with its reason in static mode")
	}
	if !strings.Contains(body, "parent must be writable by the service user.") {
		t.Error("the Directory field lacks the writable-parent help")
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
	for _, want := range []string{"Export now", "Rebuild all", `id="export-form"`, `id="rebuild-form"`, `id="hosting-crawlers"`, "214 pages", "2 minutes ago", "https://live.example.com", "upload refused", "Test export", `id="test-form"`, "These parts of the site still call this server.", ">live<"} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if !strings.Contains(body, "Failed") || !strings.Contains(body, "hm-badge--danger") {
		t.Error("the failed run has no danger badge")
	}
	if strings.Contains(body, `aria-describedby="hosting-export-why"`) {
		t.Error("Export now or Rebuild all is disabled in static mode with a target")
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

func TestHostingPageLiveMode(t *testing.T) {
	t.Parallel()

	const (
		sameChecked     = `value="same" checked`
		separateChecked = `value="separate" checked`
		closed          = `<details class="hm-disclosure"><summary>Advanced: separate live origin`
		open            = `<details class="hm-disclosure" open><summary>Advanced: separate live origin`
	)
	// Same host: no live origin saved, in served and in static mode.
	for _, f := range []*fakeHosting{servedHosting(), staticHosting()} {
		f.view.LiveOrigin = ""
		f.live = []HostingIsland{{Label: "Forms", Route: "/forms/*", Status: "via proxy"}}
		body := newHostingRig(t, f).do("GET", hostingURL, "owner-token", nil, nil).Body.String()
		if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
			t.Errorf("a11y: %v", issues)
		}
		if !strings.Contains(body, sameChecked) || strings.Contains(body, separateChecked) || !strings.Contains(body, closed) {
			t.Errorf("%s: same host is not the selected, closed choice", f.view.Mode)
		}
		if f.view.Mode == HostingStatic && !strings.Contains(body, "via proxy") {
			t.Error("static same host has no via proxy status")
		}
	}
	// A saved live origin keeps working unchanged: separate, disclosure open.
	body := newHostingRig(t, staticHosting()).do("GET", hostingURL, "owner-token", nil, nil).Body.String()
	if !strings.Contains(body, separateChecked) || strings.Contains(body, sameChecked) || !strings.Contains(body, open) || !strings.Contains(body, "https://live.example.com") {
		t.Error("a saved live origin is not shown as the open separate choice")
	}
	// A refused save with separate and no origin shows the disclosure open with the error.
	f := servedHosting()
	p := newHostingSaveRig(t, f)
	body = p.do("POST", hostingURL, "owner-token", hostingForm(url.Values{"live_mode": {"separate"}, "live_origin": {""}}), nil).Body.String()
	if !strings.Contains(body, open) || !strings.Contains(body, separateChecked) || !strings.Contains(body, `href="#hosting-live-origin"`) {
		t.Error("the refused page hides the live origin error")
	}
}

func TestHostingPageS3SameHostWarning(t *testing.T) {
	t.Parallel()

	const warn = `id="hosting-s3-same-host"`
	page := func(mut func(*HostingView)) string {
		f := staticHosting()
		mut(&f.view)
		return newHostingRig(t, f).do("GET", hostingURL, "owner-token", nil, nil).Body.String()
	}
	body := page(func(v *HostingView) { v.Target, v.LiveOrigin = HostingTargetS3, "" })
	if !strings.Contains(body, warn) || !strings.Contains(body, "A bucket cannot pass island paths to Hatamoto") {
		t.Error("S3 + same host has no warning")
	}
	for name, mut := range map[string]func(*HostingView){
		"s3 separate":    func(v *HostingView) { v.Target, v.LiveOrigin = HostingTargetS3, "https://live.example.com" },
		"dir same host":  func(v *HostingView) { v.Target, v.LiveOrigin = HostingTargetDir, "" },
		"served mode s3": func(v *HostingView) { v.Mode, v.Target, v.LiveOrigin = HostingServed, HostingTargetS3, "" },
	} {
		if strings.Contains(page(mut), warn) {
			t.Errorf("%s: unexpected warning", name)
		}
	}
}

func dirSetupHosting() *fakeHosting {
	f := staticHosting()
	f.view = HostingView{Mode: HostingStatic, Target: HostingTargetDir, Dir: "/srv/site/public"}
	f.setup = HostingSetup{
		Saved: true, Target: HostingTargetDir, SameHost: true, Site: "https://example.com", Upstream: "127.0.0.1:8090",
		Caddy: "example.com {\n\treverse_proxy 127.0.0.1:8090\n}\n", Nginx: "server {\n\tproxy_pass http://127.0.0.1:8090;\n}\n",
		Folder: HostingCheckOK, Exact: 3, Wildcard: 1, RedirectsSplit: true, RedirectsTotal: 4, Reach: HostingCheckUnknown,
		HeaderNames: []string{"X-Content-Type-Options", "X-Frame-Options"},
	}
	return f
}

func TestHostingPageSetupCard(t *testing.T) {
	t.Parallel()

	p := newHostingRig(t, dirSetupHosting())
	get := func(q string) string {
		t.Helper()
		body := p.do("GET", hostingURL+q, "owner-token", nil, nil).Body.String()
		if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
			t.Errorf("a11y %q: %v", q, issues)
		}
		return body
	}
	body := get("")
	for _, want := range []string{`id="hosting-setup"`, "Set up your web server", "Based on saved settings.",
		`href="/admin/settings/hosting?setup=nginx#hosting-setup"`, `aria-current="page">Caddy`, "S3 &#43; CDN",
		`id="hosting-setup-config"`, "reverse_proxy 127.0.0.1:8090", `data-hm-copy-field="hosting-setup-config"`, `id="hosting-setup-form"`,
		"Upstream 127.0.0.1:8090 is this server&#39;s listen address.",
		"Not checked yet. It runs after the next export on this server.", "The folder exists and the service user can write to it and to its parent.",
		"3 exact rules become meta-refresh pages and 1 wildcard rule is lost", "included in the config below"} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(body, "Run Test export") {
		t.Error("the card sends the owner to a Test export that static mode disables")
	}
	if strings.Contains(body, `form="hosting-form"`) || strings.Contains(body, `id="hosting-setup-form" method`) {
		t.Error("the config can post with Save")
	}
	if !strings.Contains(body, `readonly`) || !strings.Contains(body, `form="hosting-setup-form"`) {
		t.Error("the config is not a readonly field of the empty form")
	}

	nginx := get("?setup=nginx")
	if !strings.Contains(nginx, "proxy_pass http://127.0.0.1:8090") || strings.Contains(nginx, "reverse_proxy") || !strings.Contains(nginx, `aria-current="page">nginx`) {
		t.Error("the nginx tab does not show the nginx config")
	}
	s3 := get("?setup=s3")
	if !strings.Contains(s3, "A bucket cannot pass islands to this server") || !strings.Contains(s3, "<code>X-Frame-Options</code>") || strings.Contains(s3, `id="hosting-setup-config"`) {
		t.Error("the S3 + CDN tab lacks its guidance")
	}
	// A directory target on the S3 tab has no folder row and no config to point at.
	for _, bad := range []string{"The folder exists", "included in the config below", "The config below includes them.", "(see S3 + CDN)"} {
		if strings.Contains(s3, bad) {
			t.Errorf("the S3 + CDN tab shows %q", bad)
		}
	}
	if !strings.Contains(s3, "Add them at your CDN") {
		t.Error("the S3 + CDN tab does not send the headers to the CDN")
	}
	if evil := get("?setup=evil%22%3E"); !strings.Contains(evil, `aria-current="page">Caddy`) || strings.Contains(evil, "evil") {
		t.Error("an unknown tab does not fall back to the default")
	}
}

func TestHostingPageSetupCardForS3AndErrors(t *testing.T) {
	t.Parallel()

	// An S3 target opens on the CDN tab; Caddy says there is nothing to copy.
	f := staticHosting()
	f.setup = HostingSetup{Saved: true, Target: HostingTargetS3, RedirectsTotal: 4, Reach: HostingCheckOK, ReachAt: time.Date(2026, 11, 21, 14, 5, 0, 0, time.UTC), Site: "https://example.com"}
	p := newHostingRig(t, f)
	body := p.do("GET", hostingURL, "owner-token", nil, nil).Body.String()
	if !strings.Contains(body, `aria-current="page">S3 &#43; CDN`) || !strings.Contains(body, "A bucket cannot pass islands") ||
		!strings.Contains(body, "The last export has 4 redirect rules.") || !strings.Contains(body, "https://example.com answers. Checked 21 Nov ") {
		t.Errorf("S3 target card is wrong: %s", body[strings.Index(body, `id="hosting-setup"`):][:3000])
	}
	if body = p.do("GET", hostingURL+"?setup=caddy", "owner-token", nil, nil).Body.String(); !strings.Contains(body, "there is no web server config to copy") {
		t.Error("the Caddy tab of an S3 target has no note")
	}

	// A refused config shows its reason, never a config.
	f = dirSetupHosting()
	f.setup.Caddy, f.setup.Nginx, f.setup.GenError = "", "", "The folder contains characters a web server config cannot hold."
	body = newHostingRig(t, f).do("GET", hostingURL, "owner-token", nil, nil).Body.String()
	if !strings.Contains(body, `id="hosting-setup-error"`) || strings.Contains(body, `id="hosting-setup-config"`) {
		t.Error("a refused config is not shown as an error")
	}

	// Nothing saved: no card.
	if body = newHostingRig(t, servedHosting()).do("GET", hostingURL, "owner-token", nil, nil).Body.String(); strings.Contains(body, `id="hosting-setup"`) {
		t.Error("the card shows with no saved target")
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
		{"no live origin", url.Values{"live_mode": {"separate"}, "live_origin": {""}}, "Enter the address where the live islands answer."},
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

func TestHostingSaveSameHostNeedsNoLiveOrigin(t *testing.T) {
	t.Parallel()

	f := servedHosting()
	p := newHostingSaveRig(t, f)
	rec := p.do("POST", hostingURL, "owner-token", hostingForm(url.Values{"live_mode": {"same"}, "live_origin": {""}}), nil)
	if rec.Code != 303 || rec.Header().Get("Location") != hostingURL+"/mode?to=static" {
		t.Fatalf("save = %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if len(f.saved) != 1 || f.saved[0].LiveOrigin != "" || f.saved[0].LiveMode != HostingLiveSame {
		t.Fatalf("saved = %+v", f.saved)
	}
}

func TestHostingSaveLiveModeDerivation(t *testing.T) {
	t.Parallel()

	// An older form posts only the origin: it means a separate live origin.
	f := servedHosting()
	p := newHostingSaveRig(t, f)
	if rec := p.do("POST", hostingURL, "owner-token", hostingForm(nil), nil); rec.Code != 303 {
		t.Fatalf("save = %d", rec.Code)
	}
	if got := f.saved[0]; got.LiveMode != HostingLiveSeparate || got.LiveOrigin != "https://live.example.com" {
		t.Fatalf("saved = %+v", got)
	}
	// Same host drops a stale origin that was still in the form.
	f = servedHosting()
	p = newHostingSaveRig(t, f)
	if rec := p.do("POST", hostingURL, "owner-token", hostingForm(url.Values{"live_mode": {"same"}}), nil); rec.Code != 303 {
		t.Fatalf("save = %d", rec.Code)
	}
	if got := f.saved[0]; got.LiveOrigin != "" {
		t.Fatalf("a stale origin was saved: %+v", got)
	}
}

// A saved static site that goes back to the same host publishes differently,
// so it asks for the password like any other live origin change.
func TestHostingSaveStaticToSameHostNeedsSudo(t *testing.T) {
	t.Parallel()

	f := staticHosting()
	p := newHostingSaveRig(t, f)
	form := hostingForm(url.Values{"live_mode": {"same"}, "secret_key": {""}})
	rec := p.do("POST", hostingURL, "owner-token", form, nil)
	if rec.Code != 303 || !strings.HasPrefix(rec.Header().Get("Location"), p.a.sudoPath()) || len(f.saved) != 0 {
		t.Fatalf("save = %d %s, saved %d", rec.Code, rec.Header().Get("Location"), len(f.saved))
	}
	grantSudo(p.rig)
	if rec := p.do("POST", hostingURL, "owner-token", form, nil); rec.Code != 303 || len(f.saved) != 1 || f.saved[0].LiveOrigin != "" {
		t.Fatalf("with sudo = %d, saved %+v", rec.Code, f.saved)
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
	grantSudo(p.rig)
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
	grantSudo(p.rig)
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
	grantSudo(p.rig)
	rec := p.do("POST", hostingURL, "owner-token", hostingForm(url.Values{"secret_key": {""}}), nil)
	if rec.Code != 303 || len(f.saved) != 1 || f.saved[0].SecretKey != "" {
		t.Fatalf("code %d, saved %+v", rec.Code, f.saved)
	}
	if p.events[0].Detail["secret_changed"] != false {
		t.Errorf("detail = %v", p.events[0].Detail)
	}
}

func TestHostingSaveStaticTargetChangeNeedsSudo(t *testing.T) {
	t.Parallel()

	for name, extra := range map[string]url.Values{
		"target":      {"target": {"dir"}, "dir": {"/srv/site"}},
		"live origin": {"live_origin": {"https://other.example.com"}},
		"secret":      {},
	} {
		f := staticHosting()
		p := newHostingSaveRig(t, f)
		form := hostingForm(extra)
		if name != "secret" {
			form.Set("secret_key", "")
		}
		rec := p.do("POST", hostingURL, "owner-token", form, nil)
		if rec.Code != 303 || !strings.HasPrefix(rec.Header().Get("Location"), p.a.sudoPath()) {
			t.Fatalf("%s: save = %d %s, want the sudo page", name, rec.Code, rec.Header().Get("Location"))
		}
		if len(f.saved) != 0 || len(p.events) != 0 {
			t.Fatalf("%s: saved %d, audited %d before sudo", name, len(f.saved), len(p.events))
		}
		grantSudo(p.rig)
		if rec := p.do("POST", hostingURL, "owner-token", form, nil); rec.Code != 303 || len(f.saved) != 1 {
			t.Fatalf("%s: with sudo = %d, saved %d", name, rec.Code, len(f.saved))
		}
	}
}

// Posting the page back unchanged needs no password.
func TestHostingSaveStaticUnchangedNeedsNoSudo(t *testing.T) {
	t.Parallel()

	f := staticHosting()
	p := newHostingSaveRig(t, f)
	v := f.view
	form := url.Values{
		"mode": {v.Mode}, "target": {v.Target}, "dir": {v.Dir}, "service": {v.Service}, "endpoint": {v.Endpoint},
		"bucket": {v.Bucket}, "prefix": {v.Prefix}, "region": {v.Region}, "access_key": {v.AccessKey},
		"secret_key": {""}, "live_origin": {v.LiveOrigin},
	}
	rec := p.do("POST", hostingURL, "owner-token", form, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != hostingURL || len(f.saved) != 1 {
		t.Fatalf("unchanged save = %d %s, saved %d", rec.Code, rec.Header().Get("Location"), len(f.saved))
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
	grantSudo(p.rig)
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
