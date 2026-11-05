package admin

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/httpx"
)

const versionRetentionPath = "/settings/versions"

// VersionRetentionService is the version history limit: how many earlier
// versions of each item the daily prune keeps. The server implements it on the
// site config.
type VersionRetentionService interface {
	Keep(ctx context.Context) (int, error)
	SetKeep(ctx context.Context, n int) error
}

// versionRetentionData is the Versions settings page: the saved (or, after a
// failed save, submitted) value.
type versionRetentionData struct {
	Summary []ui.FieldError
	Error   string
	Keep    string
	Min     string
	Max     string
}

func (a *Admin) mountVersionRetentionSettings(owner *httpx.Group) {
	owner.HandleFunc("GET "+versionRetentionPath, a.handleVersionRetention)
	owner.HandleFunc("POST "+versionRetentionPath, a.handleVersionRetentionSave)
}

func (a *Admin) handleVersionRetention(w http.ResponseWriter, r *http.Request) {
	if a.deps.VersionRetention == nil {
		a.unavailable(w, r, "Versions")
		return
	}
	keep, err := a.deps.VersionRetention.Keep(r.Context())
	if err != nil {
		a.logger().Error("version retention settings", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The version history limit could not be loaded.")
		return
	}
	a.renderVersionRetention(w, r, http.StatusOK, strconv.Itoa(keep), "")
}

func (a *Admin) handleVersionRetentionSave(w http.ResponseWriter, r *http.Request) {
	svc := a.deps.VersionRetention
	if svc == nil {
		a.unavailable(w, r, "Versions")
		return
	}
	ctx := r.Context()
	old, err := svc.Keep(ctx)
	if err != nil {
		a.logger().Error("version retention settings", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The version history limit could not be loaded.")
		return
	}
	_ = r.ParseForm() // a malformed body leaves the form empty and fails validation
	raw := strings.TrimSpace(r.PostFormValue("keep"))
	n, err := strconv.Atoi(raw)
	if err != nil || n < entity.VersionKeepMin || n > entity.VersionKeepMax {
		a.renderVersionRetention(w, r, http.StatusUnprocessableEntity, raw, "Enter a number from "+strconv.Itoa(entity.VersionKeepMin)+" to "+strconv.Itoa(entity.VersionKeepMax)+".")
		return
	}
	if n != old {
		if err := svc.SetKeep(ctx, n); err != nil {
			a.logger().Error("version retention save", "err", err)
			a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The version history limit could not be saved.")
			return
		}
		a.audit(r, "entity-versions.limit", "entity-versions", map[string]any{"old": old, "new": n})
	}
	a.SetFlash(w, r, "success", "Version history limit saved.")
	http.Redirect(w, r, a.adminPath+versionRetentionPath, http.StatusSeeOther)
}

func (a *Admin) renderVersionRetention(w http.ResponseWriter, r *http.Request, status int, keep, errMsg string) {
	d := versionRetentionData{Keep: keep, Error: errMsg, Min: strconv.Itoa(entity.VersionKeepMin), Max: strconv.Itoa(entity.VersionKeepMax)}
	if errMsg != "" {
		d.Summary = []ui.FieldError{{ID: "keep", Label: "Versions to keep per item", Message: errMsg}}
	}
	v := a.NewView(w, r, "Versions")
	v.Section, v.Tab = "backups-recovery", "versions"
	v.Width = "compact"
	v.Data = d
	a.Render(w, r, status, "settings/versions", v)
}
