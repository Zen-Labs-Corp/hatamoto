package admin

import (
	"net/http"

	"hatamoto.dev/core/internal/siteaccess"
)

// AccessBanner is the notice on every admin page while the public site is not
// open. Href is the Site access setting, for owners only: editors cannot open
// it.
type AccessBanner struct {
	Mode, Label, Detail, Href string
}

// accessBanner is the banner for a page view: nil unless a signed-in owner or
// editor is looking and the site is in a mode other than open. A failed read
// shows no banner.
func (a *Admin) accessBanner(r *http.Request, u *CurrentUser) *AccessBanner {
	if a.deps.SiteAccess == nil || u == nil || (u.Role != "owner" && u.Role != "editor") {
		return nil
	}
	s, err := a.deps.SiteAccess.Get(r.Context())
	if err != nil || !s.On() {
		return nil
	}
	b := &AccessBanner{Mode: string(s.Mode)}
	switch s.Mode {
	case siteaccess.ModeComingSoon:
		b.Label, b.Detail = "coming soon", "Visitors see a coming soon page."
	case siteaccess.ModeMaintenance:
		b.Label, b.Detail = "maintenance", "Visitors see a maintenance page."
	case siteaccess.ModePassword:
		b.Label, b.Detail = "password", "Visitors need a password."
	}
	if u.Role == "owner" {
		b.Href = a.adminPath + siteAccessPath
	}
	return b
}
