package reviewflow

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/authz"
	"hatamoto.dev/core/internal/authzctx"
	"hatamoto.dev/core/internal/changeset"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/reviewstate"
	"hatamoto.dev/core/internal/siteconfig"
)

// pendingError says whose change is waiting; it matches entity.ErrReviewPending.
type pendingError struct{ who string }

func (e pendingError) Error() string {
	if e.who == "" {
		return "Another person's change to this entry is waiting for review."
	}
	return e.who + "'s change to this entry is waiting for review."
}

func (pendingError) Is(target error) bool { return target == entity.ErrReviewPending }

// Plan implements entity.ReviewGate. Without a subject (a system caller) it
// holds nothing. A switch, authorizer or policy error is returned: the entity
// service refuses the write, it never falls open.
func (f *Flow) Plan(ctx context.Context, e *entity.Entity, kind entity.ReviewKind) (entity.ReviewPlan, error) {
	s, ok := authzctx.From(ctx)
	if !ok {
		return entity.ReviewPlan{}, nil
	}
	on, err := f.Switch.ReviewEnabled(ctx)
	if errors.Is(err, siteconfig.ErrNotConfigured) {
		// No settings row yet: nothing ever turned review on.
		on, err = false, nil
	}
	if err != nil {
		return entity.ReviewPlan{}, fmt.Errorf("failed to read the review switch: %w", err)
	}
	if !on && !reviewstate.Registered() {
		// Review off, no policy: nothing can hold the change, and the bypass
		// right is not asked (a registered authorizer may not know the action).
		return entity.ReviewPlan{}, nil
	}
	bypass, err := authz.Can(ctx, s, authz.ActionEntityReviewBypass, e.AuthzObject())
	if err != nil {
		return entity.ReviewPlan{}, fmt.Errorf("failed to check the review bypass: %w", err)
	}
	stages, err := reviewstate.StagesFor(ctx, reviewstate.Edit{
		Requester:     s.UserID,
		EntityID:      e.ID.String(),
		Schema:        schemaSlug(e),
		RequesterRole: requesterRole(s),
		Bypass:        bypass,
		ReviewEnabled: on,
	})
	if err != nil {
		return entity.ReviewPlan{}, err
	}
	p := entity.ReviewPlan{Held: len(stages) > 0}
	for _, st := range stages {
		p.Stages = append(p.Stages, entity.ReviewStage{Name: st.Name, Group: st.Group})
	}
	return p, nil
}

// Submit implements entity.ReviewGate: it stages the intended row as one entity
// item of an edit changeset, based on the row as it was read.
func (f *Flow) Submit(ctx context.Context, c entity.ReviewChange, p entity.ReviewPlan) (*entity.SentForReviewError, error) {
	s, ok := authzctx.From(ctx)
	if !ok || s.UserID == "" {
		return nil, errors.New("a change sent for review needs a signed-in requester")
	}
	e := c.Entity
	payload, err := jsonx.Marshal(stagedEntity{
		Schema: schemaSlug(e), Name: c.Name, Slug: c.Slug, Locale: c.Locale,
		Attributes: c.Attributes, ParentID: c.ParentID, Status: string(c.Status), PublishedAt: c.PublishedAt,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to encode the change: %w", err)
	}
	stages := make([]changeset.StageSpec, 0, len(p.Stages))
	for _, st := range p.Stages {
		stages = append(stages, changeset.StageSpec{Name: st.Name, Group: st.Group})
	}
	id, err := f.Engine.SubmitEdit(ctx, changeset.EditOptions{Requester: s.UserID, Stages: stages}, changeset.Item{
		Kind: changeset.KindEntity, Op: changeset.OpUpsert, TargetID: e.ID.String(),
		BaseToken: changeset.EntityToken(e.Version, e.UpdatedAt), Payload: payload,
	})
	if errors.Is(err, changeset.ErrEditPending) {
		return nil, f.pendingError(ctx, e.ID.String())
	}
	if err != nil {
		return nil, err
	}
	return &entity.SentForReviewError{ChangesetID: id, Since: f.now()}, nil
}

// stagedEntity is the payload of an entity item as the engine's entity writer
// reads it (changeset.entityPayload), without the import-only fields.
type stagedEntity struct {
	Schema      string         `json:"schema"`
	Name        string         `json:"name"`
	Slug        string         `json:"slug"`
	Locale      string         `json:"locale"`
	Attributes  jsontext.Value `json:"attributes"`
	ParentID    *uuid.UUID     `json:"parent_id"`
	Status      string         `json:"status"`
	PublishedAt *time.Time     `json:"published_at,omitempty"`
}

// pendingError names the other requester when the pending edit and the user can be read.
func (f *Flow) pendingError(ctx context.Context, target string) error {
	row, ok, err := f.Engine.PendingEdit(ctx, target)
	if err != nil || !ok {
		return pendingError{}
	}
	id, err := uuid.Parse(row.Requester)
	if err != nil || f.Users == nil {
		return pendingError{}
	}
	u, err := f.Users.GetByID(ctx, id)
	if err != nil || u == nil {
		return pendingError{}
	}
	if u.Name == nil {
		return pendingError{}
	}
	return pendingError{who: strings.TrimSpace(*u.Name)}
}

func schemaSlug(e *entity.Entity) string {
	if e.Schema == nil {
		return ""
	}
	return e.Schema.Slug
}

// requesterRole is the role the policy sees: the session's role, or a key's
// creator role. It is passed through, never compared.
func requesterRole(s authz.Subject) string {
	if s.KeyID != "" {
		return s.CreatorRole
	}
	return s.Role
}
