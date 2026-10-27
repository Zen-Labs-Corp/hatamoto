package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"unicode/utf8"

	"hatamoto.dev/core/internal/admin/ui"
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
	// contact
	Contact     contactForm
	ContactErrs map[string]string
}

type contactForm struct {
	NotifyEmail, Phone, Hours, Street, City, State, Postal, Country string
}

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
	a.mountEmailSettings(owner)
	a.mountUpdatesSettings(owner)
	a.mountBackupsSettings(owner)
	a.mountAuditLog(owner)
	a.mountSecuritySettings(owner)
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

func contactFrom(c *siteconfig.Config) contactForm {
	f := contactForm{NotifyEmail: c.NotificationEmail, Phone: c.Phone, Hours: c.BusinessHours}
	var ad address
	if len(c.PostalAddress) > 0 {
		_ = json.Unmarshal(c.PostalAddress, &ad)
	}
	f.Street, f.City, f.State, f.Postal, f.Country = ad.Street, ad.City, ad.State, ad.PostalCode, ad.Country
	return f
}

func (a *Admin) renderContact(w http.ResponseWriter, r *http.Request, status int, f contactForm, errs map[string]string) {
	d := settingsData{Contact: f, ContactErrs: errs}
	for _, k := range []struct{ id, label string }{{"notify", "Notification email"}, {"phone", "Phone"}, {"hours", "Business hours"}, {"street", "Street"}, {"city", "City"}, {"state", "State or region"}, {"postal", "Postal code"}, {"country", "Country"}} {
		if m := errs[k.id]; m != "" {
			d.Summary = append(d.Summary, ui.FieldError{ID: k.id, Label: k.label, Message: m})
		}
	}
	a.renderGeneral(w, r, status, generalData{Contact: &d})
}

func (a *Admin) handleContactSave(w http.ResponseWriter, r *http.Request) {
	t := func(k string) string { return strings.TrimSpace(r.PostFormValue(k)) }
	f := contactForm{NotifyEmail: t("notify"), Phone: t("phone"), Hours: t("hours"), Street: t("street"), City: t("city"), State: t("state"), Postal: t("postal"), Country: t("country")}
	errs := map[string]string{}
	if f.NotifyEmail != "" && (!strings.Contains(f.NotifyEmail, "@") || strings.ContainsAny(f.NotifyEmail, " <>")) {
		errs["notify"] = "Enter a valid email address"
	}
	if utf8.RuneCountInString(f.Phone) > 50 {
		errs["phone"] = "Use at most 50 characters"
	}
	for k, v := range map[string]string{"hours": f.Hours, "street": f.Street, "city": f.City, "state": f.State, "postal": f.Postal, "country": f.Country} {
		if utf8.RuneCountInString(v) > 200 {
			errs[k] = "Use at most 200 characters"
		}
	}
	if len(errs) > 0 {
		a.renderContact(w, r, http.StatusUnprocessableEntity, f, errs)
		return
	}
	raw, _ := json.Marshal(address{Street: f.Street, City: f.City, State: f.State, PostalCode: f.Postal, Country: f.Country})
	_, err := a.deps.Site.Update(r.Context(), siteconfig.UpdateInput{NotificationEmail: &f.NotifyEmail, Phone: &f.Phone, BusinessHours: &f.Hours, PostalAddress: raw})
	if err != nil {
		a.logger().Error("save contact details failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The contact details could not be saved.")
		return
	}
	if a.deps.Invalidate != nil {
		a.deps.Invalidate(r.Context())
	}
	a.SetFlash(w, r, "success", "Contact details saved.")
	http.Redirect(w, r, a.adminPath+"/settings/general#contact", http.StatusSeeOther)
}
