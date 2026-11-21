package admin

import (
	"context"
	"net/http"

	"github.com/google/uuid"

	"hatamoto.dev/core/audit"
	"hatamoto.dev/core/internal/auditctx"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/middleware"
)

// Audit actions for explicit entity changes made in the admin. Saves, status
// changes, schedules, deletes and creates from the entity forms, lists and
// editors are recorded by the entity store in the write's own transaction
// (see entityAuditCtx). Autosave and draft auto-create are deliberately not
// audited: they would flood the log. The one save or autosave that takes a
// plugin's page over also writes entity.take_over (once per page: the next
// save finds it the owner's).
const (
	auditEntityPublish        = "entity.publish"
	auditEntityUnpublish      = "entity.unpublish"
	auditEntityArchive        = "entity.archive"
	auditEntityUpdate         = "entity.update"
	auditEntitySchedule       = "entity.schedule"
	auditEntityDelete         = "entity.delete"
	auditEntityRestoreVersion = "entity.restore_version"
	auditEntityCreateCopy     = "entity.create_copy"
	auditEntityDuplicate      = "entity.duplicate"
	auditEntityHandBack       = "entity.hand_back"
	auditEntityTakeOver       = "entity.take_over"
	auditEntityReorder        = "entity.reorder"
)

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

// entityDeleteDetail adds the entity's schema to extra before it is deleted:
// the store's row carries name and slug, and the schema is gone with the
// entity. Best effort; without it the row simply has no schema.
func (a *Admin) entityDeleteDetail(r *http.Request, id uuid.UUID, extra map[string]any) map[string]any {
	if a.deps.Audit == nil {
		return extra
	}
	e, err := a.deps.Content.Entities.GetEntity(r.Context(), id)
	if err != nil || e.Schema == nil {
		return extra
	}
	if extra == nil {
		extra = map[string]any{}
	}
	extra["schema"] = e.Schema.Slug
	return extra
}

// entityAuditCtx returns the request context carrying the signed-in user as
// the audit actor, so the entity store records the write in its own
// transaction (a registered audit sink can refuse it). Use it for ONE store
// call; extra is merged into the row's detail (schema, from, bulk, locale).
func (a *Admin) entityAuditCtx(r *http.Request, extra map[string]any) context.Context {
	u := userFrom(r.Context())
	if u == nil {
		return r.Context()
	}
	return auditctx.With(r.Context(), auditctx.Actor{
		ID: u.ID, Name: u.Email, Kind: audit.KindUser,
		IP: middleware.GetClientIP(r), UserAgent: r.UserAgent(), RequestID: r.Header.Get("X-Request-ID"),
		Detail: extra,
	})
}
