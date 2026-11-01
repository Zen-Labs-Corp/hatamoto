package admin

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/admin/a11y"
	"hatamoto.dev/core/internal/admin/starter"
	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/email"
	"hatamoto.dev/core/internal/user"
)

type fakeOwners struct {
	mu   sync.Mutex
	made int
	site *fakeFinderCount
}

type fakeFinderCount struct {
	mu sync.Mutex
	n  int
}

func (f *fakeFinderCount) GetByEmail(context.Context, string) (*user.User, error) {
	return nil, user.ErrUserNotFound
}
func (f *fakeFinderCount) Count(context.Context) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.n, nil
}

func (o *fakeOwners) CreateFirstOwner(_ context.Context, name, email, pw string) (*user.User, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.site.n > 0 {
		return nil, auth.ErrSetupDone
	}
	o.site.mu.Lock()
	o.site.n++
	o.site.mu.Unlock()
	o.made++
	return &user.User{ID: uuid.New(), Email: email, Role: user.RoleOwner, Name: &name}, nil
}

type fakeStarter struct {
	calls int
	opts  starter.Options
	fail  bool
}

func (f *fakeStarter) Install(_ context.Context, o starter.Options) (starter.Result, error) {
	f.calls++
	f.opts = o
	if f.fail {
		return starter.Result{}, http.ErrAbortHandler
	}
	return starter.Result{Pages: 3, Posts: 2, Menus: 2, MenuItems: 6, Forms: 1}, nil
}

type setupRig struct {
	*authRig
	owners  *fakeOwners
	starter *fakeStarter
	site    *fakeSite
	count   *fakeFinderCount
}

// newSetupRig is a fresh install: no users, setup not completed, code "ABCD-2345".
func newSetupRig(t *testing.T) *setupRig {
	t.Helper()
	g := newAuthRig(t, true)
	count := &fakeFinderCount{}
	site := newFakeSite("My Hatamoto site")
	site.cfg.SetupCompletedAt = nil
	fs := &fakeStarter{}
	owners := &fakeOwners{site: count}
	g.a.deps.Finder = count
	g.a.deps.Site = site
	g.a.deps.Owners = owners
	g.a.deps.Starter = fs
	g.a.deps.EmailSettings = &fakeEmailSettings{view: EmailSettingsView{Source: EmailSourceNone}}
	g.a.deps.SetupCode = SetupCodeFrom("ABCD-2345")
	g.a.deps.Address = &fakeAddress{state: AddressState{Path: "/k7qm-x2rf-9tdc"}, next: "/n3xt-r4nd-8wpz"}
	g.a.deps.NeedsSetup = func(context.Context) bool { n, _ := count.Count(context.Background()); return n == 0 }
	return &setupRig{authRig: g, owners: owners, starter: fs, site: site, count: count}
}

func TestSetupCodeNormalisation(t *testing.T) {
	t.Parallel()

	c := SetupCodeFrom("ABCD-2345")
	for in, want := range map[string]bool{"ABCD-2345": true, "abcd2345": true, " abcd 2345 ": true, "ABCD-2346": false, "": false, "ABCD": false} {
		if c.Verify(in) != want {
			t.Errorf("Verify(%q) = %v", in, !want)
		}
	}
	var nilCode *SetupCode
	if nilCode.Verify("anything") || SetupCodeFrom("").Verify("") {
		t.Error("an absent or empty code must never verify")
	}
	gen, display := NewSetupCode()
	if len(display) != 9 || !gen.Verify(display) || !gen.Verify(strings.ToLower(display)) {
		t.Errorf("generated code %q", display)
	}
}

// The setup code may arrive in the URL fragment (never sent to the server); the page loads an
// SRI-covered external module for it and must not echo a ?code= query value.
func TestSetupPagePrefillModuleAndNoQueryEcho(t *testing.T) {
	t.Parallel()

	g := newSetupRig(t)
	g.count.n = 0
	body := g.do("GET", "/admin/setup", "", nil, nil).Body.String()
	if !regexp.MustCompile(`<script type="module" nonce="[^"]+" src="[^"]*/setupcode\.[0-9a-f]+\.js" integrity="sha256-[^"]+"`).MatchString(body) {
		t.Errorf("setup page must load setupcode.js as an SRI module:\n%s", body)
	}
	if !strings.Contains(body, `id="code"`) {
		t.Error("setup page must have the code field")
	}
	for _, m := range regexp.MustCompile(`<script[^>]*>`).FindAllString(body, -1) {
		if !strings.Contains(m, "nonce=") {
			t.Errorf("script tag without nonce: %s", m)
		}
		if !strings.Contains(m, "src=") && !strings.Contains(m, "importmap") {
			t.Errorf("unexpected inline script: %s", m)
		}
	}
	rec := g.do("GET", "/admin/setup?code=ZZZZ-ZZZZ-ZZZZ", "", nil, nil)
	if rec.Code != 200 || strings.Contains(rec.Body.String(), "ZZZZ") {
		t.Errorf("a ?code= query must not be echoed (status %d)", rec.Code)
	}
}

