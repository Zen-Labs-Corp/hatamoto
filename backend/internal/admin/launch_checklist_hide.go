package admin

import (
	"net/http"

	"encoding/json/jsontext"

	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/siteconfig"
)

// launchHiddenKey is the theme_extra key that hides the Overview launch
// checklist for the whole site.
const launchHiddenKey = "launch_checklist_hidden"

// launchHidePath is the route behind the checklist's "Hide checklist" button.
const launchHidePath = "/launch-checklist/hide"

// launchHidden reports whether an owner hid the launch checklist. A missing or
// unreadable value counts as not hidden.
func launchHidden(cfg *siteconfig.Config) bool {
	if cfg == nil || len(cfg.ThemeExtra) == 0 {
		return false
	}
	var top map[string]jsontext.Value
	if jsonx.Unmarshal(cfg.ThemeExtra, &top, jsonx.StoredLoose) != nil {
		return false
	}
	var hidden bool
	_ = jsonx.Unmarshal(top[launchHiddenKey], &hidden, jsonx.StoredLoose)
	return hidden
}

// handleLaunchChecklistHide hides the launch checklist for the site and goes
// back to the Overview. It writes one theme_extra key and leaves the rest.
func (a *Admin) handleLaunchChecklistHide(w http.ResponseWriter, r *http.Request) {
	if a.deps.Site == nil {
		a.unavailable(w, r, "The launch checklist")
		return
	}
	if err := a.deps.Site.PatchThemeExtra(r.Context(), launchHiddenKey, jsontext.Value("true")); err != nil {
		a.logger().Error("hide launch checklist failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The checklist could not be hidden.")
		return
	}
	http.Redirect(w, r, a.adminPath+"/", http.StatusSeeOther)
}
