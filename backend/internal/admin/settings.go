package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

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
}

func (a *Admin) mountSettings(authed *httpx.Group) {
	a.mountSettingsFrame(authed)
	owner := authed.Group("", a.RequireRole("owner"))
	a.mountExportSettings(owner)
	a.mountPerformanceSettings(owner)
	a.mountSpamSettings(owner)
	a.mountSEOSettings(owner)
	a.mountPrivacySettings(owner)
	a.mountEmailSettings(owner)
	a.mountUpdatesSettings(owner)
	a.mountBackupsSettings(owner)
	a.mountChangesSettings(owner)
	a.mountAuditLog(owner)
	a.mountSecuritySettings(owner)
	a.mountLocaleConvert(owner)
	a.mountLocaleSettings(owner)
	owner.HandleFunc("GET /settings/general", a.handleGeneral)
	owner.HandleFunc("POST /settings/general/identity", a.handleSiteSave)
	owner.HandleFunc("POST /settings/general/email", a.handleNotifySave)
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
	if errs["language"] == "" {
		// The language is the default locale: it must stay one of the locales
		// that Settings > Locales stores.
		cfg, err := a.loadSite(r.Context())
		if err != nil {
			a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The settings could not be loaded.")
			return
		}
		if len(cfg.Locales) > 0 && a.deps.ValidateLocales != nil {
			if _, _, err := a.deps.ValidateLocales(r.Context(), f.Language, cfg.Locales, cfg.PrefixAll, cfg.XDefaultLocale); err != nil {
				errs["language"] = "This language does not fit your locales: " + err.Error() + ". Manage them in Settings > Locales."
			}
		}
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
	triToForm(m)
	return m
}

// valuesProfile reads decoded form values back into a profile.
func valuesProfile(vals map[string]any) (bizprofile.Profile, error) {
	vals = cloneJSON(vals)
	triFromForm(vals)
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

// notifyData is the notification email form of the General page's Email card.
type notifyData struct {
	Value, Error string
	Summary      []ui.FieldError
}

func (a *Admin) handleNotifySave(w http.ResponseWriter, r *http.Request) {
	value := strings.TrimSpace(r.PostFormValue("notify"))
	if value != "" && (!strings.Contains(value, "@") || strings.ContainsAny(value, " <>")) {
		a.renderGeneral(w, r, http.StatusUnprocessableEntity, generalData{Notify: &notifyData{Value: value, Error: "Enter a valid email address",
			Summary: []ui.FieldError{{ID: "notify", Label: "Notification email", Message: "Enter a valid email address"}}}})
		return
	}
	if _, err := a.deps.Site.Update(r.Context(), siteconfig.UpdateInput{NotificationEmail: &value}); err != nil {
		a.logger().Error("save notification email failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The notification email could not be saved.")
		return
	}
	if a.deps.Invalidate != nil {
		a.deps.Invalidate(r.Context())
	}
	a.SetFlash(w, r, "success", "Notification email saved.")
	http.Redirect(w, r, a.adminPath+"/settings/general#email", http.StatusSeeOther)
}
