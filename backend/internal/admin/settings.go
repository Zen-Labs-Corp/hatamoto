package admin

import (
	"context"
	"encoding/json"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"hatamoto.dev/core/internal/admin/schemaform"
	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/bizprofile"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/siteconfig"
)

type settingsData struct {
	Summary []ui.FieldError
	// site
	Site      siteForm
	SiteErrs  map[string]string
	Languages []ui.Option
	Timezones []ui.Option
	LogoURL   string
	Tagline   string
	Domain    string
	// business details: the notification email plus the site-wide profile form
	Contact     contactForm
	ContactErrs map[string]string
	Biz         template.HTML
	// BizHint is set on a site without a Locales list, where the office-only
	// fields feed no output yet.
	BizHint bool
}

type contactForm struct {
	NotifyEmail string
}

// bizPrefix namespaces the fields of the site-wide business form.
const bizPrefix = "biz."

type address struct {
	Street     string `json:"street"`
	City       string `json:"city"`
	State      string `json:"state"`
	PostalCode string `json:"postal_code"`
	Country    string `json:"country"`
}

func (a *Admin) mountSettings(authed *httpx.Group) {
	a.mountSettingsFrame(authed)
	owner := authed.Group("", a.RequireRole("owner"))
	a.mountExportSettings(owner)
	a.mountPerformanceSettings(owner)
	a.mountSpamSettings(owner)
	a.mountSEOSettings(owner)
	a.mountEmailSettings(owner)
	a.mountUpdatesSettings(owner)
	a.mountBackupsSettings(owner)
	a.mountAuditLog(owner)
	a.mountSecuritySettings(owner)
	a.mountLocaleConvert(owner)
	a.mountLocaleSettings(owner)
	owner.HandleFunc("GET /settings/general", a.handleGeneral)
	owner.HandleFunc("POST /settings/general/identity", a.handleSiteSave)
	owner.HandleFunc("POST /settings/general/contact", a.handleContactSave)
	a.mountAPIKeys(owner)
	a.mountWebhooks(owner)
}

func (a *Admin) loadSite(ctx context.Context) (*siteconfig.Config, error) {
	return a.deps.Site.Get(ctx)
}

func (a *Admin) renderSiteSettings(w http.ResponseWriter, r *http.Request, status int, d settingsData) {
	d.Languages, d.Timezones = languageOptions(d.Site.Language), timezoneOptions(d.Site.Timezone)
	for _, k := range []struct{ id, label string }{{"name", "Site name"}, {"url", "Site address"}, {"language", "Language"}, {"timezone", "Time zone"}, {"logo", "Logo"}, {"tagline", "Tagline"}, {"domain", "Custom domain"}} {
		if m := d.SiteErrs[k.id]; m != "" {
			d.Summary = append(d.Summary, ui.FieldError{ID: k.id, Label: k.label, Message: m})
		}
	}
	a.renderGeneral(w, r, status, generalData{Ident: &d})
}

func validLogoURL(s string) bool {
	if s == "" || strings.HasPrefix(s, "/") && !strings.HasPrefix(s, "//") {
		return true
	}
	return strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "http://")
}

// localeFielder is implemented by the locale validation error of the server,
// which knows which setting it rejected.
type localeFielder interface{ LocaleField() string }

func (a *Admin) handleSiteSave(w http.ResponseWriter, r *http.Request) {
	f := siteForm{Name: r.PostFormValue("name"), URL: r.PostFormValue("url"), Language: r.PostFormValue("language"), Timezone: r.PostFormValue("timezone")}
	d := settingsData{LogoURL: strings.TrimSpace(r.PostFormValue("logo")), Tagline: strings.TrimSpace(r.PostFormValue("tagline")), Domain: strings.TrimSpace(r.PostFormValue("domain"))}
	errs := f.validate()
	if !validLogoURL(d.LogoURL) || utf8.RuneCountInString(d.LogoURL) > 500 {
		errs["logo"] = "Enter a web address or a path starting with /"
	}
	if utf8.RuneCountInString(d.Tagline) > 200 {
		errs["tagline"] = "Use at most 200 characters"
	}
	if strings.ContainsAny(d.Domain, " /:<>\"'") || utf8.RuneCountInString(d.Domain) > 253 {
		errs["domain"] = "Enter a domain like www.example.com"
	}
	d.Site, d.SiteErrs = f, errs
	if len(errs) > 0 {
		a.renderSiteSettings(w, r, http.StatusUnprocessableEntity, d)
		return
	}
	in := siteconfig.UpdateInput{CompanyName: &f.Name, PublicBaseURL: &f.URL, Language: &f.Language, Timezone: &f.Timezone, LogoURL: &d.LogoURL, Tagline: &d.Tagline, CustomDomain: &d.Domain}
	if _, err := a.deps.Site.Update(r.Context(), in); err != nil {
		a.logger().Error("save site settings failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The settings could not be saved.")
		return
	}
	if a.deps.Invalidate != nil {
		a.deps.Invalidate(r.Context())
	}
	a.SetFlash(w, r, "success", "Site settings saved.")
	http.Redirect(w, r, a.adminPath+"/settings/general#identity", http.StatusSeeOther)
}

// siteProfile is the stored site-wide profile with the three legacy columns
// applied, so phone, hours note and address are always the saved ones.
func siteProfile(c *siteconfig.Config) (bizprofile.Profile, error) {
	p, err := bizprofile.Parse(c.BusinessProfile)
	if err != nil {
		return p, err
	}
	return bizprofile.FromLegacy(p, c.Phone, c.BusinessHours, c.PostalAddress), nil
}

