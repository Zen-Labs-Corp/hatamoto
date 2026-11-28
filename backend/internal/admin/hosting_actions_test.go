package admin

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

func TestHostingModeConfirmPostsOnlyTheMode(t *testing.T) {
	t.Parallel()

	p := newHostingSaveRig(t, servedHosting())
	rec := p.do("GET", hostingURL+"/mode?to=static", "owner-token", nil, nil)
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, "Switch to static files?") || !strings.Contains(body, `action="/admin/settings/hosting/mode"`) {
		t.Fatalf("confirm = %d", rec.Code)
	}
	if !strings.Contains(body, `name="mode" value="static"`) || strings.Contains(body, `name="secret_key"`) {
		t.Error("the confirm form carries the wrong fields")
	}
	form := body[strings.Index(body, `action="/admin/settings/hosting/mode"`):]
	form = form[:strings.Index(form, "</form>")]
	if n := strings.Count(form, `type="hidden"`); n != 2 { // csrf + mode
		t.Errorf("%d hidden inputs in the confirm form, want 2", n)
	}
	if rec = p.do("GET", hostingURL+"/mode?to=served", "owner-token", nil, nil); !strings.Contains(rec.Body.String(), "Switch to served") {
		t.Error("no served confirm")
	}
	if rec = p.do("GET", hostingURL+"/mode?to=bogus", "owner-token", nil, nil); rec.Code != 303 || rec.Header().Get("Location") != hostingURL {
		t.Errorf("bogus mode = %d %s", rec.Code, rec.Header().Get("Location"))
	}
}