func TestSetupWizardPagesAccessibleAndGolden(t *testing.T) {
	t.Parallel()

	g := newSetupRig(t)
	g.count.n = 0
	pages := map[string]string{}
	rec := g.do("GET", "/admin/setup", "", nil, nil)
	pages["setup-account"] = rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("account: %d", rec.Code)
	}
	g.count.n = 1 // after the owner exists the remaining steps are for the signed-in owner
	g.a.sudo.store.Grant(g.sessions.byToken["owner-token"].ID)
	for name, path := range map[string]string{"setup-admin-address": "/admin/setup/admin-address", "setup-site": "/admin/setup/site", "setup-email": "/admin/setup/email", "setup-theme": "/admin/setup/theme", "setup-content": "/admin/setup/content", "setup-done": "/admin/setup/done"} {
		rec := g.do("GET", path, "owner-token", nil, nil)
		if rec.Code != 200 {
			t.Fatalf("%s: %d\n%s", name, rec.Code, rec.Body.String())
		}
		pages[name] = rec.Body.String()
	}
	// the Stats step exists only with the install stats service
	g.a.deps.Stats = &fakeStats{view: StatsView{Consent: "unset", PreviewJSON: statsTestPreview}}
	rec = g.do("GET", "/admin/setup/stats", "owner-token", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("setup-stats: %d", rec.Code)
	}
	pages["setup-stats"] = rec.Body.String()
	g.a.deps.Stats = nil
	// error states
	g.count.n = 0
	rec = g.do("POST", "/admin/setup", "", url.Values{"code": {"nope"}, "email": {"bad"}, "password": {"short"}}, nil)
	if rec.Code != 422 {
		t.Fatalf("errors: %d", rec.Code)
	}
	pages["setup-account-errors"] = rec.Body.String()
	g.count.n = 1
	rec = g.do("POST", "/admin/setup/site", "owner-token", url.Values{"name": {""}, "url": {"ftp://x/y"}, "language": {"zz-nope-xx"}, "timezone": {"Mars/Base"}}, nil)
	if rec.Code != 422 {
		t.Fatalf("site errors: %d", rec.Code)
	}
	pages["setup-site-errors"] = rec.Body.String()

	for name, body := range pages {
		if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
			t.Errorf("%s a11y: %s", name, strings.Join(issues, "; "))
		}
		compareGolden(t, "pages/"+name+".html", normalizePage(body))
	}
	if !strings.Contains(pages["setup-account-errors"], "There are 5 problems") || !strings.Contains(pages["setup-site-errors"], "Enter a site name") {
		t.Error("error summaries missing")
	}
	if !strings.Contains(pages["setup-account"], `aria-label="Setup progress"`) || !strings.Contains(pages["setup-site"], `aria-current="step"`) {
		t.Error("step indicator missing")
	}
}

