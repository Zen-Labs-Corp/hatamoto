package admin

import (
	"net/http"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/entity"
)

// Audit actions for explicit entity changes made in the admin. Autosave
// (blog_editor.go), draft auto-create (entity_draft.go) and the builder's
// per-keystroke saves are deliberately not audited: they would flood the log.
// MCP, API and scheduled publishes have no admin request, so they are not
// audited here either.
const (
	auditEntityPublish        = "entity.publish"
	auditEntityUnpublish      = "entity.unpublish"
	auditEntityArchive        = "entity.archive"
	auditEntityUpdate         = "entity.update"
	auditEntitySchedule       = "entity.schedule"
	auditEntityDelete         = "entity.delete"
	auditEntityRestoreVersion = "entity.restore_version"
	auditEntityCreateCopy     = "entity.create_copy"
)

// auditStatusAction names the audit action of a move to st: published is a
// publish, archived an archive, anything else (draft) an unpublish.
func auditStatusAction(st entity.EntityStatus) string {
	switch st {
	case entity.StatusPublished:
		return auditEntityPublish
	case entity.StatusArchived:
		return auditEntityArchive
	}
	return auditEntityUnpublish
}

// auditEntity records an action on an entity. The target is the entity id;
// name, slug and schema are added when the entity is known, so the log stays
// readable after a delete. Call it only after the store call succeeded.
func (a *Admin) auditEntity(r *http.Request, action string, id uuid.UUID, e *entity.Entity, detail map[string]any) {
	if detail == nil {
		detail = map[string]any{}
	}
	if e != nil {
		detail["name"], detail["slug"] = e.Name, e.Slug
		if _, ok := detail["schema"]; !ok && e.Schema != nil {
			detail["schema"] = e.Schema.Slug
		}
	}
	a.audit(r, action, id.String(), detail)
}

// entityForAudit is a best-effort read of an entity before it is deleted; nil
// when it cannot be read, in which case the audit row carries the id only.
func (a *Admin) entityForAudit(r *http.Request, id uuid.UUID) *entity.Entity {
	if a.deps.Audit == nil {
		return nil
	}
	e, err := a.deps.Content.Entities.GetEntity(r.Context(), id)
	if err != nil {
		return nil
	}
	return e
}
