package admin

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/admin/a11y"
	"hatamoto.dev/core/internal/email"
)

const emailTestPassword = "smtp-p4ss-SECRET-0123456789"

// fakeEmailSettings is an EmailSettingsService that keeps what was saved.
type fakeEmailSettings struct {
	view     EmailSettingsView
	checkErr error
	saveErr  error
	checks   []EmailInput
	saved    []EmailInput
	keeps    []bool
	resets   int
}

func (f *fakeEmailSettings) Get(context.Context) (EmailSettingsView, error) { return f.view, nil }
func (f *fakeEmailSettings) Check(_ context.Context, in EmailInput, keep bool) error {
	f.checks = append(f.checks, in)
	f.keeps = append(f.keeps, keep)
	return f.checkErr
}
func (f *fakeEmailSettings) Save(_ context.Context, in EmailInput, keep bool) error {
	if f.saveErr != nil {
		return f.saveErr
	}
	f.saved = append(f.saved, in)
	f.keeps = append(f.keeps, keep)
	f.view.Saved, f.view.Host, f.view.Username, f.view.Source, f.view.Configured = true, in.Host, in.Username, EmailSourceSaved, true
	return nil
}
func (f *fakeEmailSettings) Reset(context.Context) error {
	f.resets++
	f.view = EmailSettingsView{Source: EmailSourceServer}
	return nil
}

func savedEmailView() EmailSettingsView {
	return EmailSettingsView{
		Saved: true, Provider: "custom", Host: "smtp.example.com", Port: 587, Security: email.SecurityStarttls,
		Username: "apikey", FromAddress: "hello@example.com", FromName: "Example",
		PasswordSaved: true, Source: EmailSourceSaved, Configured: true,
	}
}

type emailRig struct {
	*ownerRig
	svc    *fakeEmailSettings
	events []AuditEvent
}

func newEmailRig(t *testing.T, svc *fakeEmailSettings) *emailRig {
	t.Helper()
	g := newOwnerRig(t, true)
	e := &emailRig{ownerRig: g, svc: svc}
	if svc != nil {
		g.a.deps.EmailSettings = svc
	}
	g.a.deps.Audit = func(_ context.Context, ev AuditEvent) { e.events = append(e.events, ev) }
	return e
}

func (e *emailRig) get(t *testing.T) string {
	t.Helper()
	rec := e.do("GET", "/admin/settings/email", "owner-token", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("GET = %d", rec.Code)
	}
	body := rec.Body.String()
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
	return body
}

func goodEmailForm() url.Values {
	return url.Values{
		"provider": {"custom"}, "host": {"smtp.example.com"}, "port": {"587"}, "security": {"starttls"},
		"username": {"apikey"}, "password": {emailTestPassword}, "from": {"hello@example.com"}, "from-name": {"Example"},
	}
}

func TestEmailPageGoldenAndNoPassword(t *testing.T) {
	t.Parallel()

	e := newEmailRig(t, &fakeEmailSettings{view: savedEmailView()})
	body := e.get(t)
	for _, want := range []string{"Email is set up", "Using: settings saved here", "Amazon SES", "Gmail / Google Workspace", "smtp.example.com", `value="apikey"`,
		`placeholder="Saved"`, "Leave blank to keep the saved password.", "Send a test email", "Use the server&#39;s SMTP_* settings instead", "hello@example.com"} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(body, emailTestPassword) {
		t.Error("page contains the password")
	}
	compareGolden(t, "pages/settings-email.html", normalizePage(body))
}

func TestEmailPageNotSetUp(t *testing.T) {
	t.Parallel()

	body := newEmailRig(t, &fakeEmailSettings{view: EmailSettingsView{Source: EmailSourceNone}}).get(t)
	for _, want := range []string{"Email is not set up", "Using: not set up", `value="587"`} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	for _, bad := range []string{"Send a test email", "SMTP_* settings instead"} {
		if strings.Contains(body, bad) {
			t.Errorf("page offers %q with nothing set up", bad)
		}
	}
}

func TestEmailPageUsesServerSettings(t *testing.T) {
	t.Parallel()

	body := newEmailRig(t, &fakeEmailSettings{view: EmailSettingsView{Source: EmailSourceServer, Configured: true}}).get(t)
	if !strings.Contains(body, "Using: the server&#39;s SMTP_* settings") {
		t.Error("source line missing")
	}
}

func TestEmailPasswordLostWarns(t *testing.T) {
	t.Parallel()

	v := savedEmailView()
	v.PasswordSaved, v.PasswordLost, v.Source = false, true, EmailSourceServer
	body := newEmailRig(t, &fakeEmailSettings{view: v}).get(t)
	if !strings.Contains(body, "Re-enter your SMTP password") || !strings.Contains(body, "Using: the server&#39;s SMTP_* settings") {
		t.Error("lost-password warning or source missing")
	}
	if strings.Contains(body, `placeholder="Saved"`) {
		t.Error("claims a password is saved")
	}
}