func TestHostingModeSwitchStartsAFullExport(t *testing.T) {
	t.Parallel()

	f := servedHosting()
	p := newHostingSaveRig(t, f)
	grantSudo(p.rig)
	rec := p.do("POST", hostingURL+"/mode", "owner-token", url.Values{"mode": {"static"}}, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != hostingURL {
		t.Fatalf("switch = %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if !reflect.DeepEqual(f.modes, []string{"static"}) || !reflect.DeepEqual(f.exports, []hostingExportCall{{true, "mode"}}) {
		t.Fatalf("modes %v, exports %v", f.modes, f.exports)
	}
	if len(p.events) != 1 || p.events[0].Action != "hosting.mode" {
		t.Errorf("audit = %+v", p.events)
	}
	if body := followFlashTo(p.ownerRig, hostingURL, rec); !strings.Contains(body, "The first export has started.") {
		t.Error("no flash")
	}
}

func TestHostingModeSwitchToServedStartsNothing(t *testing.T) {
	t.Parallel()

	f := staticHosting()
	p := newHostingSaveRig(t, f)
	grantSudo(p.rig)
	rec := p.do("POST", hostingURL+"/mode", "owner-token", url.Values{"mode": {"served"}}, nil)
	if rec.Code != 303 || !reflect.DeepEqual(f.modes, []string{"served"}) || len(f.exports) != 0 {
		t.Fatalf("code %d, modes %v, exports %v", rec.Code, f.modes, f.exports)
	}
}

func TestHostingModeRefusedShowsTheReason(t *testing.T) {
	t.Parallel()

	f := servedHosting()
	f.modeErr = errors.New("site access is on")
	p := newHostingSaveRig(t, f)
	grantSudo(p.rig)
	rec := p.do("POST", hostingURL+"/mode", "owner-token", url.Values{"mode": {"static"}}, nil)
	if rec.Code != 303 || len(f.exports) != 0 || len(p.events) != 0 {
		t.Fatalf("code %d, exports %v, audit %v", rec.Code, f.exports, p.events)
	}
	if body := followFlashTo(p.ownerRig, hostingURL, rec); !strings.Contains(body, "site access is on.") {
		t.Error("the reason is not shown")
	}
	if rec = p.do("POST", hostingURL+"/mode", "owner-token", url.Values{"mode": {"bogus"}}, nil); rec.Code != 303 || len(f.modes) != 0 {
		t.Errorf("bogus mode = %d, modes %v", rec.Code, f.modes)
	}
}

func TestHostingSudoActionsNeedThePassword(t *testing.T) {
	t.Parallel()

	f := staticHosting()
	p := newHostingSaveRig(t, f)
	for _, path := range []string{"/mode", "/rebuild"} {
		rec := p.do("POST", hostingURL+path, "owner-token", url.Values{"mode": {"served"}}, nil)
		if rec.Code != 303 || !strings.HasPrefix(rec.Header().Get("Location"), p.a.sudoPath()) {
			t.Errorf("%s without sudo = %d %s", path, rec.Code, rec.Header().Get("Location"))
		}
	}
	if len(f.modes) != 0 || len(f.exports) != 0 || len(p.events) != 0 {
		t.Errorf("service touched: modes %v, exports %v, audit %v", f.modes, f.exports, p.events)
	}
}

func TestHostingExportAndRebuild(t *testing.T) {
	t.Parallel()

	f := staticHosting()
	p := newHostingSaveRig(t, f)
	if rec := p.do("POST", hostingURL+"/export", "owner-token", url.Values{}, nil); rec.Code != 303 {
		t.Fatalf("export without sudo = %d", rec.Code)
	}
	grantSudo(p.rig)
	rec := p.do("POST", hostingURL+"/rebuild", "owner-token", url.Values{}, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != hostingURL {
		t.Fatalf("rebuild = %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if want := []hostingExportCall{{false, "manual"}, {true, "rebuild"}}; !reflect.DeepEqual(f.exports, want) {
		t.Errorf("exports = %v, want %v", f.exports, want)
	}
	if body := followFlashTo(p.ownerRig, hostingURL, rec); !strings.Contains(body, "Full rebuild started.") {
		t.Error("no rebuild flash")
	}
}

func TestHostingExportErrors(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		err  error
		want string
	}{
		{ErrHostingNotStatic, "exports run only in static mode."},
		{ErrHostingExportQueued, "another starts when it finishes."},
		{errors.New("target unreachable"), "The export could not be started: target unreachable."},
	} {
		f := servedHosting()
		f.exportErr = tc.err
		p := newHostingSaveRig(t, f)
		rec := p.do("POST", hostingURL+"/export", "owner-token", url.Values{}, nil)
		if body := followFlashTo(p.ownerRig, hostingURL, rec); rec.Code != 303 || !strings.Contains(body, tc.want) || len(p.events) != 0 {
			t.Errorf("%v: code %d, flash lacks %q or audited", tc.err, rec.Code, tc.want)
		}
	}
}

func TestHostingActionsAreOwnerOnly(t *testing.T) {
	t.Parallel()

	f := staticHosting()
	p := newHostingSaveRig(t, f)
	grantSudo(p.rig)
	if rec := p.do("GET", hostingURL+"/mode?to=static", "editor-token", nil, nil); rec.Code != 403 {
		t.Errorf("editor confirm = %d, want 403", rec.Code)
	}
	for _, path := range []string{"/mode", "/export", "/rebuild", "/test"} {
		if rec := p.do("POST", hostingURL+path, "editor-token", url.Values{"mode": {"served"}}, nil); rec.Code != 403 {
			t.Errorf("editor POST %s = %d, want 403", path, rec.Code)
		}
	}
	if len(f.modes) != 0 || len(f.exports) != 0 || f.tests != 0 {
		t.Errorf("service touched: %v %v %d", f.modes, f.exports, f.tests)
	}
}

func TestHostingTestExportStartsWithoutSudo(t *testing.T) {
	t.Parallel()

	f := servedHosting()
	p := newHostingSaveRig(t, f)
	rec := p.do("POST", hostingURL+"/test", "owner-token", url.Values{}, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != hostingURL || f.tests != 1 {
		t.Fatalf("test export = %d %s, calls %d", rec.Code, rec.Header().Get("Location"), f.tests)
	}
	if len(p.events) != 1 || p.events[0].Action != "hosting.test_export" {
		t.Errorf("audit = %+v", p.events)
	}
	if body := followFlashTo(p.ownerRig, hostingURL, rec); !strings.Contains(body, "Test export started.") {
		t.Error("no start flash")
	}
}

func TestHostingTestExportErrors(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		err  error
		want string
	}{
		{ErrHostingNoTarget, "Save a target first."},
		{ErrHostingTestBusy, "An export is running. Try the test export again when it finishes."},
		{errors.New("target unreachable"), "The test export could not be started: target unreachable."},
	} {
		f := servedHosting()
		f.testErr = tc.err
		p := newHostingSaveRig(t, f)
		rec := p.do("POST", hostingURL+"/test", "owner-token", url.Values{}, nil)
		if body := followFlashTo(p.ownerRig, hostingURL, rec); rec.Code != 303 || !strings.Contains(body, tc.want) || len(p.events) != 0 {
			t.Errorf("%v: code %d, flash lacks %q or audited", tc.err, rec.Code, tc.want)
		}
	}
}

func TestHostingTestExportNeedsCSRF(t *testing.T) {
	t.Parallel()

	f := servedHosting()
	p := newHostingSaveRig(t, f)
	req := httptest.NewRequest("POST", hostingURL+"/test", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "hatamoto_session", Value: "owner-token"})
	if rec := serve(p.h, req, nil); rec.Code != 403 || f.tests != 0 {
		t.Errorf("POST without CSRF = %d, calls %d", rec.Code, f.tests)
	}
}

// In static mode Test export would publish to the live target: it is refused.
func TestHostingTestExportRefusedInStaticMode(t *testing.T) {
	t.Parallel()

	f := staticHosting()
	p := newHostingSaveRig(t, f)
	rec := p.do("POST", hostingURL+"/test", "owner-token", url.Values{}, nil)
	if body := followFlashTo(p.ownerRig, hostingURL, rec); rec.Code != 303 || !strings.Contains(body, "In static mode, use Export now.") {
		t.Errorf("code %d, no refusal flash", rec.Code)
	}
	if f.tests != 0 || len(p.events) != 0 {
		t.Errorf("test export ran in static mode: %d calls, audit %+v", f.tests, p.events)
	}
}

func TestHostingHostKeyConfirmShowsFingerprint(t *testing.T) {
	t.Parallel()

	f := sftpHosting()
	f.probe = testSFTPPrint
	p := newHostingSaveRig(t, f)
	rec := p.do("GET", hostingURL+"/host-key", "owner-token", nil, nil)
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, "Trust this host key?") || !strings.Contains(body, testSFTPPrint) {
		t.Fatalf("confirm = %d", rec.Code)
	}
	if !strings.Contains(body, `name="host_key" value="`+testSFTPPrint+`"`) || !strings.Contains(body, `action="/admin/settings/hosting/host-key"`) {
		t.Error("the confirm form lacks the fingerprint or posts elsewhere")
	}
	if len(f.trusted) != 0 || len(f.saved) != 0 || len(p.events) != 0 {
		t.Errorf("the GET wrote: trusted %v, saved %v, audit %v", f.trusted, f.saved, p.events)
	}
}

func TestHostingHostKeyConfirmNoTarget(t *testing.T) {
	t.Parallel()

	f := servedHosting()
	f.probeErr = ErrHostingNoTarget
	p := newHostingSaveRig(t, f)
	rec := p.do("GET", hostingURL+"/host-key", "owner-token", nil, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != hostingURL {
		t.Fatalf("confirm = %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if body := followFlashTo(p.ownerRig, hostingURL, rec); !strings.Contains(body, "Save an SFTP target first.") {
		t.Error("no flash")
	}
}

func TestHostingTrustHostKeyNeedsSudo(t *testing.T) {
	t.Parallel()

	f := sftpHosting()
	p := newHostingSaveRig(t, f)
	rec := p.do("POST", hostingURL+"/host-key", "owner-token", url.Values{"host_key": {testSFTPPrint}}, nil)
	if rec.Code != 303 || !strings.HasPrefix(rec.Header().Get("Location"), p.a.sudoPath()) {
		t.Fatalf("trust = %d %s, want the sudo page", rec.Code, rec.Header().Get("Location"))
	}
	if len(f.trusted) != 0 {
		t.Errorf("trusted before sudo: %v", f.trusted)
	}
}

func TestHostingTrustHostKeyStoresAndAudits(t *testing.T) {
	t.Parallel()

	f := sftpHosting()
	p := newHostingSaveRig(t, f)
	grantSudo(p.rig)
	rec := p.do("POST", hostingURL+"/host-key", "owner-token", url.Values{"host_key": {testSFTPPrint}}, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != hostingURL {
		t.Fatalf("trust = %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if !reflect.DeepEqual(f.trusted, []string{testSFTPPrint}) {
		t.Fatalf("trusted = %v", f.trusted)
	}
	if len(p.events) != 1 || p.events[0].Action != "hosting.trust_host_key" || p.events[0].Detail["fingerprint"] != testSFTPPrint {
		t.Errorf("audit = %+v", p.events)
	}
	if body := followFlashTo(p.ownerRig, hostingURL, rec); !strings.Contains(body, "Host key trusted.") {
		t.Error("no flash")
	}
	// A value that is not a fingerprint is refused before the service.
	f.trusted = nil
	if rec = p.do("POST", hostingURL+"/host-key", "owner-token", url.Values{"host_key": {"nope"}}, nil); rec.Code != 303 || len(f.trusted) != 0 {
		t.Errorf("bad fingerprint = %d, trusted %v", rec.Code, f.trusted)
	}
}

func TestHostingTrustHostKeyChanged(t *testing.T) {
	t.Parallel()

	f := sftpHosting()
	f.trustErr = ErrHostingHostKeyChanged
	p := newHostingSaveRig(t, f)
	grantSudo(p.rig)
	rec := p.do("POST", hostingURL+"/host-key", "owner-token", url.Values{"host_key": {testSFTPPrint}}, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != hostingURL {
		t.Fatalf("trust = %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if body := followFlashTo(p.ownerRig, hostingURL, rec); !strings.Contains(body, "The host key changed since it was shown.") {
		t.Error("no warning flash")
	}
	if len(p.events) != 0 {
		t.Errorf("audited a refused trust: %+v", p.events)
	}
}
