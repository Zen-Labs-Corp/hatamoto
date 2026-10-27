package admin

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/email"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/mailsettings"
)

// EmailSource says where the live email configuration comes from.
type EmailSource string

const (
	// EmailSourceSaved: settings saved in Settings > Email.
	EmailSourceSaved EmailSource = "saved"
	// EmailSourceServer: the server's SMTP_* environment.
	EmailSourceServer EmailSource = "server"
	// EmailSourceNone: nothing usable is configured.
	EmailSourceNone EmailSource = "none"
)

// EmailInput is one submitted SMTP form after validation. Host is already
// derived for the SES and Gmail presets. Password is empty when the owner left
// it blank (see keepPassword on Check and Save).
type EmailInput struct {
	Provider    string
	SESRegion   string
	Host        string
	Port        int
	Security    email.Security
	Username    string
	Password    string
	FromAddress string
	FromName    string
}

// EmailSettingsView is what the Email tab and wizard step show: the saved
// values (never the password) and how the live configuration was chosen.
type EmailSettingsView struct {
	// Saved reports whether a settings row exists. The form fields below are
	// defaults (custom, 587, STARTTLS) when it does not.
	Saved       bool
	Provider    string
	SESRegion   string
	Host        string
	Port        int
	Security    email.Security
	Username    string
	FromAddress string
	FromName    string

	// PasswordSaved: a password is stored and can be read.
	PasswordSaved bool
	// PasswordLost: a password is stored but cannot be read (the server
	// SECRET changed); the owner must type it again.
	PasswordLost bool

	Source EmailSource
	// Configured: the live service can send.
	Configured bool
	// Disabled: EMAIL_DISABLED=true switches all sending off.
	Disabled bool
}

// EmailSettingsService is the SMTP configuration saved in the admin and
// applied to the running mail service without a restart.
type EmailSettingsService interface {
	Get(ctx context.Context) (EmailSettingsView, error)
	// Check connects and signs in with in without sending a message. A
	// *email.CheckError explains a failure. keepPassword uses the saved
	// password instead of in.Password.
	Check(ctx context.Context, in EmailInput, keepPassword bool) error
	// Save stores in (sealing the password) and applies it at once.
	// keepPassword keeps the saved password instead of in.Password.
	Save(ctx context.Context, in EmailInput, keepPassword bool) error
	// Reset deletes the saved settings so the server's SMTP_* apply again.
	Reset(ctx context.Context) error
}

const emailPath = "/settings/email"

// emailFields is the SMTP fields as shown, shared by Settings > Email and the
// setup wizard. There is deliberately no password value.
type emailFields struct {
	Errs map[string]string

	ProviderOptions []ui.Option
	RegionOptions   []ui.Option
	SecurityOptions []ui.Option
	Host            string
	Port            string
	Username        string
	FromAddress     string
	FromName        string

	PasswordSaved bool // a password is stored and readable
	PasswordLost  bool // a password is stored but cannot be read any more
}

// emailData is the Settings > Email page.
type emailData struct {
	Form    emailFields
	Summary []ui.FieldError
	// CheckFailed is the connection check's message after a refused save.
	CheckFailed string

	Configured bool
	Disabled   bool
	Saved      bool
	// Using is the "Using:" line: where the live settings come from.
	Using string
}

func emailUsing(src EmailSource) string {
	switch src {
	case EmailSourceSaved:
		return "settings saved here"
	case EmailSourceServer:
		return "the server's SMTP_* settings"
	default:
		return "not set up"
	}
}

