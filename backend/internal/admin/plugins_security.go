package admin

import (
	"net/http"
	"net/url"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/httpx"
)

// mountPluginSecurity registers the Package security page and the route that
// turns the unsigned-package opt-in off (owner only).
func (a *Admin) mountPluginSecurity(owner *httpx.Group) {
	owner.HandleFunc("GET /plugins/security", a.handlePackageSecurity)
	owner.HandleFunc("POST /plugins/unsigned/off", a.handleStopUnsigned)
}

// pluginSecurityPage is the data of the Package security page.
type pluginSecurityPage struct {
	State     UnsignedState
	UntilText string
	Signers   []SignerInfo
	// SiteHost is the host name the owner has to type to allow unsigned packages.
	SiteHost string
	// NoPassword is set when the signed-in owner has no password to confirm with.
	NoPassword  bool
	ProfileHref string
	Durations   []ui.Option
}

// unsignedUntilText formats the end of a timed opt-in; empty when there is none.
func unsignedUntilText(s UnsignedState) string {
	if !s.On || s.Until == nil {
		return ""
	}
	return s.Until.UTC().Format("2 Jan 2006 15:04 UTC")
}

// packageHost is the host name of this site as the owner sees it: the base URL's
// host, else the request's, without a port. Empty when neither resolves.
func (a *Admin) packageHost(r *http.Request) string {
	if a.deps.BaseURL != nil {
		if u, err := url.Parse(a.deps.BaseURL(r)); err == nil && u.Hostname() != "" {
			return u.Hostname()
		}
	}
	if u, err := url.Parse("//" + r.Host); err == nil {
		return u.Hostname()
	}
	return ""
}

// Durations of an unsigned allowance, as posted by the form.
const (
	unsignedHour    = "1h"
	unsignedDay     = "24h"
	unsignedForever = "forever"
)

func (a *Admin) handlePackageSecurity(w http.ResponseWriter, r *http.Request) {
	pk, ok := a.packageAdmin(w, r)
	if !ok {
		return
	}
	d := pluginSecurityPage{
		State:       pk.UnsignedState(r.Context()),
		Signers:     pk.Signers(r.Context()),
		SiteHost:    a.packageHost(r),
		ProfileHref: a.url("profile"),
		Durations: []ui.Option{
			{Value: unsignedHour, Label: "1 hour"},
			{Value: unsignedDay, Label: "24 hours"},
			{Value: unsignedForever, Label: "Until I turn it off"},
		},
	}
	d.UntilText = unsignedUntilText(d.State)
	d.NoPassword = !a.hasPassword(r)
	v := a.NewView(w, r, "Package security")
	v.Width = "narrow"
	v.Crumbs = []Crumb{{Label: "Plugins", Href: a.adminPath + "/plugins"}, {Label: "Package security"}}
	v.Data = d
	a.Render(w, r, http.StatusOK, "plugins/security", v)
}

// hasPassword reports whether the signed-in user has a password to confirm
// with. Users without one (or that cannot be read) are blocked, never waved through.
func (a *Admin) hasPassword(r *http.Request) bool {
	s := sessionFrom(r.Context())
	if s == nil || s.UserID == nil {
		return false
	}
	u, err := a.deps.Users.GetByID(r.Context(), *s.UserID)
	return err == nil && u.PasswordHash != ""
}

func (a *Admin) handleStopUnsigned(w http.ResponseWriter, r *http.Request) {
	a.notBuilt(w, r)
}