func TestEmailDisabledWarns(t *testing.T) {
	t.Parallel()

	v := savedEmailView()
	v.Disabled = true
	if body := newEmailRig(t, &fakeEmailSettings{view: v}).get(t); !strings.Contains(body, "EMAIL_DISABLED") {
		t.Error("disabled warning missing")
	}
}

func TestEmailUnavailableAndRoles(t *testing.T) {
	t.Parallel()

	e := newEmailRig(t, nil)
	if rec := e.do("GET", "/admin/settings/email", "owner-token", nil, nil); rec.Code != 503 {
		t.Errorf("nil dep GET = %d, want 503", rec.Code)
	}
	e = newEmailRig(t, &fakeEmailSettings{view: savedEmailView()})
	if rec := e.do("GET", "/admin/settings/email", "editor-token", nil, nil); rec.Code != 403 {
		t.Errorf("editor GET = %d", rec.Code)
	}
	if rec := e.do("GET", "/admin/settings/email", "", nil, nil); rec.Code != 303 {
		t.Errorf("anonymous GET = %d", rec.Code)
	}
	for _, p := range []string{"", "/test", "/reset"} {
		if rec := e.do("POST", "/admin/settings/email"+p, "editor-token", goodEmailForm(), nil); rec.Code != 403 {
			t.Errorf("editor POST %s = %d", p, rec.Code)
		}
	}
	if len(e.svc.saved) != 0 || e.svc.resets != 0 {
		t.Error("editor changed settings")
	}
}

func TestEmailSave(t *testing.T) {
	t.Parallel()

	e := newEmailRig(t, &fakeEmailSettings{view: EmailSettingsView{Source: EmailSourceNone}})
	rec := e.do("POST", "/admin/settings/email", "owner-token", goodEmailForm(), nil)
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/settings/email" {
		t.Fatalf("save = %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if len(e.svc.checks) != 1 || len(e.svc.saved) != 1 || e.svc.saved[0].Password != emailTestPassword || e.svc.saved[0].Host != "smtp.example.com" {
		t.Fatalf("check/save = %+v %+v", e.svc.checks, e.svc.saved)
	}
	if len(e.events) != 1 || e.events[0].Action != "email.settings" {
		t.Fatalf("audit = %+v", e.events)
	}
	if strings.Contains(fmt.Sprint(e.events[0]), emailTestPassword) {
		t.Error("audit contains the password")
	}
}

func TestEmailSaveBlankPasswordKeepsSaved(t *testing.T) {
	t.Parallel()

	e := newEmailRig(t, &fakeEmailSettings{view: savedEmailView()})
	form := goodEmailForm()
	form.Set("password", "")
	if rec := e.do("POST", "/admin/settings/email", "owner-token", form, nil); rec.Code != 303 {
		t.Fatalf("save = %d", rec.Code)
	}
	if len(e.svc.saved) != 1 || e.svc.saved[0].Password != "" || !e.svc.keeps[0] || !e.svc.keeps[1] {
		t.Errorf("keep not requested: %+v %v", e.svc.saved, e.svc.keeps)
	}
	// A changed host must not reuse the saved password.
	form.Set("host", "evil.example.net")
	rec := e.do("POST", "/admin/settings/email", "owner-token", form, nil)
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), "Enter the password for this host and username.") || len(e.svc.saved) != 1 {
		t.Errorf("host change: %d saved=%d", rec.Code, len(e.svc.saved))
	}
}

func TestEmailSaveValidationRefused(t *testing.T) {
	t.Parallel()

	e := newEmailRig(t, &fakeEmailSettings{view: EmailSettingsView{Source: EmailSourceNone}})
	form := goodEmailForm()
	form.Set("port", "0")
	form.Set("from", "nope")
	rec := e.do("POST", "/admin/settings/email", "owner-token", form, nil)
	body := rec.Body.String()
	if rec.Code != 422 || !strings.Contains(body, "Enter a port from 1 to 65535") || !strings.Contains(body, "Enter the address mail is sent from") {
		t.Fatalf("validation = %d", rec.Code)
	}
	if len(e.svc.checks) != 0 || len(e.svc.saved) != 0 {
		t.Error("checked or saved an invalid form")
	}
	if strings.Contains(body, emailTestPassword) {
		t.Error("password echoed")
	}
	if !strings.Contains(body, `value="smtp.example.com"`) {
		t.Error("typed values lost")
	}
}