// profileValues is the form view of a profile.
func profileValues(p bizprofile.Profile) map[string]any {
	raw, _ := json.Marshal(p)
	m := map[string]any{}
	_ = json.Unmarshal(raw, &m)
	return m
}

// valuesProfile reads decoded form values back into a profile.
func valuesProfile(vals map[string]any) (bizprofile.Profile, error) {
	raw, err := json.Marshal(vals)
	if err != nil {
		return bizprofile.Profile{}, err
	}
	return bizprofile.Parse(raw)
}

// openAfterAdd names the repeater item a pressed "add" button just created, so
// the re-rendered form opens it: "opening_hours.2".
func openAfterAdd(form map[string][]string, prefix string, vals map[string]any) string {
	for k := range form {
		name, ok := strings.CutPrefix(k, "_add."+prefix)
		if !ok {
			continue
		}
		if items, ok := vals[name].([]any); ok && len(items) > 0 {
			return name + "." + strconv.Itoa(len(items)-1)
		}
	}
	return ""
}

func localeSetOf(c *siteconfig.Config) *entity.LocaleSet {
	entries := make([]entity.LocaleEntry, 0, len(c.Locales))
	for _, e := range c.Locales {
		entries = append(entries, entity.LocaleEntry{Tag: e.Tag, Segment: e.Segment, Name: e.Name, Hidden: e.Hidden})
	}
	set, err := entity.NewLocaleSet(c.Language, entries, c.PrefixAll, c.XDefaultLocale)
	if err != nil {
		return nil
	}
	return set
}

func (a *Admin) renderContact(w http.ResponseWriter, r *http.Request, status int, f contactForm, errs map[string]string, vals map[string]any, perrs schemaform.Errors) {
	d := settingsData{Contact: f, ContactErrs: errs}
	if m := errs["notify"]; m != "" {
		d.Summary = append(d.Summary, ui.FieldError{ID: "notify", Label: "Notification email", Message: m})
	}
	d.Summary = append(d.Summary, formErrors(bizPrefix, perrs)...)
	a.renderGeneral(w, r, status, generalData{Contact: &d, bizValues: vals, bizErrs: perrs, bizOpen: openAfterAdd(r.PostForm, bizPrefix, vals)})
}

// siteBusinessHTML renders the site-wide business form.
func siteBusinessHTML(vals map[string]any, errs schemaform.Errors, open string) (template.HTML, error) {
	schema, err := siteSchema()
	if err != nil {
		return "", err
	}
	return schemaform.Render(schema, vals, schemaform.Options{Prefix: bizPrefix, UI: businessUI, Errors: errs, OpenPath: open})
}

func (a *Admin) handleContactSave(w http.ResponseWriter, r *http.Request) {
	cfg, err := a.loadSite(r.Context())
	if err != nil {
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The settings could not be loaded.")
		return
	}
	prev, err := siteProfile(cfg)
	if err != nil {
		prev = bizprofile.Profile{}
	}
	schema, err := siteSchema()
	if err != nil {
		a.logger().Error("business schema failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The business details could not be saved.")
		return
	}
	f := contactForm{NotifyEmail: strings.TrimSpace(r.PostFormValue("notify"))}
	errs := map[string]string{}
	if f.NotifyEmail != "" && (!strings.Contains(f.NotifyEmail, "@") || strings.ContainsAny(f.NotifyEmail, " <>")) {
		errs["notify"] = "Enter a valid email address"
	}
	vals, perrs := schemaform.Decode(schema, r.PostForm, profileValues(prev), schemaform.Options{Prefix: bizPrefix})
	p, perr := valuesProfile(vals)
	// Coordinates and the map address belong to a locale card; a stored
	// site-wide value is ignored and dropped on save.
	p.Address.Lat, p.Address.Lng, p.Address.MapURL = "", "", ""
	if schemaform.Structural(r.PostForm) {
		a.renderContact(w, r, http.StatusOK, f, nil, vals, nil)
		return
	}
	if perr == nil && len(perrs) == 0 {
		perrs = profileErrors(bizprofile.Validate(p))
	}
	if len(errs) > 0 || len(perrs) > 0 || perr != nil {
		a.renderContact(w, r, http.StatusUnprocessableEntity, f, errs, vals, perrs)
		return
	}
	raw, _ := json.Marshal(address{Street: p.Address.Street, City: p.Address.City, State: p.Address.State, PostalCode: p.Address.PostalCode, Country: p.Address.Country})
	// The legacy columns hold phone, hours note and the five address keys; the
	// profile column holds everything else.
	stored := p
	stored.Phone, stored.HoursNote = "", ""
	stored.Address.Street, stored.Address.City, stored.Address.State, stored.Address.PostalCode, stored.Address.Country = "", "", "", "", ""
	profileRaw, _ := json.Marshal(stored)
	_, err = a.deps.Site.Update(r.Context(), siteconfig.UpdateInput{NotificationEmail: &f.NotifyEmail, Phone: &p.Phone, BusinessHours: &p.HoursNote, PostalAddress: raw, BusinessProfile: profileRaw})
	if err != nil {
		a.logger().Error("save business details failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The business details could not be saved.")
		return
	}
	if a.deps.Invalidate != nil {
		a.deps.Invalidate(r.Context())
	}
	a.SetFlash(w, r, "success", "Business details saved.")
	http.Redirect(w, r, a.adminPath+"/settings/general#contact", http.StatusSeeOther)
}
