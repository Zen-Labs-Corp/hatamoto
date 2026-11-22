package entity

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/authz"
)

// ReviewKind says what a person asked for.
type ReviewKind string

// The requests the review gate sees.
const (
	ReviewPublish  ReviewKind = "publish"
	ReviewSchedule ReviewKind = "schedule"
	ReviewEdit     ReviewKind = "edit"
	ReviewRestore  ReviewKind = "restore"
)

// ReviewPlan is the gate's answer before the write: Held means stage, do not write.
type ReviewPlan struct {
	Held   bool
	Stages []ReviewStage
}

// ReviewStage is one review step of a plan; Group names who may decide it.
type ReviewStage struct{ Name, Group string }

// ReviewChange is the intended row: what the write would store, after
// validation and before_save.
type ReviewChange struct {
	Kind        ReviewKind
	Entity      *Entity // the row as read
	Name        string
	Slug        string
	Locale      string
	Attributes  jsontext.Value
	ParentID    *uuid.UUID
	Status      EntityStatus // published or scheduled
	PublishedAt *time.Time   // a scheduled request's time
}

// ReviewGate decides whether a person's change to live content waits for
// review and stages it. Submit returns the outcome error for the caller to
// pass on, or an error when the change could not be staged.
type ReviewGate interface {
	Plan(ctx context.Context, e *Entity, kind ReviewKind) (ReviewPlan, error)
	Submit(ctx context.Context, c ReviewChange, p ReviewPlan) (*SentForReviewError, error)
}

// SetReviewGate installs the review gate; nil, the default, writes every
// change directly. The gate is consulted only for a person (a subject on the
// context) changing live content.
func (s *Service) SetReviewGate(g ReviewGate) { s.review = g }

// Errors of the review gate.
var (
	// ErrSentForReview is the outcome of a change that was staged instead of written.
	ErrSentForReview = errors.New("sent for review")
	// ErrReviewPending means another person's change to this entry is waiting for review.
	ErrReviewPending = errors.New("another change to this entry is waiting for review")
	// ErrReviewUnsupported means a held change cannot be staged without losing part of it.
	ErrReviewUnsupported = errors.New("this change cannot be sent for review")
	// ErrReviewHold means the review policy failed, so the change is refused and nothing is written.
	ErrReviewHold = errors.New("the review policy refused the change")
)

// SentForReviewError reports that the change was staged and nothing live
// changed. Callers show it as a notice, not a failure.
type SentForReviewError struct {
	ChangesetID int64
	Since       time.Time
}

// Error returns the outcome as a sentence.
func (e *SentForReviewError) Error() string {
	return "sent for review: it goes live when a reviewer approves it"
}

// Unwrap lets errors.Is(err, ErrSentForReview) match.
func (e *SentForReviewError) Unwrap() error { return ErrSentForReview }

// live reports a status the public sees or will see (published or scheduled).
func live(st EntityStatus) bool { return st == StatusPublished || st == StatusScheduled }

// gateable reports whether a row's schema may be held for review: blocks, theme
// parts and persons keep their own status rules and are never staged by a status
// change (a person's Public flag goes through UpdateEntity).
func gateable(e *Entity) bool {
	return e.Schema == nil || (!IsReuseSchema(e.Schema) && e.Schema.Slug != personSchemaSlug)
}

// reviewPlan asks the gate about a person's write that touches live content.
// With no gate or no subject (a system caller) it answers "not held" without
// calling anything.
func (s *Service) reviewPlan(ctx context.Context, e *Entity, kind ReviewKind) (ReviewPlan, error) {
	if s.review == nil || !hasSubject(ctx) {
		return ReviewPlan{}, nil
	}
	p, err := s.review.Plan(ctx, e, kind)
	if err != nil {
		return ReviewPlan{}, fmt.Errorf("%w: %v", ErrReviewHold, err)
	}
	return p, nil
}

