package admin

import (
	"html"
	"html/template"
	"net/http"

	"hatamoto.dev/core/internal/admin/ui"
)

// generalData is the General settings page: identity, homepage and email on
// one narrow page. A section a failed save re-renders carries its submitted
// values and errors; the others show saved state.
type generalData struct {
	Ident  *settingsData
	Notify *notifyData
	Home   *homepageData

	MailOn bool

	// Tabs is the in-page strip; IdentNote is the Identity description with
	// its link to Theme colours, built here so the href is escaped once.
	Tabs      []ui.Tab
	IdentNote template.HTML
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
		g.Ident = &d
	}
	if g.Notify == nil {
		g.Notify = &notifyData{Value: cfg.NotificationEmail}
	}
	if g.Home == nil {
		h := a.homepageCurrent(r, cfg)
		g.Home = &h
	}
	g.MailOn = a.mailConfigured()

	g.Tabs = []ui.Tab{
		{Label: "Identity", Href: "#identity", Active: true},
		{Label: "Homepage", Href: "#homepage-section"},
		{Label: "Email", Href: "#email"},
	}
	g.IdentNote = template.HTML("The name, address and language of your site. Brand colours live in Theme › <a href=\"" +
		html.EscapeString(a.url("theme", "colours")) + "\">Colours and fonts</a>.")

	v := a.NewView(w, r, "General")
	v.Width = "compact"
	v.Section, v.Tab = "general", "general"
	v.Data = g
	a.Render(w, r, status, "settings/general", v)
}