func TestSetupRequiresCodeAndIsOneShot(t *testing.T) {
	t.Parallel()

	g := newSetupRig(t)
	good := func() url.Values {
		return url.Values{"code": {"abcd 2345"}, "name": {"Olga"}, "email": {"o@example.com"}, "password": {"a-long-enough-pw-1"}, "confirm": {"a-long-enough-pw-1"}}
	}
	bad := good()
	bad.Set("code", "ZZZZ-9999")
	if rec := g.do("POST", "/admin/setup", "", bad, nil); rec.Code != 422 || g.owners.made != 0 {
		t.Fatalf("wrong code: %d made=%d", rec.Code, g.owners.made)
	}
	mismatch := good()
	mismatch.Set("confirm", "different-password-12")
	if rec := g.do("POST", "/admin/setup", "", mismatch, nil); rec.Code != 422 || g.owners.made != 0 {
		t.Fatalf("mismatch: %d", rec.Code)
	}
	rec := g.do("POST", "/admin/setup", "", good(), nil)
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/setup/admin-address" || g.owners.made != 1 {
		t.Fatalf("create: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	signed := false
	for _, c := range rec.Result().Cookies() {
		if c.Name == "hatamoto_session" && c.Value != "" {
			signed = true
		}
	}
	if !signed {
		t.Error("the new owner must be signed in")
	}
	// A second attempt, with the right code, is refused.
	if rec := g.do("POST", "/admin/setup", "", good(), nil); rec.Code != 404 || g.owners.made != 1 {
		t.Fatalf("second owner: %d made=%d", rec.Code, g.owners.made)
	}
	// Without the code configured the first step cannot succeed.
	g2 := newSetupRig(t)
	g2.a.deps.SetupCode = nil
	if rec := g2.do("POST", "/admin/setup", "", good(), nil); rec.Code != 422 || g2.owners.made != 0 {
		t.Fatalf("no code configured: %d", rec.Code)
	}
}

func TestSetupStepsAreOwnerOnlyAndCloseAfterCompletion(t *testing.T) {
	t.Parallel()

	g := newSetupRig(t)
	g.count.n = 1
	if rec := g.do("GET", "/admin/setup/site", "", nil, nil); rec.Code != 303 || !strings.HasPrefix(rec.Header().Get("Location"), "/admin/login") {
		t.Errorf("anonymous: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if rec := g.do("GET", "/admin/setup/site", "editor-token", nil, nil); rec.Code != 403 {
		t.Errorf("editor: %d", rec.Code)
	}
	if rec := g.do("GET", "/admin/setup", "", nil, nil); rec.Code != 303 || rec.Header().Get("Location") != "/admin/login" {
		t.Errorf("anonymous after the owner exists: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if rec := g.do("GET", "/admin/setup", "owner-token", nil, nil); rec.Code != 303 || rec.Header().Get("Location") != "/admin/setup/admin-address" {
		t.Errorf("owner resumes: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	// Finish; every step now 404s, except the done page for the owner.
	if rec := g.do("POST", "/admin/setup/content", "owner-token", url.Values{"content": {"empty"}}, nil); rec.Code != 303 || rec.Header().Get("Location") != "/admin/setup/done" {
		t.Fatalf("finish: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if g.starter.calls != 0 {
		t.Error("starter ran for 'empty'")
	}
	for _, p := range []string{"/admin/setup", "/admin/setup/site", "/admin/setup/email", "/admin/setup/theme", "/admin/setup/content"} {
		if rec := g.do("GET", p, "owner-token", nil, nil); rec.Code != 404 {
			t.Errorf("%s after completion: %d", p, rec.Code)
		}
	}
	if rec := g.do("POST", "/admin/setup/site", "owner-token", url.Values{"name": {"x"}}, nil); rec.Code != 404 {
		t.Errorf("POST after completion: %d", rec.Code)
	}
	if rec := g.do("GET", "/admin/setup/done", "owner-token", nil, nil); rec.Code != 200 {
		t.Errorf("done: %d", rec.Code)
	}
}

func TestSetupSiteAndThemeAndStarterSteps(t *testing.T) {
	t.Parallel()

	flushes := 0
	g := newSetupRig(t)
	g.a.deps.Invalidate = func(context.Context) { flushes++ }
	g.count.n = 1
	rec := g.do("POST", "/admin/setup/site", "owner-token", url.Values{"name": {"  Acme  "}, "url": {"https://acme.example/"}, "language": {"en_au"}, "timezone": {"Australia/Sydney"}}, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/setup/email" {
		t.Fatalf("site: %d %s\n%s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	if flushes != 1 {
		t.Errorf("site step must flush the render cache, flushes=%d", flushes)
	}
	if c := g.site.cfg; c.CompanyName != "Acme" || c.PublicBaseURL != "https://acme.example" || c.Language != "en-AU" || c.Timezone != "Australia/Sydney" {
		t.Errorf("site not stored normalised: %+v", c)
	}
	for _, bad := range []string{"javascript:alert(1)", "red;}", "#12", ""} {
		if rec := g.do("POST", "/admin/setup/theme", "owner-token", url.Values{"colour": {bad}}, nil); rec.Code != 422 {
			t.Errorf("colour %q accepted: %d", bad, rec.Code)
		}
	}
	if rec := g.do("POST", "/admin/setup/theme", "owner-token", url.Values{"colour": {"#aa3300"}}, nil); rec.Code != 303 || g.site.cfg.PrimaryColor != "#aa3300" {
		t.Errorf("colour: %d %q", rec.Code, g.site.cfg.PrimaryColor)
	}
	if flushes != 2 {
		t.Errorf("colour step must flush the render cache, flushes=%d", flushes)
	}
	if rec := g.do("POST", "/admin/setup/content", "owner-token", url.Values{"content": {"bogus"}}, nil); rec.Code != 422 {
		t.Errorf("bad choice: %d", rec.Code)
	}
	g.starter.fail = true
	if rec := g.do("POST", "/admin/setup/content", "owner-token", url.Values{"content": {"starter"}}, nil); rec.Code != 500 || !strings.Contains(rec.Body.String(), "safe to try again") || g.site.cfg.SetupCompletedAt != nil {
		t.Errorf("failed starter must not complete setup: %d", rec.Code)
	}
	g.starter.fail = false
	rec = g.do("POST", "/admin/setup/content", "owner-token", url.Values{"content": {"starter"}}, nil)
	if rec.Code != 303 || g.site.cfg.SetupCompletedAt == nil || g.starter.opts.SiteName != "Acme" || g.starter.opts.NotifyEmail != "owner@example.com" {
		t.Errorf("starter: %d %+v", rec.Code, g.starter.opts)
	}
}

func TestSiteFormValidation(t *testing.T) {
	t.Parallel()

	ok := siteForm{Name: " Acme ", URL: "https://acme.example/", Language: "pt_br", Timezone: "America/Sao_Paulo"}
	if errs := ok.validate(); len(errs) != 0 || ok.Name != "Acme" || ok.URL != "https://acme.example" || ok.Language != "pt-BR" {
		t.Errorf("valid form: %v %+v", errs, ok)
	}
	for name, f := range map[string]siteForm{
		"name":     {Name: "", Language: "en", Timezone: "UTC"},
		"url":      {Name: "x", URL: "https://a.example/path", Language: "en", Timezone: "UTC"},
		"language": {Name: "x", Language: "!!", Timezone: "UTC"},
		"timezone": {Name: "x", Language: "en", Timezone: "Nowhere/Land"},
	} {
		if errs := f.validate(); errs[name] == "" {
			t.Errorf("%s not rejected: %v", name, errs)
		}
	}
	long := siteForm{Name: strings.Repeat("x", 121), Language: "en", Timezone: "UTC"}
	if errs := long.validate(); errs["name"] == "" {
		t.Error("over-long name accepted")
	}
}

func TestLanguageAndTimezoneOptions(t *testing.T) {
	t.Parallel()

	langs := languageOptions("pt-BR")
	if langs[0].Value != "pt-BR" || !langs[0].Selected {
		t.Errorf("an unlisted current language must be kept first: %+v", langs[0])
	}
	var de bool
	for _, o := range languageOptions("en") {
		if o.Value == "en" && !o.Selected {
			t.Error("current language not selected")
		}
		de = de || strings.Contains(o.Label, "Deutsch")
	}
	if !de || len(languages) != 28 {
		t.Errorf("language list: %d entries, german=%v", len(languages), de)
	}
	for _, z := range timezones {
		if f := (siteForm{Name: "x", Language: "en", Timezone: z}); f.validate()["timezone"] != "" {
			t.Errorf("offered time zone %q is not valid on this system", z)
		}
	}
}

func TestSetupEmailStep(t *testing.T) {
	t.Parallel()

	g := newSetupRig(t)
	g.count.n = 1
	svc := g.a.deps.EmailSettings.(*fakeEmailSettings)
	// The step shows the fields, a Skip link to Look and no password.
	body := g.do("GET", "/admin/setup/email", "owner-token", nil, nil).Body.String()
	for _, want := range []string{"Email", "Amazon SES", `href="/admin/setup/theme">Skip</a>`, `name="password"`, `aria-current="step"`} {
		if !strings.Contains(body, want) {
			t.Errorf("email step lacks %q", want)
		}
	}
	if strings.Contains(body, "SMTP_* settings instead") {
		t.Error("the wizard must not offer the server's-settings reset")
	}
	// Skip is just a link: nothing is stored.
	if len(svc.saved) != 0 {
		t.Error("something saved before any POST")
	}
	// A refused connection check keeps you on the step, with the SMTP error and no password.
	svc.checkErr = &email.CheckError{Stage: "auth", Code: 535, Msg: "Authentication failed", Host: "smtp.example.com", Port: 587}
	rec := g.do("POST", "/admin/setup/email", "owner-token", goodEmailForm(), nil)
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), "535 Authentication failed") || strings.Contains(rec.Body.String(), emailTestPassword) || len(svc.saved) != 0 {
		t.Fatalf("failed check: %d saved=%d", rec.Code, len(svc.saved))
	}
	if issues := a11y.Check(rec.Body.String(), a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
	// Validation errors re-render the step.
	bad := goodEmailForm()
	bad.Set("from", "")
	if rec := g.do("POST", "/admin/setup/email", "owner-token", bad, nil); rec.Code != 422 || !strings.Contains(rec.Body.String(), "Enter the address mail is sent from") {
		t.Errorf("validation: %d", rec.Code)
	}
	// A good save moves on to Look.
	svc.checkErr = nil
	rec = g.do("POST", "/admin/setup/email", "owner-token", goodEmailForm(), nil)
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/setup/theme" || len(svc.saved) != 1 || svc.saved[0].Password != emailTestPassword {
		t.Fatalf("save: %d %s saved=%d", rec.Code, rec.Header().Get("Location"), len(svc.saved))
	}
	if rec := g.do("GET", "/admin/setup/email", "editor-token", nil, nil); rec.Code != 403 {
		t.Errorf("editor: %d", rec.Code)
	}
}

func TestSetupEmailStepWithoutServiceIsSkipped(t *testing.T) {
	t.Parallel()

	g := newSetupRig(t)
	g.count.n = 1
	g.a.deps.EmailSettings = nil
	for _, m := range []string{"GET", "POST"} {
		if rec := g.do(m, "/admin/setup/email", "owner-token", url.Values{}, nil); rec.Code != 303 || rec.Header().Get("Location") != "/admin/setup/theme" {
			t.Errorf("%s without service: %d %s", m, rec.Code, rec.Header().Get("Location"))
		}
	}
}

// The Look step lists installed themes as radio cards and activates the chosen one; an unlisted
// or blocked theme is refused with a message, never a 500.
func TestSetupThemeStepActivatesTheChosenTheme(t *testing.T) {
	t.Parallel()

	g := newSetupRig(t)
	g.count.n = 1
	pk := &fakePackages{themes: []ThemeInfo{
		{Name: "default", Builtin: true, Active: true},
		{Name: "sunrise", Description: "Warm and serif."},
		{Name: "shady", Blocked: "unsigned"},
	}}
	g.a.deps.Growth.Packages = pk
	body := g.do("GET", "/admin/setup/theme", "owner-token", nil, nil).Body.String()
	for _, want := range []string{`value="default" checked`, `value="sunrise"`, "Warm and serif.", `href="/admin/setup/email"`, ">Back<"} {
		if !strings.Contains(body, want) {
			t.Errorf("theme step missing %q", want)
		}
	}
	if strings.Contains(body, "shady") {
		t.Error("a blocked theme must not be offered")
	}
	if rec := g.do("POST", "/admin/setup/theme", "owner-token", url.Values{"colour": {"#aa3300"}, "theme": {"shady"}}, nil); rec.Code != 422 || pk.activated != "" {
		t.Errorf("a blocked theme: %d, activated %q", rec.Code, pk.activated)
	}
	if rec := g.do("POST", "/admin/setup/theme", "owner-token", url.Values{"colour": {"#aa3300"}, "theme": {"default"}}, nil); rec.Code != 303 || pk.activated != "" {
		t.Errorf("the active theme needs no switch: %d, activated %q", rec.Code, pk.activated)
	}
	if rec := g.do("POST", "/admin/setup/theme", "owner-token", url.Values{"colour": {"#aa3300"}, "theme": {"sunrise"}}, nil); rec.Code != 303 || pk.activated != "sunrise" {
		t.Errorf("sunrise: %d, activated %q", rec.Code, pk.activated)
	}
}

// A theme that cannot be switched on re-shows the step (422) with the choice still checked and
// an inline error; the owner's colour is already saved, so nothing is lost by trying again.
func TestSetupThemeActivateFailureReshowsTheStep(t *testing.T) {
	t.Parallel()

	g := newSetupRig(t)
	g.count.n = 1
	g.a.deps.Growth.Packages = &fakePackages{
		themes:      []ThemeInfo{{Name: "default", Builtin: true, Active: true}, {Name: "sunrise"}},
		activateErr: errors.New("boom"),
	}
	rec := g.do("POST", "/admin/setup/theme", "owner-token", url.Values{"colour": {"#aa3300"}, "theme": {"sunrise"}}, nil)
	body := rec.Body.String()
	if rec.Code != 422 {
		t.Fatalf("a failed switch: %d, want 422", rec.Code)
	}
	for _, want := range []string{"That theme could not be switched on", `href="#theme-0"`, `value="sunrise" checked`} {
		if !strings.Contains(body, want) {
			t.Errorf("failed switch: missing %q", want)
		}
	}
	if g.site.cfg.PrimaryColor != "#aa3300" {
		t.Errorf("the colour is saved before the theme switch, got %q", g.site.cfg.PrimaryColor)
	}
}

// Back from Look goes to Site when the build has no Email step.
func TestSetupThemeBackWithoutEmailSettings(t *testing.T) {
	t.Parallel()

	g := newSetupRig(t)
	g.count.n = 1
	g.a.deps.EmailSettings = nil
	body := g.do("GET", "/admin/setup/theme", "owner-token", nil, nil).Body.String()
	if !strings.Contains(body, `href="/admin/setup/site"><span>Back</span>`) || strings.Contains(body, `href="/admin/setup/email"`) {
		t.Errorf("Back must go to /admin/setup/site:\n%s", body)
	}
}

// Without a package manager the Look step shows the built-in theme and points at Settings.
func TestSetupThemeStepWithoutPackages(t *testing.T) {
	t.Parallel()

	g := newSetupRig(t)
	g.count.n = 1
	body := g.do("GET", "/admin/setup/theme", "owner-token", nil, nil).Body.String()
	if !strings.Contains(body, `value="default" checked`) || !strings.Contains(body, "More themes are in") {
		t.Errorf("built-in theme card and the Settings pointer expected:\n%s", body)
	}
}

// Every step after the Site step has a Back link before its primary button.
func TestSetupStepsHaveBack(t *testing.T) {
	t.Parallel()

	g := newSetupRig(t)
	g.count.n = 1
	g.a.deps.Stats = &fakeStats{view: StatsView{Consent: "unset", PreviewJSON: statsTestPreview}}
	for path, back := range map[string]string{"/admin/setup/site": "/admin/setup/admin-address", "/admin/setup/email": "/admin/setup/site", "/admin/setup/theme": "/admin/setup/email", "/admin/setup/stats": "/admin/setup/theme", "/admin/setup/content": "/admin/setup/stats"} {
		body := g.do("GET", path, "owner-token", nil, nil).Body.String()
		i, j := strings.Index(body, `href="`+back+`"><span>Back</span>`), strings.Index(body, "hm-btn--primary")
		if i < 0 || j < i {
			t.Errorf("%s: Back to %s must come before the primary button", path, back)
		}
	}
}

// With no setup code the account form is disabled and a warning callout carries the command.
func TestSetupAccountWithoutCodeIsDisabled(t *testing.T) {
	t.Parallel()

	g := newSetupRig(t)
	g.a.deps.SetupCode = nil
	body := g.do("GET", "/admin/setup", "", nil, nil).Body.String()
	for _, want := range []string{"hm-callout--warning", "No setup code is active", `value="hatamoto user create"`, `data-hm-copy-field="setup-command"`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if n := strings.Count(body, " disabled"); n < 6 { // five fields and the submit
		t.Errorf("fields and submit must be disabled, got %d:\n%s", n, body)
	}
}

// The Done step shows a success icon and four link cards.
func TestSetupDoneHasLinkCards(t *testing.T) {
	t.Parallel()

	g := newSetupRig(t)
	g.count.n = 1
	body := g.do("GET", "/admin/setup/done", "owner-token", nil, nil).Body.String()
	if strings.Contains(body, "hm-setup-done__icon") || strings.Count(body, `class="hm-setup-link"`) != 4 {
		t.Errorf("four link cards and no success icon expected:\n%s", body)
	}
}