func TestEmailSaveCheckFailureShowsSMTPError(t *testing.T) {
	t.Parallel()

	e := newEmailRig(t, &fakeEmailSettings{
		view:     EmailSettingsView{Source: EmailSourceNone},
		checkErr: &email.CheckError{Stage: "auth", Code: 535, Msg: "Authentication failed", Host: "smtp.example.com", Port: 587},
	})
	rec := e.do("POST", "/admin/settings/email", "owner-token", goodEmailForm(), nil)
	body := rec.Body.String()
	if rec.Code != 422 {
		t.Fatalf("status %d", rec.Code)
	}
	for _, want := range []string{"smtp.example.com:587", "535 Authentication failed", "Nothing saved."} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %q", want)
		}
	}
	if len(e.svc.saved) != 0 {
		t.Error("saved after a failed check")
	}
	if strings.Contains(body, emailTestPassword) {
		t.Error("password echoed")
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
}

func TestEmailSaveCheckGenericError(t *testing.T) {
	t.Parallel()

	e := newEmailRig(t, &fakeEmailSettings{view: EmailSettingsView{Source: EmailSourceNone}, checkErr: errors.New("boom")})
	rec := e.do("POST", "/admin/settings/email", "owner-token", goodEmailForm(), nil)
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), "Could not check the connection. Nothing saved.") || strings.Contains(rec.Body.String(), "boom") {
		t.Errorf("generic error = %d", rec.Code)
	}
}

func TestEmailTestSend(t *testing.T) {
	t.Parallel()

	e := newEmailRig(t, &fakeEmailSettings{view: savedEmailView()})
	rec := e.do("POST", "/admin/settings/email/test", "owner-token", url.Values{}, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/settings/email" || len(e.mail.tests) != 1 || e.mail.tests[0] != "owner@example.com" {
		t.Fatalf("test send: %d %s %v", rec.Code, rec.Header().Get("Location"), e.mail.tests)
	}
	rec = e.do("POST", "/admin/settings/email/test", "owner-token", url.Values{"return": {"done"}}, nil)
	if rec.Header().Get("Location") != "/admin/setup/done" {
		t.Errorf("return=done goes to %s", rec.Header().Get("Location"))
	}
	rec = e.do("POST", "/admin/settings/email/test", "owner-token", url.Values{"return": {"https://evil.example"}}, nil)
	if rec.Header().Get("Location") != "/admin/settings/email" {
		t.Errorf("unknown return goes to %s", rec.Header().Get("Location"))
	}
	e.mail.testErr = errors.New("smtp down")
	if rec := e.do("POST", "/admin/settings/email/test", "owner-token", url.Values{}, nil); rec.Code != 303 {
		t.Errorf("failed send = %d", rec.Code)
	}
}

// The test-send flash is a fixed sentence: the raw error (host, IP, remote
// reply) stays in the log.
func TestEmailTestSendFailureFlashHidesRawError(t *testing.T) {
	t.Parallel()

	e := newEmailRig(t, &fakeEmailSettings{view: savedEmailView()})
	e.mail.testErr = errors.New("dial tcp 10.0.0.5:6379: connect: connection refused")
	rec := e.do("POST", "/admin/settings/email/test", "owner-token", url.Values{}, nil)
	for _, c := range rec.Result().Cookies() {
		if c.Name != flashCookie {
			continue
		}
		raw, err := base64.RawURLEncoding.DecodeString(strings.SplitN(c.Value, ".", 2)[0])
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "10.0.0.5") || strings.Contains(string(raw), "refused") {
			t.Fatalf("flash leaks the raw error: %s", raw)
		}
		return
	}
	t.Fatal("no flash set")
}

func TestEmailTestSendNotConfiguredOrDisabled(t *testing.T) {
	t.Parallel()

	e := newEmailRig(t, &fakeEmailSettings{view: EmailSettingsView{Source: EmailSourceNone}})
	e.mail.configured = false
	e.do("POST", "/admin/settings/email/test", "owner-token", url.Values{}, nil)
	v := savedEmailView()
	v.Disabled = true
	e.svc.view = v
	e.mail.configured = true
	e.do("POST", "/admin/settings/email/test", "owner-token", url.Values{}, nil)
	if len(e.mail.tests) != 0 {
		t.Errorf("sent %v", e.mail.tests)
	}
}

func TestEmailReset(t *testing.T) {
	t.Parallel()

	e := newEmailRig(t, &fakeEmailSettings{view: savedEmailView()})
	rec := e.do("POST", "/admin/settings/email/reset", "owner-token", url.Values{}, nil)
	if rec.Code != 303 || e.svc.resets != 1 {
		t.Fatalf("reset = %d resets=%d", rec.Code, e.svc.resets)
	}
	if len(e.events) != 1 || e.events[0].Action != "email.settings.reset" {
		t.Errorf("audit = %+v", e.events)
	}
}
