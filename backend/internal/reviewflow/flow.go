// Package reviewflow routes people's changes to published content through
// review: it decides whether a change waits (the entity service's gate), stages
// it as an edit changeset, and records reviewers' decisions.
//
// The review policy (hatamoto.dev/core/review) is asked with SlugChange false:
// the plan is taken before the input is known, and v1 needs no slug rule.
package reviewflow

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/changeset"
	"hatamoto.dev/core/internal/user"
)

// Switch reads the site's review switch. It must not cache: the gate asks it on
// every gated write, so a switch turned on takes effect on every server at once.
type Switch interface {
	ReviewEnabled(ctx context.Context) (bool, error)
}

// Engine is the part of *changeset.Engine the flow uses.
type Engine interface {
	SubmitEdit(ctx context.Context, o changeset.EditOptions, it changeset.Item) (int64, error)
	PendingEdit(ctx context.Context, targetID string) (changeset.EditRow, bool, error)
}

// UserReader reads a user, to name the person whose change is waiting.
type UserReader interface {
	GetByID(ctx context.Context, id uuid.UUID) (*user.User, error)
}

// Flow is the review gate of the entity service and, with the decisions, the
// service behind the review screens.
type Flow struct {
	Switch Switch
	Engine Engine
	Users  UserReader
	Now    func() time.Time
	Logger *slog.Logger
}

func (f *Flow) now() time.Time {
	if f.Now != nil {
		return f.Now()
	}
	return time.Now().UTC()
}

func (f *Flow) logger() *slog.Logger {
	if f.Logger != nil {
		return f.Logger
	}
	return slog.Default()
}
