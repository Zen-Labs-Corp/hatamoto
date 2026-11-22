package server

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"strings"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/changeset"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/reviewflow"
	"hatamoto.dev/core/internal/reviewstate"
	"hatamoto.dev/core/internal/siteconfig"
)

// reviewAdmin is the admin's view of the review flow: the site's switch and
// the change waiting for one entry.
type reviewAdmin struct {
	flow   *reviewflow.Flow
	engine *changeset.Engine // the queue and detail reads; the flow holds the narrow interface
	site   *siteconfig.Store
}

// Enabled reads the cached settings; the gate itself reads the switch uncached.
func (a reviewAdmin) Enabled(ctx context.Context) bool {
	c, err := a.site.Get(ctx)
	return err == nil && c.ReviewEnabled
}

func (a reviewAdmin) SetEnabled(ctx context.Context, on bool) error {
	return a.site.SetReviewEnabled(ctx, on)
}

// Plan reports whether a change by the signed-in viewer would wait. A policy or
// switch error reads as "waits": the buttons say Send for review, the server
// decides on the write.
func (a reviewAdmin) Plan(ctx context.Context, e *entity.Entity) bool {
	p, err := a.flow.Plan(ctx, e, entity.ReviewEdit)
	return err != nil || p.Held
}

// stagedPayload is the part of an entity item's payload the editors show.
type stagedPayload struct {
	Name       string         `json:"name"`
	Slug       string         `json:"slug"`
	Status     string         `json:"status"`
	Attributes jsontext.Value `json:"attributes"`
}

func (a reviewAdmin) Pending(ctx context.Context, entityID string) (admin.PendingEdit, bool, error) {
	row, ok, err := a.flow.Engine.PendingEdit(ctx, entityID)
	if err != nil || !ok {
		return admin.PendingEdit{}, false, err
	}
	_, items, err := a.flow.Engine.Get(ctx, row.ID)
	if err != nil {
		return admin.PendingEdit{}, false, err
	}
	p, err := a.pendingEdit(ctx, row, items)
	if err == nil && len(items) > 0 && a.flow.Entities != nil {
		if eid, perr := uuid.Parse(items[0].TargetID); perr == nil {
			if live, gerr := a.flow.Entities.GetEntity(ctx, eid); gerr == nil {
				p.Stale = changeset.EntityToken(live.Version, live.UpdatedAt) != items[0].BaseToken
			}
		}
	}
	return p, err == nil, err
}

// pendingEdit is row as the admin reads it: who sent it, the payload the
// requester left and, for a sent-back change, the reviewer's comment.
func (a reviewAdmin) pendingEdit(ctx context.Context, row changeset.EditRow, items []changeset.StoredItem) (admin.PendingEdit, error) {
	p := admin.PendingEdit{
		ID: row.ID, EntityID: row.TargetID, EntityName: row.Name, Requester: row.Requester,
		RequesterName: a.userName(ctx, row.Requester), Since: row.CreatedAt.UTC(), Rejected: row.Rejected,
	}
	if len(items) > 0 {
		var sp stagedPayload
		if err := jsonx.Unmarshal(items[0].Payload, &sp); err == nil {
			p.Payload = &entity.Entity{Name: sp.Name, Slug: sp.Slug, Status: entity.EntityStatus(sp.Status), Attributes: sp.Attributes}
		}
	}
	if row.Rejected {
		ds, err := a.flow.Engine.StageDecisions(ctx, row.ID)
		if err != nil {
			return admin.PendingEdit{}, err
		}
		for _, d := range ds {
			if d.Decision == "rejected" {
				p.Comment, p.Reviewer = d.Comment, a.userName(ctx, d.Reviewer)
			}
		}
	}
	return p, nil
}