// newEmailFields shows the saved values, or, after a failed save, what was
// submitted (echo.Posted).
func newEmailFields(v EmailSettingsView, echo emailFormEcho, errs []ui.FieldError) emailFields {
	provider, region, sec := v.Provider, v.SESRegion, string(v.Security)
	f := emailFields{
		Errs: map[string]string{},
		Host: v.Host, Username: v.Username, FromAddress: v.FromAddress, FromName: v.FromName,
		PasswordSaved: v.PasswordSaved, PasswordLost: v.PasswordLost,
	}
	if v.Port > 0 {
		f.Port = strconv.Itoa(v.Port)
	}
	if echo.Posted {
		provider, region, sec = echo.Provider, echo.SESRegion, echo.Security
		f.Host, f.Port, f.Username, f.FromAddress, f.FromName = echo.Host, echo.Port, echo.Username, echo.FromAddress, echo.FromName
	}
	if provider == "" {
		provider = mailsettings.ProviderCustom
	}
	if region == "" {
		region = mailsettings.DefaultSESRegion
	}
	if sec == "" {
		sec = string(email.SecurityStarttls)
	}
	if f.Port == "" {
		f.Port = "587"
	}
	for _, p := range []struct{ v, label string }{
		{mailsettings.ProviderCustom, "Custom"},
		{mailsettings.ProviderSES, "Amazon SES"},
		{mailsettings.ProviderGmail, "Gmail / Google Workspace"},
	} {
		f.ProviderOptions = append(f.ProviderOptions, ui.Option{Value: p.v, Label: p.label, Selected: provider == p.v})
	}
	for _, r := range mailsettings.SESRegions {
		f.RegionOptions = append(f.RegionOptions, ui.Option{Value: r, Label: r, Selected: region == r})
	}
	for _, s := range []struct{ v, label string }{
		{string(email.SecurityStarttls), "STARTTLS"},
		{string(email.SecurityTLS), "TLS (SSL)"},
		{string(email.SecurityNone), "None (localhost only)"},
	} {
		f.SecurityOptions = append(f.SecurityOptions, ui.Option{Value: s.v, Label: s.label, Selected: sec == s.v})
	}
	for _, e := range errs {
		f.Errs[e.ID] = e.Message
	}
	return f
}

// emailCheckMessage turns a failed connection check into the sentence shown
// above the form.
func emailCheckMessage(err error) string {
	var ce *email.CheckError
	if errors.As(err, &ce) {
		return ce.UserMessage() + " Nothing saved."
	}
	return "Could not check the connection. Nothing saved."
}

func (a *Admin) mountEmailSettings(owner *httpx.Group) {
	owner.HandleFunc("GET /settings/email", a.handleEmail)
	owner.HandleFunc("POST /settings/email", a.handleEmailSave)
	owner.HandleFunc("POST /settings/email/test", a.handleEmailSettingsTest)
	owner.HandleFunc("POST /settings/email/reset", a.handleEmailReset)
}

