package admin

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"hatamoto.dev/core/internal/admin/starter"
	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/middleware"
	"hatamoto.dev/core/internal/user"
)

var setupSteps = []string{"Your account", "Admin address", "Your site", "Email", "Look", "Stats", "Content", "Done"}

func steps(current int) []ui.Step {
	out := make([]ui.Step, len(setupSteps))
	for i, l := range setupSteps {
		out[i] = ui.Step{Label: l, Current: i == current, Done: i < current}
	}
	return out
}

type setupData struct {
	Steps  []ui.Step
	Errors []ui.FieldError
	// account
	Name, Email string
	Code        string // typed setup code, kept across a validation error
	CodeErr     string
	NameErr     string
	EmailErr    string
	PassErr     string
	MatchErr    string
	CodeHint    bool // a code exists (otherwise: restart the server)
	// site
	Site      siteForm
	Detected  string // origin the wizard is open on (hint only)
	SiteErrs  map[string]string
	Languages []ui.Option
	Timezones []ui.Option
	// email
	MailForm   emailFields
	EmailCheck string // the connection check refused the form
	// look
	Colour, ColourErr string
	ThemeItems        []ui.RadioCardItem
	// ContentItems are the three content choices as radio cards.
	ContentItems []ui.RadioCardItem
	// stats
	StatsJSON   string // the exact request body shown before the owner chooses
	StatsNeeded bool   // the form was posted without choosing Yes or No
	// Back is the previous step's address; empty on the first steps.
	Back string
	// content
	Choice string
	Failed string
	// admin address
	Addr         *addressForm
	AddrLockText string // set when the address cannot be changed here
	// done
	Home    string
	Mail    bool
	Address string // the full admin address, also shown read-only when locked
	Emailed bool
	Links   []doneLink
}

// doneLink is one card on the Done step.
type doneLink struct{ Icon, Title, Href string }

func (a *Admin) mountSetup(public, authed *httpx.Group) {
	public.HandleFunc("GET /setup", a.handleSetupStart)
	public.HandleFunc("POST /setup", a.handleSetupAccount)

	owner := authed.Group("", a.RequireRole("owner"), a.setupOpen)
	// The step needs a recent password confirmation, as the Security page does.
	// The prompt comes on the GET so it returns here (no Referer is ever sent
	// from an admin page). The save asks for it only when it changes something.
	sudo := owner.Group("", a.RequireSudo)
	sudo.HandleFunc("GET "+setupAddressPath, a.handleSetupAddress)
	owner.HandleFunc("POST "+setupAddressPath, a.handleSetupAddressSave)
	sudo.HandleFunc("POST "+setupAddressPath+"/regenerate", a.handleSetupAddressRegen)
	owner.HandleFunc("GET "+setupAddressPath+"/strength", a.handleSetupAddressStrength)
	owner.HandleFunc("GET /setup/site", a.handleSetupSiteForm)
	owner.HandleFunc("POST /setup/site", a.handleSetupSite)
	owner.HandleFunc("GET /setup/email", a.handleSetupEmailForm)
	owner.HandleFunc("POST /setup/email", a.handleSetupEmail)
	owner.HandleFunc("GET /setup/theme", a.handleSetupThemeForm)
	owner.HandleFunc("POST /setup/theme", a.handleSetupTheme)
	owner.HandleFunc("GET /setup/stats", a.handleSetupStatsForm)
	owner.HandleFunc("POST /setup/stats", a.handleSetupStats)
	owner.HandleFunc("GET /setup/content", a.handleSetupContentForm)
	owner.HandleFunc("POST /setup/content", a.handleSetupContent)
	authed.Group("", a.RequireRole("owner")).HandleFunc("GET /setup/done", a.handleSetupDone)
}

// setupCompleted reports a finished wizard (or no site config to ask).
func (a *Admin) setupCompleted(r *http.Request) bool {
	if a.deps.Site == nil {
		return true
	}
	cfg, err := a.deps.Site.Get(r.Context())
	return err != nil || cfg.SetupCompletedAt != nil
}

