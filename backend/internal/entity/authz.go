package entity

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"hatamoto.dev/core/authz"
	"hatamoto.dev/core/internal/authzctx"
)

// ErrNotAllowed is returned when the caller on the context may not make the change.
var ErrNotAllowed = errors.New("not allowed")

// NotAllowedError names the refused change in words a person can act on.
type NotAllowedError struct {
	Action authz.Action
	// Err is the authorizer's error when it failed rather than said no.
	Err error
}

// Error returns the refusal as a sentence for the person who asked.
func (e *NotAllowedError) Error() string {
	switch e.Action {
	case authz.ActionEntityPublish:
		return "you cannot publish this entry: an editor or reviewer can publish it"
	case authz.ActionEntityUpdate:
		return "you cannot change this entry: authors edit their own content"
	case authz.ActionEntityCreate:
		return "you cannot create entries"
	case authz.ActionEntityDelete:
		return "you cannot delete this entry"
	case authz.ActionEntityUnpublish:
		return "you cannot unpublish this entry"
	case authz.ActionEntityVersionRestore:
		return "you cannot restore versions of this entry"
	}
	return "you cannot make this change"
}

// Unwrap lets errors.Is(err, ErrNotAllowed) match.
func (e *NotAllowedError) Unwrap() error { return ErrNotAllowed }

// AuthzObject describes e to the authorizer. CreatedBy is the creating user, not the byline.
func (e *Entity) AuthzObject() authz.Object {
	o := authz.Object{Kind: "entity", ID: e.ID.String(), Locale: e.Locale, Status: string(e.Status)}
	if e.Schema != nil {
		o.Schema = e.Schema.Slug
	}
	if e.CreatedBy != nil {
		o.CreatedBy = e.CreatedBy.String()
	}
	return o
}

// Authorize asks whether the subject on ctx may perform act on obj. No subject means a system caller
// (scheduler, workers, the CLI without --as, imports): allowed, as before. An authorizer error refuses.
func Authorize(ctx context.Context, act authz.Action, obj authz.Object) error {
	s, ok := authzctx.From(ctx)
	if !ok {
		return nil
	}
	allowed, err := authz.Can(ctx, s, act, obj)
	if err != nil {
		return &NotAllowedError{Action: act, Err: err}
	}
	if !allowed {
		return &NotAllowedError{Action: act}
	}
	return nil
}

func hasSubject(ctx context.Context) bool {
	_, ok := authzctx.From(ctx)
	return ok
}

// statusAction is the action a status change needs: into published or scheduled is a publish, out of them an
// unpublish, anything else an update.
func statusAction(from, to EntityStatus) authz.Action {
	live := func(s EntityStatus) bool { return s == StatusPublished || s == StatusScheduled }
	switch {
	case live(to):
		return authz.ActionEntityPublish
	case live(from):
		return authz.ActionEntityUnpublish
	}
	return authz.ActionEntityUpdate
}

// authorizeStored loads the entity and asks whether the subject on ctx may perform act on it. Without a
// subject it does nothing (no read).
func (s *Service) authorizeStored(ctx context.Context, id uuid.UUID, act authz.Action) error {
	if !hasSubject(ctx) {
		return nil
	}
	e, err := s.entityRepo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	return Authorize(ctx, act, e.AuthzObject())
}

// authorizeLocked asks for the publish or unpublish a locked status (a person follows Public) implies, so
// that route cannot publish what entity.publish would refuse. A nil locked status or no live change asks nothing.
func authorizeLocked(ctx context.Context, from EntityStatus, locked *EntityStatus, obj authz.Object) error {
	if locked == nil {
		return nil
	}
	if act := statusAction(from, *locked); act != authz.ActionEntityUpdate {
		return Authorize(ctx, act, obj)
	}
	return nil
}