// Queue lists the changes waiting for a decision, oldest first.
func (a reviewAdmin) Queue(ctx context.Context, limit, offset int) ([]admin.PendingEdit, int, error) {
	rows, err := a.engine.ListEdits(ctx, changeset.EditFilter{Pending: true}, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	total, err := a.engine.CountPendingEdits(ctx)
	if err != nil {
		return nil, 0, err
	}
	out := make([]admin.PendingEdit, 0, len(rows))
	for _, r := range rows {
		out = append(out, admin.PendingEdit{ID: r.ID, EntityID: r.TargetID, EntityName: r.Name, Requester: r.Requester,
			RequesterName: a.userName(ctx, r.Requester), Since: r.CreatedAt.UTC()})
	}
	return out, total, nil
}

func (a reviewAdmin) Count(ctx context.Context) (int, error) {
	return a.engine.CountPendingEdits(ctx)
}

// Detail reads one staged edit. A closed or unknown changeset is not found.
func (a reviewAdmin) Detail(ctx context.Context, id int64) (admin.ReviewDetail, error) {
	h, items, err := a.flow.Engine.Get(ctx, id)
	switch {
	case errors.Is(err, changeset.ErrNotFound):
		return admin.ReviewDetail{}, admin.ErrReviewNotFound
	case err != nil:
		return admin.ReviewDetail{}, err
	case h.Kind != changeset.ChangesetEdit:
		return admin.ReviewDetail{}, admin.ErrReviewNotFound
	case h.State != changeset.StateStaged || len(items) == 0:
		return admin.ReviewDetail{}, admin.ErrReviewNotStaged
	}
	row, ok, err := a.flow.Engine.PendingEdit(ctx, items[0].TargetID)
	if err != nil {
		return admin.ReviewDetail{}, err
	}
	if !ok || row.ID != id {
		return admin.ReviewDetail{}, admin.ErrReviewNotStaged
	}
	p, err := a.pendingEdit(ctx, row, items)
	if err != nil {
		return admin.ReviewDetail{}, err
	}
	d := admin.ReviewDetail{PendingEdit: p}
	if eid, err := uuid.Parse(items[0].TargetID); err == nil && a.flow.Entities != nil {
		if live, err := a.flow.Entities.GetEntity(ctx, eid); err == nil {
			d.Live = live
			d.Stale = changeset.EntityToken(live.Version, live.UpdatedAt) != items[0].BaseToken
		}
	}
	switch {
	case row.Rejected:
	case row.Approvals >= len(row.Stages):
		d.Approved = true
	case row.Stages[row.Approvals].Group != reviewstate.DefaultGroup:
		d.NeedsGroup = row.Stages[row.Approvals].Group
	}
	return d, nil
}

func (a reviewAdmin) Approve(ctx context.Context, id int64, comment string) (admin.ReviewOutcome, error) {
	o, err := a.flow.Approve(ctx, id, comment)
	return outcome(o), reviewError(err)
}

func (a reviewAdmin) Publish(ctx context.Context, id int64) (admin.ReviewOutcome, error) {
	o, err := a.flow.Publish(ctx, id)
	return outcome(o), reviewError(err)
}

func (a reviewAdmin) SendBack(ctx context.Context, id int64, comment string) error {
	return reviewError(a.flow.SendBack(ctx, id, comment))
}

func (a reviewAdmin) Withdraw(ctx context.Context, id int64) error {
	return reviewError(a.flow.Withdraw(ctx, id))
}

func (a reviewAdmin) Discard(ctx context.Context, id int64) error {
	return reviewError(a.flow.Discard(ctx, id))
}

func outcome(o reviewflow.Outcome) admin.ReviewOutcome {
	return admin.ReviewOutcome{Published: o.Published, NotApplied: o.NotApplied, Pending: o.Pending}
}

// reviewError maps the flow's and the engine's refusals to the admin's.
func reviewError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, changeset.ErrSelfDecision):
		return admin.ErrReviewSelf
	case errors.Is(err, reviewflow.ErrNotDecider), errors.Is(err, reviewflow.ErrUnknownGroup):
		return admin.ErrReviewNotDecider
	case errors.Is(err, reviewflow.ErrEditStale):
		return admin.ErrReviewStale
	case errors.Is(err, reviewflow.ErrCommentNeeded):
		return admin.ErrReviewNoComment
	case errors.Is(err, reviewflow.ErrNotRequester):
		return admin.ErrReviewNotRequester
	case errors.Is(err, changeset.ErrNotFound):
		return admin.ErrReviewNotFound
	case errors.Is(err, changeset.ErrNotStaged), errors.Is(err, changeset.ErrNotEdit), errors.Is(err, changeset.ErrStageRejected):
		return admin.ErrReviewNotStaged
	}
	return err
}

// userName is the person's display name, else their address; empty when unknown.
func (a reviewAdmin) userName(ctx context.Context, id string) string {
	uid, err := uuid.Parse(id)
	if err != nil || a.flow.Users == nil {
		return ""
	}
	u, err := a.flow.Users.GetByID(ctx, uid)
	if err != nil || u == nil {
		return ""
	}
	if u.Name != nil && strings.TrimSpace(*u.Name) != "" {
		return strings.TrimSpace(*u.Name)
	}
	return u.Email
}