// setupOpen 404s the wizard once it is finished.
func (a *Admin) setupOpen(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.setupCompleted(r) {
			a.Error(w, r, http.StatusNotFound, "Page not found", "Setup is already finished.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// freshInstall reports an empty users table.
func (a *Admin) freshInstall(r *http.Request) bool {
	if a.deps.Finder == nil {
		return false
	}
	n, err := a.deps.Finder.Count(r.Context())
	return err == nil && n == 0
}

func (a *Admin) handleSetupStart(w http.ResponseWriter, r *http.Request) {
	if a.setupCompleted(r) {
		a.Error(w, r, http.StatusNotFound, "Page not found", "Setup is already finished.")
		return
	}
	if !a.freshInstall(r) {
		if _, _, ok := a.authenticate(r); ok {
			http.Redirect(w, r, a.adminPath+setupAddressPath, http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, a.adminPath+"/login", http.StatusSeeOther)
		return
	}
	a.renderAccount(w, r, http.StatusOK, setupData{})
}

func (a *Admin) renderAccount(w http.ResponseWriter, r *http.Request, status int, d setupData) {
	d.Steps = steps(0)
	d.CodeHint = a.deps.SetupCode != nil
	v := a.NewView(w, r, "Set up your site")
	v.SiteName = "Hatamoto setup" // the site has no name of its own until step 2
	v.Wide = true
	v.Data = d
	a.Render(w, r, status, "setup/account", v)
}

func (a *Admin) handleSetupAccount(w http.ResponseWriter, r *http.Request) {
	if a.setupCompleted(r) || !a.freshInstall(r) {
		a.Error(w, r, http.StatusNotFound, "Setup already completed", "This site already has an owner. Sign in instead.")
		return
	}
	d := setupData{Name: strings.TrimSpace(r.PostFormValue("name")), Email: strings.TrimSpace(r.PostFormValue("email")), Code: strings.TrimSpace(r.PostFormValue("code"))}
	code, pw, confirm := r.PostFormValue("code"), r.PostFormValue("password"), r.PostFormValue("confirm")
	add := func(id, label, msg string, field *string) {
		*field = msg
		d.Errors = append(d.Errors, ui.FieldError{ID: id, Label: label, Message: msg})
	}
	if !a.deps.SetupCode.Verify(code) {
		add("code", "Setup code", "That code is not right. It is in the server log, or in HATAMOTO_SETUP_CODE.", &d.CodeErr)
	}
	if d.Name == "" {
		add("name", "Name", "Enter your name", &d.NameErr)
	}
	if !strings.Contains(d.Email, "@") || strings.ContainsAny(d.Email, " <>") {
		add("email", "Email", "Enter a valid email address", &d.EmailErr)
	}
	if len(pw) < auth.MinPasswordLen {
		add("password", "Password", sentence(auth.ErrPasswordShort.Error()), &d.PassErr)
	}
	if pw != confirm {
		add("confirm", "Repeat the password", "The passwords do not match", &d.MatchErr)
	}
	if len(d.Errors) > 0 {
		a.renderAccount(w, r, http.StatusUnprocessableEntity, d)
		return
	}
	if a.deps.Owners == nil || a.deps.Auth == nil {
		a.Error(w, r, http.StatusServiceUnavailable, "Setup unavailable", "The server is not ready.")
		return
	}
	u, err := a.deps.Owners.CreateFirstOwner(r.Context(), d.Name, d.Email, pw)
	switch {
	case errors.Is(err, auth.ErrSetupDone), errors.Is(err, user.ErrEmailAlreadyExists):
		a.Error(w, r, http.StatusNotFound, "Setup already completed", "This site already has an owner. Sign in instead.")
		return
	case err != nil:
		a.logger().Error("create first owner failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "We could not create the account. Try again.")
		return
	}
	token, err := a.deps.Auth.IssueSession(r.Context(), u.ID, r.UserAgent(), middleware.GetClientIP(r))
	if err != nil {
		a.logger().Error("issue session failed", "err", err)
		http.Redirect(w, r, a.adminPath+"/login", http.StatusSeeOther)
		return
	}
	a.setSession(w, r, token)
	// The account step just proved the password, so the address step needs no second prompt.
	if a.deps.Sessions != nil {
		if sess, err := a.deps.Sessions.Validate(r.Context(), token); err == nil {
			a.sudo.store.Grant(sess.ID)
		}
	}
	http.Redirect(w, r, a.adminPath+setupAddressPath, http.StatusSeeOther)
}

func (a *Admin) renderSite(w http.ResponseWriter, r *http.Request, status int, f siteForm, errs map[string]string) {
	d := setupData{Steps: steps(2), Back: a.setupBackToAddress(), Detected: a.requestOrigin(r), Site: f, SiteErrs: errs, Languages: languageOptions(f.Language), Timezones: timezoneOptions(f.Timezone)}
	for _, k := range []struct{ id, label string }{{"name", "Site name"}, {"url", "Site address"}, {"language", "Language"}, {"timezone", "Time zone"}} {
		if m := errs[k.id]; m != "" {
			d.Errors = append(d.Errors, ui.FieldError{ID: k.id, Label: k.label, Message: m})
		}
	}
	v := a.NewView(w, r, "Your site")
	v.Wide = true
	v.Data = d
	a.Render(w, r, status, "setup/site", v)
}

func (a *Admin) handleSetupSiteForm(w http.ResponseWriter, r *http.Request) {
	cfg, err := a.deps.Site.Get(r.Context())
	if err != nil {
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "We could not load the site settings.")
		return
	}
	// The site address starts empty: pinning whatever origin the wizard was
	// opened on (localhost, a bare IP) would silently wrong every canonical
	// URL. The detected origin is only a hint.
	a.renderSite(w, r, http.StatusOK, siteFormFrom(cfg), nil)
}

func (a *Admin) handleSetupSite(w http.ResponseWriter, r *http.Request) {
	f := siteForm{Name: r.PostFormValue("name"), URL: r.PostFormValue("url"), Language: r.PostFormValue("language"), Timezone: r.PostFormValue("timezone")}
	if errs := f.validate(); len(errs) > 0 {
		a.renderSite(w, r, http.StatusUnprocessableEntity, f, errs)
		return
	}
	if err := a.saveSite(r.Context(), f); err != nil {
		a.logger().Error("setup: save site failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "We could not save the settings.")
		return
	}
	a.invalidate(r.Context())
	http.Redirect(w, r, a.adminPath+"/setup/email", http.StatusSeeOther)
}

// The Email step is optional: Skip is a plain link to the next step, and a
// build without the email settings service goes straight past it.
func (a *Admin) handleSetupEmailForm(w http.ResponseWriter, r *http.Request) {
	if a.deps.EmailSettings == nil {
		http.Redirect(w, r, a.adminPath+"/setup/theme", http.StatusSeeOther)
		return
	}
	v, err := a.deps.EmailSettings.Get(r.Context())
	if err != nil {
		a.logger().Error("setup: email settings", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The email settings could not be loaded.")
		return
	}
	a.renderSetupEmail(w, r, http.StatusOK, v, emailSubmission{})
}

func (a *Admin) handleSetupEmail(w http.ResponseWriter, r *http.Request) {
	svc := a.deps.EmailSettings
	if svc == nil {
		http.Redirect(w, r, a.adminPath+"/setup/theme", http.StatusSeeOther)
		return
	}
	stored, err := svc.Get(r.Context())
	if err != nil {
		a.logger().Error("setup: email settings", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The email settings could not be loaded.")
		return
	}
	res := a.submitEmail(r, svc, stored)
	switch {
	case res.fatal:
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "We could not save the email settings.")
	case !res.saved:
		a.renderSetupEmail(w, r, http.StatusUnprocessableEntity, stored, res)
	default:
		http.Redirect(w, r, a.adminPath+"/setup/theme", http.StatusSeeOther)
	}
}

func (a *Admin) renderSetupEmail(w http.ResponseWriter, r *http.Request, status int, stored EmailSettingsView, res emailSubmission) {
	v := a.NewView(w, r, "Set up email")
	v.Wide = true
	v.Data = setupData{
		Steps: steps(3), Errors: res.errs, EmailCheck: res.checkFailed, Back: a.adminPath + "/setup/site",
		MailForm: newEmailFields(stored, res.echo, res.errs),
	}
	a.Render(w, r, status, "setup/email", v)
}

var hexColour = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// setupThemes lists the themes the Look step offers: every installed theme that policy lets run.
// Without a package manager it is the built-in theme alone.
func (a *Admin) setupThemes(ctx context.Context) []ThemeInfo {
	if a.deps.Growth.Packages != nil {
		if all, err := a.deps.Growth.Packages.Themes(ctx); err == nil {
			var out []ThemeInfo
			for _, t := range all {
				if t.Blocked == "" {
					out = append(out, t)
				}
			}
			if len(out) > 0 {
				return out
			}
		}
	}
	return []ThemeInfo{{Name: "default", Description: "The built-in theme.", Builtin: true, Active: true}}
}

// themeItems draws themes as radio cards; chosen (when set) overrides which one is selected.
func themeItems(themes []ThemeInfo, chosen string) []ui.RadioCardItem {
	items := make([]ui.RadioCardItem, 0, len(themes))
	for _, t := range themes {
		it := ui.RadioCardItem{Value: t.Name, Title: t.Name, Description: t.Description, Checked: t.Active}
		if t.Builtin {
			it.Title = "Default"
			if it.Description == "" {
				it.Description = "The built-in theme."
			}
		}
		if chosen != "" {
			it.Checked = t.Name == chosen
		}
		if t.Active {
			it.Tag = &ui.Badge{Label: "Active", Kind: "success"}
		}
		items = append(items, it)
	}
	return items
}

func (a *Admin) renderTheme(w http.ResponseWriter, r *http.Request, status int, colour, chosen, colourErr, themeErr string) {
	d := setupData{Steps: steps(4), Colour: colour, ColourErr: colourErr, Back: a.setupBackToEmail()}
	d.ThemeItems = themeItems(a.setupThemes(r.Context()), chosen)
	if themeErr != "" {
		d.Errors = append(d.Errors, ui.FieldError{ID: "theme-0", Label: "Theme", Message: themeErr})
	}
	if colourErr != "" {
		d.Errors = append(d.Errors, ui.FieldError{ID: "colour", Label: "Brand colour", Message: colourErr})
	}
	v := a.NewView(w, r, "Choose a look")
	v.Wide = true
	v.Data = d
	a.Render(w, r, status, "setup/theme", v)
}

// setupBackToEmail is where Back on the Look step goes: the Email step is skipped when the
// build has no email settings, and its form would send the owner straight forward again.
func (a *Admin) setupBackToEmail() string {
	if a.deps.EmailSettings == nil {
		return a.adminPath + "/setup/site"
	}
	return a.adminPath + "/setup/email"
}

func (a *Admin) handleSetupThemeForm(w http.ResponseWriter, r *http.Request) {
	colour := "#0060df"
	if cfg, err := a.deps.Site.Get(r.Context()); err == nil && hexColour.MatchString(cfg.PrimaryColor) {
		colour = cfg.PrimaryColor
	}
	a.renderTheme(w, r, http.StatusOK, colour, "", "", "")
}

func (a *Admin) handleSetupTheme(w http.ResponseWriter, r *http.Request) {
	colour := strings.TrimSpace(r.PostFormValue("colour"))
	chosen := r.PostFormValue("theme")
	if !hexColour.MatchString(colour) || !validBrandColor(colour) {
		a.renderTheme(w, r, http.StatusUnprocessableEntity, colour, chosen, "Choose a colour", "")
		return
	}
	var pick *ThemeInfo
	if chosen != "" {
		themes := a.setupThemes(r.Context())
		for i := range themes {
			if themes[i].Name == chosen {
				pick = &themes[i]
			}
		}
		if pick == nil {
			a.renderTheme(w, r, http.StatusUnprocessableEntity, colour, "", "", "Choose one of the listed themes")
			return
		}
	}
	// The colour is saved first: if it fails nothing has changed yet, and a theme that then
	// cannot be switched on leaves the owner's colour kept and the step open to try again.
	if _, err := a.deps.Site.Update(r.Context(), siteUpdateColour(colour)); err != nil {
		a.logger().Error("setup: save colour failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "We could not save the colour.")
		return
	}
	if pick != nil && !pick.Active && a.deps.Growth.Packages != nil {
		if err := a.deps.Growth.Packages.Activate(r.Context(), pick.Name); err != nil {
			a.logger().Error("setup: activate theme failed", "theme", pick.Name, "err", err)
			a.invalidate(r.Context())
			a.renderTheme(w, r, http.StatusUnprocessableEntity, colour, chosen, "", "That theme could not be switched on. Pick another or continue with the current one.")
			return
		}
	}
	a.invalidate(r.Context())
	http.Redirect(w, r, a.setupAfterTheme(r.Context()), http.StatusSeeOther)
}

// setupStatsStep reports whether the Stats step is part of this wizard: it is
// skipped when the build has no install stats service or the server
// configuration turns them off (HATAMOTO_TELEMETRY=off). Like the Email step,
// a skipped step's form sends the owner straight forward again.
func (a *Admin) setupStatsStep(ctx context.Context) bool {
	if a.deps.Stats == nil || a.deps.Demo.Enabled {
		return false
	}
	// Only the server configuration matters here, not the stored choice.
	return !a.deps.Stats.ForcedOff()
}

// setupAfterTheme is where the Look step goes on to.
func (a *Admin) setupAfterTheme(ctx context.Context) string {
	if a.setupStatsStep(ctx) {
		return a.adminPath + "/setup/stats"
	}
	return a.adminPath + "/setup/content"
}

// setupBackToStats is where Back on the Content step goes.
func (a *Admin) setupBackToStats(ctx context.Context) string {
	if a.setupStatsStep(ctx) {
		return a.adminPath + "/setup/stats"
	}
	return a.adminPath + "/setup/theme"
}

func (a *Admin) renderSetupStats(w http.ResponseWriter, r *http.Request, status int, v StatsView, needed bool) {
	nv := a.NewView(w, r, "Help improve Hatamoto")
	nv.Wide = true
	nv.Data = setupData{Steps: steps(5), Back: a.adminPath + "/setup/theme", StatsJSON: v.PreviewJSON, StatsNeeded: needed}
	a.Render(w, r, status, "setup/stats", nv)
}

// setupStatsSkip handles a stats store error in the wizard: warn and go on to
// Content, so the optional step can never block setup.
func (a *Admin) setupStatsSkip(w http.ResponseWriter, r *http.Request, msg string, err error) {
	a.logger().Warn(msg, "err", err)
	http.Redirect(w, r, a.adminPath+"/setup/content", http.StatusSeeOther)
}

// The Stats step is optional and never ends setup: the owner may skip past it
// with consent left unset, and the Content step finishes the wizard.
func (a *Admin) handleSetupStatsForm(w http.ResponseWriter, r *http.Request) {
	if !a.setupStatsStep(r.Context()) {
		http.Redirect(w, r, a.adminPath+"/setup/content", http.StatusSeeOther)
		return
	}
	v, err := a.deps.Stats.View(r.Context())
	if err != nil {
		a.setupStatsSkip(w, r, "setup: stats view", err)
		return
	}
	a.renderSetupStats(w, r, http.StatusOK, v, false)
}

func (a *Admin) handleSetupStats(w http.ResponseWriter, r *http.Request) {
	if !a.setupStatsStep(r.Context()) {
		http.Redirect(w, r, a.adminPath+"/setup/content", http.StatusSeeOther)
		return
	}
	choice := r.PostFormValue("choice")
	if choice != "yes" && choice != "no" {
		// Neither answer is a default: Enter on the form, or a bare post, asks again.
		v, err := a.deps.Stats.View(r.Context())
		if err != nil {
			a.setupStatsSkip(w, r, "setup: stats view", err)
			return
		}
		a.renderSetupStats(w, r, http.StatusUnprocessableEntity, v, true)
		return
	}
	if err := a.deps.Stats.Choose(r.Context(), choice == "yes"); err != nil {
		a.setupStatsSkip(w, r, "setup: stats choose", err)
		return
	}
	a.audit(r, "stats.consent", "stats", map[string]any{"choice": choice})
	http.Redirect(w, r, a.adminPath+"/setup/content", http.StatusSeeOther)
}

// contentItems are the three starting points of the Content step.
func contentItems(choice string) []ui.RadioCardItem {
	return []ui.RadioCardItem{
		{Value: "empty", Title: "Start empty", Description: "Just a home page.", Checked: choice == "empty"},
		{Value: "starter", Title: "Install starter content", Description: "About and Contact pages, two blog posts, menus and a contact form.", Checked: choice == "starter"},
		{Value: "wordpress", Title: "Import from WordPress", Description: "Copy your pages, posts, media and menus and keep every old address working. Continues to the importer.", Checked: choice == "wordpress"},
	}
}

func (a *Admin) renderContent(w http.ResponseWriter, r *http.Request, status int, choice, failed string) {
	if choice == "" {
		choice = "empty"
	}
	v := a.NewView(w, r, "Start with content")
	v.Wide = true
	v.Data = setupData{Steps: steps(6), Choice: choice, Failed: failed, ContentItems: contentItems(choice), Back: a.setupBackToStats(r.Context())}
	a.Render(w, r, status, "setup/content", v)
}

func (a *Admin) handleSetupContentForm(w http.ResponseWriter, r *http.Request) {
	a.renderContent(w, r, http.StatusOK, "", "")
}

func (a *Admin) handleSetupContent(w http.ResponseWriter, r *http.Request) {
	choice := r.PostFormValue("content")
	if choice != "empty" && choice != "starter" && choice != "wordpress" {
		a.renderContent(w, r, http.StatusUnprocessableEntity, "empty", "Choose one of the options.")
		return
	}
	u := userFrom(r.Context())
	if choice == "starter" {
		if a.deps.Starter == nil {
			a.renderContent(w, r, http.StatusServiceUnavailable, choice, "Starter content is not available.")
			return
		}
		opts := starter.Options{SiteName: a.siteName(r.Context()), NotifyEmail: u.Email}
		if id, err := uuidOf(u.ID); err == nil {
			opts.UserID = &id
		}
		res, err := a.deps.Starter.Install(r.Context(), opts)
		if err != nil {
			a.logger().Error("starter content failed", "err", err)
			a.renderContent(w, r, http.StatusInternalServerError, choice, "Installing the starter content failed part-way. It is safe to try again: finished items are skipped.")
			return
		}
		a.SetFlash(w, r, "success", starterSummary(res))
	}
	if _, err := a.deps.Site.Update(r.Context(), siteUpdateDone()); err != nil {
		a.logger().Error("setup: mark complete failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "We could not finish setup.")
		return
	}
	a.invalidate(r.Context())
	if choice == "wordpress" {
		// Setup is finished; the import wizard takes over from here.
		http.Redirect(w, r, a.adminPath+importBase, http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, a.adminPath+"/setup/done", http.StatusSeeOther)
}

func (a *Admin) handleSetupDone(w http.ResponseWriter, r *http.Request) {
	v := a.NewView(w, r, "You are all set")
	v.Wide = true
	d := setupData{Steps: steps(7), Home: "/", Mail: a.mailConfigured(), Links: []doneLink{
		{Icon: "external", Title: "View your site", Href: "/"},
		{Icon: "edit", Title: "Edit pages and posts", Href: a.adminPath + "/entities"},
		{Icon: "users", Title: "Invite someone", Href: a.adminPath + "/settings/users"},
		{Icon: "settings", Title: "Change settings", Href: a.adminPath + "/settings"},
	}}
	if a.deps.Address != nil {
		d.Address = a.addressOrigin(r) + a.deps.Address.State().Path
		// The mail is built from the configured site address, never from the
		// request's Host: without one it carries the path alone.
		d.Emailed = a.setupAddressMail(r.Context(), userFrom(r.Context()), a.siteAddress(r)+a.deps.Address.State().Path)
	}
	v.Data = d
	a.Render(w, r, http.StatusOK, "setup/done", v)
}

// invalidate clears the rendered-page cache after a step changed what public
// pages show (site name, colour, homepage).
func (a *Admin) invalidate(ctx context.Context) {
	if a.deps.Invalidate != nil {
		a.deps.Invalidate(ctx)
	}
}
