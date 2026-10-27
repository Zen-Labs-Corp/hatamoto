package admin

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"hatamoto.dev/core/internal/httpx"
)

// mountPluginUpdate registers the one-click update routes of the Plugins page
// (owner only).
func (a *Admin) mountPluginUpdate(owner *httpx.Group) {
	owner.HandleFunc("POST /plugins/update-all", a.handlePluginUpdateAll)
	owner.HandleFunc("POST /plugins/{name}/update", a.handlePluginUpdate)
}

// updateResult is what updatePlugin did.
type updateResult int

const (
	// updated: the new version is installed, grants as they were.
	updated updateResult = iota
	// needsReview: nothing was installed; the owner has to look at this update.
	needsReview
)

// updatePlugin brings one plugin to the registry's version without asking.
// It downloads the package itself and decides from what it finds, so nothing
// the browser posted is trusted. It never passes a grant (the stored grants
// stay as they are) and never confirms: an update that needs the owner's
// confirmation (other signer, older version, wider access) or that asks for a
// permission the plugin does not have yet is reported as needsReview, with the
// downloaded package so the caller can show the permission prompt.
func (a *Admin) updatePlugin(r *http.Request, pk PackageAdmin, name string, newPerms bool) (res updateResult, data []byte, sum PackageSummary, err error) {
	data, err = pk.Fetch(r.Context(), "plugin", name)
	if err != nil {
		return 0, nil, sum, err
	}
	sum, err = pk.Inspect(r.Context(), data)
	switch {
	case err != nil:
		return 0, nil, sum, err
	case sum.Problem != "":
		return 0, nil, sum, &Refusal{Msg: sum.Problem}
	case sum.Confirm != nil || newPerms:
		return needsReview, data, sum, nil
	}
	if err := pk.Install(r.Context(), data, "registry", nil, false); err != nil {
		return 0, nil, sum, err
	}
	return updated, data, sum, nil
}

// handlePluginUpdate is the Update button of one row. An update that needs
// review is not installed: the owner gets the permission prompt instead.
func (a *Admin) handlePluginUpdate(w http.ResponseWriter, r *http.Request) {
	pa, ok := a.pluginAdmin(w, r)
	if !ok {
		return
	}
	pk, ok := a.packageAdmin(w, r)
	if !ok {
		return
	}
	back, label := a.adminPath+"/plugins", "Back to plugins"
	name := r.PathValue("name")
	p, found := a.findPlugin(pa, name)
	if !found {
		a.ErrorBack(w, r, http.StatusNotFound, "Plugin not found", "That plugin is not installed.", back, label)
		return
	}
	if !takesUpdate(p) || p.Package.Update == "" {
		a.ErrorBack(w, r, http.StatusUnprocessableEntity, "No update", name+" has no update to install.", back, label)
		return
	}
	res, data, sum, err := a.updatePlugin(r, pk, name, len(p.Package.UpdateNew) > 0)
	if err != nil {
		a.refused(w, r, err, back, label)
		return
	}
	if res == needsReview {
		a.prompt(w, r, pk, data, "registry")
		return
	}
	a.invalidate(r.Context())
	a.SetFlash(w, r, "success", "Updated "+name+" to v"+sum.Version+".")
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// handlePluginUpdateAll updates every plugin whose update needs no review, one
// after the other, in name order. Plugins that need review (new permissions
// in the list, or a confirmation found on download) are left alone and named
// in the message, even when the run stops early. The first failure stops it.
func (a *Admin) handlePluginUpdateAll(w http.ResponseWriter, r *http.Request) {
	pa, ok := a.pluginAdmin(w, r)
	if !ok {
		return
	}
	pk, ok := a.packageAdmin(w, r)
	if !ok {
		return
	}
	// Plugins whose update adds permissions need review whatever the download
	// says, so they are set aside before anything is fetched: a failing
	// download of one of them cannot stop the safe updates.
	var safe, done, review []string
	for _, p := range pa.Plugins() {
		switch {
		case !takesUpdate(p) || p.Package.Update == "":
		case len(p.Package.UpdateNew) > 0:
			review = append(review, p.Name)
		default:
			safe = append(safe, p.Name)
		}
	}
	slices.Sort(safe)

	var failed error
	failedName, untried := "", 0
	for i, name := range safe {
		res, _, _, err := a.updatePlugin(r, pk, name, false)
		if err != nil {
			failed, failedName, untried = err, name, len(safe)-i-1
			break
		}
		if res == needsReview {
			review = append(review, name)
			continue
		}
		done = append(done, name)
	}
	slices.Sort(review)
	if len(done) > 0 {
		a.invalidate(r.Context())
	}
	kind, msg := "success", updateSummary(done, review)
	if failed != nil && len(done)+len(review) == 0 {
		msg = ""
	}
	if failed != nil {
		kind = "error"
		var ref *Refusal
		reason := "an unexpected error"
		if errors.As(failed, &ref) {
			reason = ref.Msg
		} else {
			a.logger().Error("plugin update failed", "plugin", failedName, "err", failed)
		}
		msg = strings.TrimSpace(fmt.Sprintf("%s Could not update %s: %s.", msg, failedName, strings.TrimSuffix(reason, ".")))
		if untried > 0 {
			msg += fmt.Sprintf(" Stopped; %d more not tried.", untried)
		}
	}
	a.SetFlash(w, r, kind, msg)
	http.Redirect(w, r, a.adminPath+"/plugins", http.StatusSeeOther)
}

// updateSummary is "Updated 2 plugins (a, b). 1 needs review: c."
func updateSummary(done, review []string) string {
	var parts []string
	if len(done) > 0 {
		parts = append(parts, fmt.Sprintf("Updated %d %s (%s).", len(done), plural(len(done), "plugin", "plugins"), strings.Join(done, ", ")))
	}
	if len(review) > 0 {
		parts = append(parts, fmt.Sprintf("%d %s review: %s.", len(review), plural(len(review), "needs", "need"), strings.Join(review, ", ")))
	}
	if len(parts) == 0 {
		return "Nothing to update."
	}
	return strings.Join(parts, " ")
}
