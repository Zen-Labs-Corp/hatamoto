package admin

import (
	"context"
	"net/http"
	"strings"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/spam"
)

const spamPath = "/settings/spam"

// SpamService is the spam protection settings and the provider check behind
// Settings > Forms > Spam protection. The captcha secret only ever goes in
// (SealSecret) and is checked against the provider (TestSecret); it never
// comes back out.
type SpamService interface {
	Settings(ctx context.Context) (spam.Settings, error)
	Save(ctx context.Context, s spam.Settings) error
	SealSecret(secret string) ([]byte, error)
	// SecretReadable reports whether the saved secret can be opened.
	SecretReadable(s spam.Settings) bool
	// TestSecret checks the saved secret with the provider.
	TestSecret(ctx context.Context, s spam.Settings) (valid bool, detail string, err error)
}

// spamData is the Spam protection page: the saved (or, after a failed save,
// submitted) values. There is deliberately no secret field.
type spamData struct {
	Summary []ui.FieldError
	Errs    map[string]string

	ProviderOptions []ui.Option
	SiteKey         string
	SecretSaved     bool // a secret is stored and readable
	SecretLost      bool // a secret is stored but cannot be read any more
	MinSeconds      string
	MaxLinks        string
	BlockedWords    string
	Scripts         []ui.Option
	ActionOptions   []ui.Option
	// RequiredForms counts forms that require the check; shown with the
	// warning when no provider keys are usable.
	RequiredForms int
	Unconfigured  bool
	// TestOK and TestMsg are the Test connection result, if one was run.
	TestOK  bool
	TestMsg string
}

func (a *Admin) mountSpamSettings(owner *httpx.Group) {
	owner.HandleFunc("GET /settings/spam", a.handleSpam)
	owner.HandleFunc("POST /settings/spam", a.handleSpamSave)
	owner.HandleFunc("POST /settings/spam/test", a.handleSpamTest)
}