func (a *Admin) emailView(w http.ResponseWriter, r *http.Request) (EmailSettingsService, EmailSettingsView, bool) {
	svc := a.deps.EmailSettings
	if svc == nil {
		a.unavailable(w, r, "Email settings")
		return nil, EmailSettingsView{}, false
	}
	v, err := svc.Get(r.Context())
	if err != nil {
		a.logger().Error("email settings", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The email settings could not be loaded.")
		return nil, EmailSettingsView{}, false
	}
	return svc, v, true
}

func (a *Admin) handleEmail(w http.ResponseWriter, r *http.Request) {
	if _, v, ok := a.emailView(w, r); ok {
		a.renderEmail(w, r, http.StatusOK, v, emailFormEcho{}, nil, "")
	}
}

func (a *Admin) handleEmailSave(w http.ResponseWriter, r *http.Request) {
	svc, stored, ok := a.emailView(w, r)
	if !ok {
		return
	}
	res := a.submitEmail(r, svc, stored)
	switch {
	case res.fatal:
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The email settings could not be saved.")
	case !res.saved:
		a.renderEmail(w, r, http.StatusUnprocessableEntity, stored, res.echo, res.errs, res.checkFailed)
	default:
		a.SetFlash(w, r, "success", "Email settings saved. Email is set up.")
		http.Redirect(w, r, a.adminPath+emailPath, http.StatusSeeOther)
	}
}

// emailSubmission is the outcome of one SMTP form post.
type emailSubmission struct {
	echo        emailFormEcho
	errs        []ui.FieldError
	checkFailed string // the connection check refused it; nothing was saved
	saved       bool
	fatal       bool // the store failed
}

// submitEmail validates the posted SMTP form, checks the connection and, only
// when that works, saves and applies it. Shared by Settings > Email and the
// setup wizard.
func (a *Admin) submitEmail(r *http.Request, svc EmailSettingsService, stored EmailSettingsView) emailSubmission {
	ctx := r.Context()
	_ = r.ParseForm() // a malformed body leaves the form empty and fails validation
	in, echo, errs := validateEmail(r.PostForm, stored, stored.PasswordSaved)
	res := emailSubmission{echo: echo, errs: errs}
	if len(errs) > 0 {
		return res
	}
	if err := svc.Check(ctx, in, echo.KeepPassword); err != nil {
		a.logger().Warn("email settings check failed", "host", in.Host, "port", in.Port, "err", err)
		res.checkFailed = emailCheckMessage(err)
		return res
	}
	if err := svc.Save(ctx, in, echo.KeepPassword); err != nil {
		a.logger().Error("email settings save", "err", err)
		res.fatal = true
		return res
	}
	a.audit(r, "email.settings", "email", map[string]any{
		"provider": in.Provider, "host": in.Host, "port": in.Port, "security": string(in.Security),
		"username": in.Username, "from": in.FromAddress, "password_changed": in.Password != "",
	})
	res.saved = true
	return res
}

// handleEmailSettingsTest sends a test message to the signed-in owner, so a wrong SMTP
// setting shows up here instead of when a visitor submits a form. The hidden
// return field picks where to go back to: the tab (default) or the setup
// wizard's last page.
func (a *Admin) handleEmailSettingsTest(w http.ResponseWriter, r *http.Request) {
	back := a.adminPath + emailPath
	if r.PostFormValue("return") == "done" {
		back = a.adminPath + "/setup/done"
	}
	switch {
	case a.deps.EmailSettings != nil && a.emailDisabled(r):
		a.SetFlash(w, r, "warning", "Email is switched off by the server (EMAIL_DISABLED), so nothing was sent.")
	case !a.mailConfigured():
		a.SetFlash(w, r, "error", "Email is not set up, so nothing was sent.")
	default:
		u := userFrom(r.Context())
		if err := a.deps.Mailer.SendTest(r.Context(), u.Email, a.siteName(r.Context())); err != nil {
			a.logger().Error("test email failed", "err", err)
			a.SetFlash(w, r, "error", "The test email could not be sent. Check the email settings and the server log.")
		} else {
			a.SetFlash(w, r, "success", "Test email sent to "+u.Email+". Check your inbox.")
		}
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

func (a *Admin) emailDisabled(r *http.Request) bool {
	v, err := a.deps.EmailSettings.Get(r.Context())
	return err == nil && v.Disabled
}

func (a *Admin) handleEmailReset(w http.ResponseWriter, r *http.Request) {
	svc := a.deps.EmailSettings
	if svc == nil {
		a.unavailable(w, r, "Email settings")
		return
	}
	if err := svc.Reset(r.Context()); err != nil {
		a.logger().Error("email settings reset", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The saved email settings could not be removed.")
		return
	}
	a.audit(r, "email.settings.reset", "email", nil)
	a.SetFlash(w, r, "success", "Saved email settings removed. The server's SMTP_* settings apply again.")
	http.Redirect(w, r, a.adminPath+emailPath, http.StatusSeeOther)
}

func (a *Admin) renderEmail(w http.ResponseWriter, r *http.Request, status int, stored EmailSettingsView, echo emailFormEcho, errs []ui.FieldError, checkFailed string) {
	d := emailData{
		Form: newEmailFields(stored, echo, errs), Summary: errs, CheckFailed: checkFailed,
		Configured: stored.Configured, Disabled: stored.Disabled, Saved: stored.Saved, Using: emailUsing(stored.Source),
	}
	v := a.NewView(w, r, "Email")
	v.Section, v.Tab = "email", "email"
	v.Width = "compact"
	v.Data = d
	a.Render(w, r, status, "settings/email", v)
}
