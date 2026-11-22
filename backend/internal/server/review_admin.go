package server

import (
	"context"
	"encoding/json/jsontext"
	"strings"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/reviewflow"
	"hatamoto.dev/core/internal/siteconfig"
)

// reviewAdmin is the admin's view of the review flow: the site's switch and
// the change waiting for one entry.
type reviewAdmin struct {
	flow *reviewflow.Flow
	site *siteconfig.Store
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
	Attributes jsontext.Value `json:"attributes"`
}

func (a reviewAdmin) Pending(ctx context.Context, entityID string) (admin.PendingEdit, bool, error) {
	row, ok, err := a.flow.Engine.PendingEdit(ctx, entityID)
	if err != nil || !ok {
		return admin.PendingEdit{}, false, err
	}
	p := admin.PendingEdit{
		ID: row.ID, EntityID: entityID, EntityName: row.Name, Requester: row.Requester,
		RequesterName: a.userName(ctx, row.Requester), Since: row.CreatedAt.UTC(), Rejected: row.Rejected,
	}
	_, items, err := a.flow.Engine.Get(ctx, row.ID)
	if err != nil {
		return admin.PendingEdit{}, false, err
	}
	if len(items) > 0 {
		var sp stagedPayload
		if err := jsonx.Unmarshal(items[0].Payload, &sp); err == nil {
			p.Payload = &entity.Entity{Name: sp.Name, Slug: sp.Slug, Attributes: sp.Attributes}
		}
	}
	if row.Rejected {
		ds, err := a.flow.Engine.StageDecisions(ctx, row.ID)
		if err != nil {
			return admin.PendingEdit{}, false, err
		}
		for _, d := range ds {
			if d.Decision == "rejected" {
				p.Comment, p.Reviewer = d.Comment, a.userName(ctx, d.Reviewer)
			}
		}
	}
	return p, true, nil
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