func (a *Admin) handleSpam(w http.ResponseWriter, r *http.Request) {
	svc := a.deps.Spam
	if svc == nil {
		a.unavailable(w, r, "Spam protection")
		return
	}
	stored, err := svc.Settings(r.Context())
	if err != nil {
		a.logger().Error("spam settings", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The spam protection settings could not be loaded.")
		return
	}
	a.renderSpam(w, r, http.StatusOK, stored, spamForm{}, nil)
}

func (a *Admin) handleSpamSave(w http.ResponseWriter, r *http.Request) {
	svc := a.deps.Spam
	if svc == nil {
		a.unavailable(w, r, "Spam protection")
		return
	}
	ctx := r.Context()
	stored, err := svc.Settings(ctx)
	if err != nil {
		a.logger().Error("spam settings", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The spam protection settings could not be loaded.")
		return
	}
	_ = r.ParseForm() // a malformed body leaves the form empty and fails validation
	next, in, errs := validateSpam(r.PostForm, stored, svc.SecretReadable(stored))
	if len(errs) == 0 && in.Secret != "" {
		sealed, err := svc.SealSecret(in.Secret)
		if err != nil {
			a.logger().Error("spam secret seal")
			a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The spam protection settings could not be saved.")
			return
		}
		next.SecretSealed = sealed
	}
	if len(errs) > 0 {
		a.renderSpam(w, r, http.StatusUnprocessableEntity, stored, in, errs)
		return
	}
	if err := svc.Save(ctx, next); err != nil {
		a.logger().Error("spam settings save", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The spam protection settings could not be saved.")
		return
	}
	a.audit(r, "spam.settings", "spam", map[string]any{
		"old": spamDetail(stored), "new": spamDetail(next), "secret_changed": in.Secret != "",
	})
	// The provider and site key are baked into cached pages and their CSP.
	if a.deps.Invalidate != nil {
		a.deps.Invalidate(ctx)
	}
	a.SetFlash(w, r, "success", "Spam protection saved.")
	http.Redirect(w, r, a.adminPath+spamPath, http.StatusSeeOther)
}

// handleSpamTest checks the keys as typed on the page with the provider, so an
// owner can test before saving. A blank secret field means the saved secret,
// which only applies while the provider is unchanged. Nothing is saved. The
// page is re-rendered with the typed values and the result; the Test
// connection button swaps just the result region in (htmx), so the typed
// secret stays in its field.
func (a *Admin) handleSpamTest(w http.ResponseWriter, r *http.Request) {
	svc := a.deps.Spam
	if svc == nil {
		a.unavailable(w, r, "Spam protection")
		return
	}
	stored, err := svc.Settings(r.Context())
	if err != nil {
		a.logger().Error("spam settings", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The spam protection settings could not be loaded.")
		return
	}
	_ = r.ParseForm()
	// A post without the settings fields tests what is saved.
	if _, ok := r.PostForm["provider"]; !ok {
		r.PostForm.Set("provider", stored.Provider)
		r.PostForm.Set("site_key", stored.SiteKey)
	}
	_, in, _ := validateSpam(r.PostForm, stored, svc.SecretReadable(stored))
	cand := stored
	cand.Provider = r.PostForm.Get("provider")
	cand.SiteKey = strings.TrimSpace(r.PostForm.Get("site_key"))

	res := spamTestResult{}
	switch {
	case !contains(spam.Providers, cand.Provider) || cand.Provider == spam.ProviderNone:
		res.Msg = "Choose a captcha provider to test."
	case cand.SiteKey == "":
		res.Msg = "Enter the site key to test."
	case in.Secret != "":
		sealed, err := svc.SealSecret(in.Secret)
		if err != nil {
			a.logger().Error("spam secret seal")
			res.Msg = "The secret key could not be checked. Try again."
			break
		}
		cand.SecretSealed = sealed
		res = a.runSpamTest(r, cand)
	case cand.Provider != stored.Provider || !svc.SecretReadable(stored):
		res.Msg = "Enter the secret key to test."
	default:
		res = a.runSpamTest(r, cand)
	}
	a.renderSpamResult(w, r, stored, in, res)
}

// spamTestResult is the Test connection outcome shown under the button.
type spamTestResult struct {
	OK  bool
	Msg string
}

func (a *Admin) runSpamTest(r *http.Request, s spam.Settings) spamTestResult {
	valid, detail, err := a.deps.Spam.TestSecret(r.Context(), s)
	switch {
	case err != nil:
		a.logger().Warn("spam test connection failed", "provider", s.Provider)
		return spamTestResult{Msg: "The provider could not be reached. Try again in a moment."}
	case valid:
		return spamTestResult{OK: true, Msg: strings.TrimSpace("Connection works. " + detail + " Click Save to keep these keys.")}
	default:
		return spamTestResult{Msg: detail}
	}
}

func (a *Admin) renderSpam(w http.ResponseWriter, r *http.Request, status int, stored spam.Settings, in spamForm, errs []ui.FieldError) {
	a.renderSpamPage(w, r, status, stored, in, errs, spamTestResult{})
}

// renderSpamResult shows the page with the typed values and a test result.
// Always 200: the result is information, not a failed save, and htmx only
// swaps successful responses.
func (a *Admin) renderSpamResult(w http.ResponseWriter, r *http.Request, stored spam.Settings, in spamForm, res spamTestResult) {
	a.renderSpamPage(w, r, http.StatusOK, stored, in, nil, res)
}

func (a *Admin) renderSpamPage(w http.ResponseWriter, r *http.Request, status int, stored spam.Settings, in spamForm, errs []ui.FieldError, res spamTestResult) {
	svc := a.deps.Spam
	// After a failed save show what was submitted; otherwise what is stored.
	shown := stored
	if in.Posted {
		shown = in.Settings
	}
	readable := svc.SecretReadable(stored)
	d := spamData{
		Summary: errs, Errs: map[string]string{},
		SiteKey:      shown.SiteKey,
		SecretSaved:  readable,
		SecretLost:   len(stored.SecretSealed) > 0 && !readable,
		MinSeconds:   in.orInt(shown.MinSeconds, in.MinSeconds),
		MaxLinks:     in.orInt(shown.MaxLinks, in.MaxLinks),
		BlockedWords: strings.Join(shown.BlockedWords, ", "),
		TestOK:       res.OK,
		TestMsg:      res.Msg,
	}
	if in.Posted {
		d.BlockedWords = in.Words
	}
	for _, p := range []struct{ v, label string }{
		{spam.ProviderNone, "None"},
		{spam.ProviderTurnstile, "Cloudflare Turnstile"},
		{spam.ProviderRecaptcha, "Google reCAPTCHA v3"},
		{spam.ProviderHCaptcha, "hCaptcha"},
	} {
		d.ProviderOptions = append(d.ProviderOptions, ui.Option{Value: p.v, Label: p.label, Selected: shown.Provider == p.v})
	}
	for _, s := range []struct{ v, label string }{
		{"cyrillic", "Cyrillic"}, {"cjk", "CJK"}, {"arabic", "Arabic"},
		{"hebrew", "Hebrew"}, {"thai", "Thai"}, {"devanagari", "Devanagari"},
	} {
		d.Scripts = append(d.Scripts, ui.Option{Value: s.v, Label: s.label, Selected: contains(shown.BlockedScripts, s.v)})
	}
	d.ActionOptions = []ui.Option{
		{Value: spam.ActionSpam, Label: "Save as Spam, no notification", Selected: shown.Action != spam.ActionRefuse},
		{Value: spam.ActionRefuse, Label: "Refuse submission", Selected: shown.Action == spam.ActionRefuse},
	}
	for _, e := range errs {
		d.Errs[e.ID] = e.Message
	}
	d.RequiredForms = a.captchaRequiredForms(r.Context())
	d.Unconfigured = d.RequiredForms > 0 && (stored.Provider == spam.ProviderNone || !readable)

	v := a.NewView(w, r, "Spam protection")
	v.Section, v.Tab = "forms", "spam"
	v.Width = "narrow"
	v.Data = d
	a.Render(w, r, status, "settings/spam", v)
}

// captchaUnconfigured reports that no captcha provider keys are usable. It is
// false when the spam service is not wired, since nothing is known then.
func (a *Admin) captchaUnconfigured(ctx context.Context) bool {
	svc := a.deps.Spam
	if svc == nil {
		return false
	}
	set, err := svc.Settings(ctx)
	if err != nil {
		return false
	}
	return set.Provider == spam.ProviderNone || !svc.SecretReadable(set)
}

// captchaRequiredForms counts the forms whose "Require the spam check" switch is on.
func (a *Admin) captchaRequiredForms(ctx context.Context) int {
	if a.deps.Growth.Forms == nil {
		return 0
	}
	forms, err := a.deps.Growth.Forms.ListForms(ctx, "")
	if err != nil {
		return 0
	}
	n := 0
	for _, f := range forms {
		if f.Settings.EnableCaptcha {
			n++
		}
	}
	return n
}

// spamDetail is the audit record of the settings: never the secret.
func spamDetail(s spam.Settings) map[string]any {
	return map[string]any{
		"provider": s.Provider, "site_key": s.SiteKey, "min_seconds": s.MinSeconds,
		"max_links": s.MaxLinks, "blocked_words": len(s.BlockedWords),
		"blocked_scripts": s.BlockedScripts, "action": s.Action,
	}
}
