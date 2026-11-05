package admin

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/redirect"
)

// Audit actions for the scheduled unpublish an owner sets or removes.
const (
	auditEntityUnpublishSchedule = "entity.unpublish_schedule"
	auditEntityUnpublishClear    = "entity.unpublish_clear"
)

// UnpublishStore reads and writes an entity's scheduled unpublish. Nil in
// ContentDeps hides the fields.
type UnpublishStore interface {
	SetUnpublish(ctx context.Context, id uuid.UUID, at time.Time, target string) error
	ClearUnpublish(ctx context.Context, id uuid.UUID) error
	UnpublishFor(ctx context.Context, id uuid.UUID) (*entity.Unpublish, error)
	UnpublishMap(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]entity.Unpublish, error)
}

// readUnpublish reads the posted unpublish fields into f. A form that does
// not carry the field (a script, the builder) leaves the schedule alone.
func readUnpublish(r *http.Request, f *entityForm) {
	if _, ok := r.PostForm["unpublish_at"]; !ok {
		return
	}
	f.UnpublishPosted = true
	f.UnpublishAt = strings.TrimSpace(r.PostFormValue("unpublish_at"))
	f.UnpublishRedirect = strings.TrimSpace(r.PostFormValue("unpublish_redirect"))
	if r.PostFormValue("unpublish_clear") != "" {
		f.UnpublishAt, f.UnpublishRedirect = "", ""
	}
}

// fillUnpublish shows the stored schedule when the form did not post one.
func (a *Admin) fillUnpublish(ctx context.Context, f *entityForm, self *entity.Entity) {
	st := a.deps.Content.Unpublish
	if st == nil || self == nil || f.UnpublishPosted {
		return
	}
	if u, err := st.UnpublishFor(ctx, self.ID); err == nil && u != nil {
		f.UnpublishAt = u.At.In(a.siteLocation(ctx)).Format(scheduleLayout)
		f.UnpublishRedirect = u.RedirectTarget
	}
}

// unpublishErrors validates the unpublish fields of the posted form into
// f.Errors. A time equal to the stored one stays valid even once it has passed.
func (a *Admin) unpublishErrors(r *http.Request, f *entityForm, current *entity.Entity) {
	if !f.UnpublishPosted || a.deps.Content.Unpublish == nil {
		return
	}
	ctx := r.Context()
	if f.UnpublishAt == "" {
		if f.UnpublishRedirect != "" {
			f.Errors["unpublish_redirect"] = "Pick the date and time to unpublish first"
		}
		return
	}
	loc := a.siteLocation(ctx)
	at, err := time.ParseInLocation(scheduleLayout, f.UnpublishAt, loc)
	switch {
	case err != nil:
		f.Errors["unpublish_at"] = "Enter a valid date and time"
		return
	case at.Format(scheduleLayout) != f.UnpublishAt:
		f.Errors["unpublish_at"] = "That time does not exist in " + loc.String() + " (the clocks change then). Pick another"
		return
	}
	if !at.After(time.Now()) {
		unchanged := false
		if current != nil {
			if u, err := a.deps.Content.Unpublish.UnpublishFor(ctx, current.ID); err == nil && u != nil && u.At.Equal(at) {
				unchanged = true
			}
		}
		if !unchanged {
			f.Errors["unpublish_at"] = "Pick a time in the future"
			return
		}
	}
	if f.Status == string(entity.StatusScheduled) {
		if pub, err := time.ParseInLocation(scheduleLayout, f.Schedule, loc); err == nil && !at.After(pub) {
			f.Errors["unpublish_at"] = "Pick a time after the publish time"
			return
		}
	}
	if t := f.UnpublishRedirect; t != "" && !redirect.SafeLocation(t, true) {
		f.Errors["unpublish_redirect"] = "Use a path starting with / or a full https address"
	}
}

// saveUnpublish stores or clears the schedule after a save and returns a note
// for the flash message when it could not be done.
func (a *Admin) saveUnpublish(r *http.Request, p *posted, e *entity.Entity, schema string) string {
	st := a.deps.Content.Unpublish
	f := p.f
	if st == nil || !f.UnpublishPosted {
		return ""
	}
	ctx := r.Context()
	stored, _ := st.UnpublishFor(ctx, e.ID)
	if f.UnpublishAt == "" {
		if stored == nil {
			return ""
		}
		if err := st.ClearUnpublish(ctx, e.ID); err != nil {
			a.logger().Error("clear scheduled unpublish failed", "err", err)
			return "The scheduled unpublish could not be removed."
		}
		a.auditEntity(r, auditEntityUnpublishClear, e.ID, e, map[string]any{"schema": schema})
		return ""
	}
	at, err := time.ParseInLocation(scheduleLayout, f.UnpublishAt, a.siteLocation(ctx))
	if err != nil {
		return ""
	}
	if stored != nil && stored.At.Equal(at) && stored.RedirectTarget == f.UnpublishRedirect {
		return ""
	}
	if err := st.SetUnpublish(ctx, e.ID, at, f.UnpublishRedirect); err != nil {
		a.logger().Error("schedule unpublish failed", "err", err)
		return "The scheduled unpublish could not be saved."
	}
	a.auditEntity(r, auditEntityUnpublishSchedule, e.ID, e, map[string]any{
		"schema": schema, "unpublish_at": at.UTC().Format(time.RFC3339), "redirect": f.UnpublishRedirect})
	return ""
}

// unpublishCell is the status text of a published row that is scheduled to
// end, "" when it is not.
func unpublishCell(u entity.Unpublish, loc *time.Location) string {
	return "Published, ends " + u.At.In(loc).Format("2 Jan")
}
