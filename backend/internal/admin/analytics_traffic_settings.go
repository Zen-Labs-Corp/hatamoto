package admin

import (
	"net/http"
	"net/url"
)

// handleTrafficSettings saves the engagement switch of the Traffic page (owner only) and goes
// back to the page with the range kept.
func (a *Admin) handleTrafficSettings(w http.ResponseWriter, r *http.Request) {
	if u := userFrom(r.Context()); u == nil || u.Role != "owner" {
		a.Error(w, r, http.StatusForbidden, "Not allowed", "Only the site owner can change what the visit counter records.")
		return
	}
	es := a.deps.Growth.EngagementSetting
	if es == nil {
		a.unavailable(w, r, "Analytics")
		return
	}
	on := r.PostFormValue("engagement") == "1"
	if err := es.Set(r.Context(), on); err != nil {
		a.logger().Error("save engagement setting failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The setting could not be saved.")
		return
	}
	a.audit(r, "analytics.engagement", "analytics-settings", map[string]any{"enabled": on})
	if on {
		a.SetFlash(w, r, "success", "Engagement counting is on.")
	} else {
		a.SetFlash(w, r, "success", "Engagement counting is off. Visits and pageviews are still counted.")
	}
	http.Redirect(w, r, a.trafficReturn(r), http.StatusSeeOther)
}

// trafficReturn is the Traffic page with the range and compare switch of the form's return field
// (anything else in it is dropped, so it cannot send the user elsewhere).
func (a *Admin) trafficReturn(r *http.Request) string {
	keep := url.Values{}
	if u, err := url.Parse(r.PostFormValue("return")); err == nil {
		for _, k := range []string{"from", "to", "compare"} {
			if v := u.Query().Get(k); v != "" {
				keep.Set(k, v)
			}
		}
	}
	return withCarry(a.url("analytics", "traffic"), keep)
}