// authorizeReview asks what the plan requires: entity.review.request when the
// change is held, else act.
func authorizeReview(ctx context.Context, p ReviewPlan, act authz.Action, obj authz.Object) error {
	if p.Held {
		act = authz.ActionEntityReviewRequest
	}
	return Authorize(ctx, act, obj)
}

// settleLockedPlan revisits the plan of UpdateEntity and RestoreVersion once
// the locked status is known, and finishes the authorization. A stored draft
// whose locked status goes live (a person's Public flag) is a publish and is
// planned now; a held change whose locked status leaves the live states is an
// unpublish and is written directly. plain is the action asked when the change
// is not held. machine marks writes the gate never sees.
func (s *Service) settleLockedPlan(ctx context.Context, e *Entity, locked *EntityStatus, plan ReviewPlan, plain authz.Action, machine bool) (ReviewPlan, error) {
	if locked == nil {
		return plan, nil
	}
	wasHeld := plan.Held
	if !machine {
		switch {
		case plan.Held && !live(*locked):
			plan = ReviewPlan{}
		case !plan.Held && !live(e.Status) && live(*locked):
			var err error
			if plan, err = s.reviewPlan(ctx, e, ReviewPublish); err != nil {
				return ReviewPlan{}, err
			}
		}
	}
	if plan.Held {
		if wasHeld {
			return plan, nil
		}
		return plan, Authorize(ctx, authz.ActionEntityReviewRequest, e.AuthzObject())
	}
	if wasHeld {
		if err := Authorize(ctx, plain, e.AuthzObject()); err != nil {
			return plan, err
		}
	}
	return plan, authorizeLocked(ctx, e.Status, locked, e.AuthzObject())
}

// unsupportedForReview reports input fields a staged change cannot carry: the
// byline and author fields, metadata, a cleared parent, and autosave (which
// never stages).
func unsupportedForReview(in *UpdateEntityInput) bool {
	return in.Autosave || in.Metadata != nil || in.AuthorID != nil || in.ClearAuthor ||
		in.BylinePersonID != nil || in.ClearBylinePerson || in.BylineOrg != nil || in.ClearParent
}

// sendForReview stages c through the gate and returns the outcome as the error
// callers pass on: a *SentForReviewError, or why it could not be staged.
func (s *Service) sendForReview(ctx context.Context, c ReviewChange, p ReviewPlan) error {
	sent, err := s.review.Submit(ctx, c, p)
	if err != nil {
		return err
	}
	if sent == nil {
		return errors.New("review gate staged the change without an outcome")
	}
	return sent
}

// heldChange is the row e as it is, to be staged with status st.
func heldChange(kind ReviewKind, e *Entity, st EntityStatus) ReviewChange {
	return ReviewChange{Kind: kind, Entity: e, Name: e.Name, Slug: e.Slug, Locale: e.Locale,
		Attributes: e.Attributes, ParentID: e.ParentID, Status: st, PublishedAt: e.PublishedAt}
}

// holdUpdate stages an UpdateEntity as the row it would have written: the
// input over the stored values, with the locked or stored status. Nothing is
// written. A stale ExpectedVersion is refused as the write would refuse it.
func (s *Service) holdUpdate(ctx context.Context, e *Entity, in *UpdateEntityInput, attrs jsontext.Value, plan ReviewPlan) error {
	if in.ExpectedVersion != nil && *in.ExpectedVersion != e.Version {
		return &VersionConflictError{Current: e.Version}
	}
	kind, st := ReviewEdit, e.Status
	if !live(e.Status) {
		kind = ReviewPublish
	}
	if in.Status != nil {
		st = *in.Status
	}
	c := heldChange(kind, e, st)
	c.Attributes = attrs
	if in.Name != nil {
		c.Name = *in.Name
	}
	if in.Slug != nil {
		c.Slug = *in.Slug
	}
	if in.Locale != nil {
		c.Locale = *in.Locale
	}
	if in.ParentID != nil {
		c.ParentID = in.ParentID
	}
	return s.sendForReview(ctx, c, plan)
}
