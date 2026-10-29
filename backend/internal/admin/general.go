package admin

import (
	"net/http"
)

// generalData is the General settings page: identity, homepage, contact and
// email on one narrow page. A section a failed save re-renders
// carries its submitted values and errors; the others show saved state.
type generalData struct {
	Ident   *settingsData
	Contact *settingsData
	Home    *homepageData

	MailOn bool
}

func (a *Admin) handleGeneral(w http.ResponseWriter, r *http.Request) {
	a.renderGeneral(w, r, http.StatusOK, generalData{})
}

// renderGeneral fills every section the caller did not supply from the saved
// settings and renders the page.
func (a *Admin) renderGeneral(w http.ResponseWriter, r *http.Request, status int, g generalData) {
	cfg, err := a.loadSite(r.Context())
	if err != nil {
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The settings could not be loaded.")
		return
	}
	if g.Ident == nil {
		d := settingsData{Site: siteFormFrom(cfg), LogoURL: cfg.LogoURL, Tagline: cfg.Tagline, Domain: cfg.CustomDomain}
		d.Languages, d.Timezones = languageOptions(d.Site.Language), timezoneOptions(d.Site.Timezone)
		d.XDefaults = xDefaultOptions(d.Site)
		g.Ident = &d
	}
	if g.Contact == nil {
		g.Contact = &settingsData{Contact: contactFrom(cfg)}
	}
	if g.Home == nil {
		h := a.homepageCurrent(r, cfg)
		g.Home = &h
	}
	g.MailOn = a.mailConfigured()

	v := a.NewView(w, r, "General")
	v.Width = "compact"
	v.Section, v.Tab = "general", "general"
	v.Data = g
	a.Render(w, r, status, "settings/general", v)
}
