package admin

import (
	"net/http"

	"hatamoto.dev/core/internal/httpx"
)

const reviewSettingsPath = "/settings/review"

// reviewSettingsData is the Review settings page.
type reviewSettingsData struct{ Enabled bool }

func (a *Admin) mountReviewSettings(owner *httpx.Group) {
	owner.HandleFunc("GET "+reviewSettingsPath, a.handleReviewSettings)
	owner.HandleFunc("POST "+reviewSettingsPath, a.handleReviewSettingsSave)
}

func (a *Admin) handleReviewSettings(w http.ResponseWriter, r *http.Request) {
	if a.deps.Review == nil {
		a.unavailable(w, r, "Review")
		return
	}
	v := a.NewView(w, r, "Review")
	v.Section, v.Tab = "entity-models", "review"
	v.Width = "compact"
	v.Data = reviewSettingsData{Enabled: a.deps.Review.Enabled(r.Context())}
	a.Render(w, r, http.StatusOK, "settings/review", v)
}

func (a *Admin) handleReviewSettingsSave(w http.ResponseWriter, r *http.Request) {
	svc := a.deps.Review
	if svc == nil {
		a.unavailable(w, r, "Review")
		return
	}
	_ = r.ParseForm() // a malformed body leaves the form empty: the switch reads as off
	on := r.PostFormValue("review_enabled") == "1"
	if err := svc.SetEnabled(r.Context(), on); err != nil {
		a.logger().Error("review settings save", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The review setting could not be saved.")
		return
	}
	a.audit(r, "settings.review", "site", map[string]any{"enabled": on})
	if on {
		a.SetFlash(w, r, "success", "Review is on.")
	} else {
		a.SetFlash(w, r, "success", "Review is off.")
	}
	http.Redirect(w, r, a.adminPath+reviewSettingsPath, http.StatusSeeOther)
}
