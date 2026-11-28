package admin

import (
	"context"
	"net/http"

	"hatamoto.dev/core/internal/httpx"
)

const publicAPISettingsPath = "/settings/public-api"

// PublicAPISettings is the switch behind Settings > Integrations > Public content API.
type PublicAPISettings interface {
	Enabled(ctx context.Context) bool
	SetEnabled(ctx context.Context, on bool) error
}

// publicAPISettingsData is the Public content API settings page.
type publicAPISettingsData struct{ Enabled bool }

func (a *Admin) mountPublicAPISettings(owner *httpx.Group) {
	owner.HandleFunc("GET "+publicAPISettingsPath, a.handlePublicAPISettings)
	owner.HandleFunc("POST "+publicAPISettingsPath, a.handlePublicAPISettingsSave)
}

func (a *Admin) handlePublicAPISettings(w http.ResponseWriter, r *http.Request) {
	if a.deps.PublicAPI == nil {
		a.unavailable(w, r, "The public content API")
		return
	}
	v := a.NewView(w, r, "Public content API")
	v.Section, v.Tab = "integrations", "public-api"
	v.Width = "compact"
	v.Data = publicAPISettingsData{Enabled: a.deps.PublicAPI.Enabled(r.Context())}
	a.Render(w, r, http.StatusOK, "settings/public-api", v)
}

func (a *Admin) handlePublicAPISettingsSave(w http.ResponseWriter, r *http.Request) {
	svc := a.deps.PublicAPI
	if svc == nil {
		a.unavailable(w, r, "The public content API")
		return
	}
	_ = r.ParseForm() // a malformed body leaves the form empty: the switch reads as off
	on := r.PostFormValue("public_content_api") == "1"
	if err := svc.SetEnabled(r.Context(), on); err != nil {
		a.logger().Error("public api settings save", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The public content API setting could not be saved.")
		return
	}
	a.audit(r, "settings.public_api", "site", map[string]any{"enabled": on})
	if on {
		a.SetFlash(w, r, "success", "The public content API is on.")
	} else {
		a.SetFlash(w, r, "success", "The public content API is off.")
	}
	http.Redirect(w, r, a.adminPath+publicAPISettingsPath, http.StatusSeeOther)
}
