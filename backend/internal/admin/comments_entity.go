package admin

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"hatamoto.dev/core/internal/admin/ui"
)

// centityFormID is the id of the form the editors render after their own
// form, so the card's select and button can post it with form= (no nesting).
const centityFormID = "hm-commentsform"

// commentsPanel is the Comments card of the entity editors.
type commentsPanel struct {
	EntityID  string
	CardID    string // "comments"; the blog editor's is "be-comments"
	FormID    string
	Back      string // where the POST returns to
	Summary   string // "4 comments (1 pending)"; empty when there are none
	QueueHref string
	Options   []ui.Option
}

// centityModeOptions are the choices of a mode select; value "" inherits.
func centityModeOptions(override, inherit string) []ui.Option {
	opts := []ui.Option{
		{Value: "", Label: inherit},
		{Value: "open", Label: "Open"},
		{Value: "closed", Label: "Closed (no new comments)"},
		{Value: "hidden", Label: "Hidden"},
	}
	for i := range opts {
		opts[i].Selected = opts[i].Value == override
	}
	return opts
}

func centityPlural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// commentsPanel builds the card for the entity, or nil when no comment service
// is wired, the site switch is off, the entity is new, or a lookup fails (a
// failed lookup hides the card, not the editor).
func (a *Admin) commentsPanel(r *http.Request, entityID string) *commentsPanel {
	svc := a.deps.Comments
	if svc == nil || entityID == "" {
		return nil
	}
	s, err := svc.Settings(r.Context())
	if err != nil || !s.Enabled {
		return nil
	}
	ec, err := svc.EntityComments(r.Context(), entityID)
	if err != nil {
		a.logger().Error("entity comments lookup failed", "entity", entityID, "err", err)
		return nil
	}
	p := &commentsPanel{
		EntityID:  entityID,
		CardID:    "comments",
		FormID:    centityFormID,
		Back:      a.adminPath + "/entities/" + entityID,
		QueueHref: a.adminPath + "/comments?entity=" + url.QueryEscape(entityID) + "&status=pending",
		Options:   centityModeOptions(ec.Override, "Inherit ("+ec.Resolved+")"),
	}
	if ec.Total > 0 {
		p.Summary = fmt.Sprintf("%s (%d pending)", centityPlural(ec.Total, "comment", "comments"), ec.Pending)
	}
	return p
}

// handleEntityComments saves the per-entity comment mode. It is its own POST:
// no new version, no interference with the editor's save or autosave.
func (a *Admin) handleEntityComments(w http.ResponseWriter, r *http.Request) {
	svc := a.deps.Comments
	if svc == nil {
		a.unavailable(w, r, "Comments")
		return
	}
	uid, ok := uuidParam(r, "id")
	if !ok {
		a.notFound(w, r, "Entity", a.adminPath+"/entities", "Back to entities")
		return
	}
	id := uid.String()
	mode := r.PostFormValue("comments_mode")
	if !validCommentMode(mode) {
		a.Error(w, r, http.StatusUnprocessableEntity, "Unknown setting", "Choose inherit, open, closed or hidden.")
		return
	}
	prev, err := svc.EntityComments(r.Context(), id)
	if errors.Is(err, ErrCommentNotFound) {
		a.notFound(w, r, "Entity", a.adminPath+"/entities", "Back to entities")
		return
	}
	if err != nil {
		a.serverError(w, r, "The comment setting could not be read", err)
		return
	}
	if err := svc.SetEntityMode(r.Context(), id, mode); err != nil {
		a.serverError(w, r, "The comment setting could not be saved", err)
		return
	}
	a.audit(r, "comments.entity-mode", id, map[string]any{"old": prev.Override, "new": mode})
	a.SetFlash(w, r, "success", "Comment setting saved.")
	Redirect(w, r, backTo(r, a.adminPath+"/entities", a.adminPath+"/entities/"+id))
}

// handleSchemaComments is a stub until its screen lands.
func (a *Admin) handleSchemaComments(w http.ResponseWriter, r *http.Request) {
	a.unavailable(w, r, "Comments")
}
