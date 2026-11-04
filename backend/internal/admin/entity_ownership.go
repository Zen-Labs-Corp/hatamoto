package admin

import (
	"errors"
	"fmt"
	"net/http"

	"hatamoto.dev/core/internal/changeset"
)

// handleEntityHandBack gives a page a person took over back to the plugin that
// imported it. It changes no content and no site setting, so it needs no sudo.
func (a *Admin) handleEntityHandBack(w http.ResponseWriter, r *http.Request) {
	own := a.deps.Content.Ownership
	if own == nil {
		a.notFound(w, r, "Entity", a.adminPath+"/entities", "Back to entities")
		return
	}
	e, _, ok := a.loadEntity(w, r)
	if !ok {
		return
	}
	back := a.adminPath + "/entities/" + e.ID.String()
	plugin, err := own.HandBack(r.Context(), e.ID)
	switch {
	case errors.Is(err, changeset.ErrNotHandedOver):
		a.SetFlash(w, r, "error", "A plugin already tracks this page.")
	case errors.Is(err, changeset.ErrNoMapping):
		a.SetFlash(w, r, "error", "No plugin imported this page, so there is nothing to hand back.")
	case err != nil:
		a.serverError(w, r, "The page could not be handed back", err)
		return
	default:
		a.auditEntity(r, auditEntityHandBack, e.ID, e, map[string]any{"plugin": plugin})
		a.SetFlash(w, r, "success", fmt.Sprintf("%s will track this page again.", plugin))
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}
