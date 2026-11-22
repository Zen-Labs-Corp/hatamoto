package review

import (
	"context"

	"hatamoto.dev/core/internal/reviewstate"
)

// Default stage used when no policy is registered and review is on.
const (
	DefaultStageName = reviewstate.DefaultStageName
	DefaultGroup     = reviewstate.DefaultGroup
)

// Edit describes a human edit that is about to be staged.
type Edit = reviewstate.Edit

// Stage is one ordered review step; Group names who may decide it.
type Stage = reviewstate.Stage

// Policy decides the ordered review stages of an edit. Return no stages for
// an edit that needs no review.
type Policy = reviewstate.Policy

// ErrPolicy means the policy refused or failed. The caller must hold the
// edit and never publish it.
var ErrPolicy = reviewstate.ErrPolicy

// Register sets the review policy. Call it from an init function. It panics
// when called after app.Main started, a second time, or with a nil policy.
func Register(p Policy) { reviewstate.Register(p) }

// StagesFor returns the ordered review stages of e. A returned error means
// "hold the edit, never publish it" (fail closed).
func StagesFor(ctx context.Context, e Edit) ([]Stage, error) {
	return reviewstate.StagesFor(ctx, e)
}

// GroupResolver says who may decide a stage whose Group is not DefaultGroup.
type GroupResolver = reviewstate.GroupResolver

// RegisterGroups sets the group resolver. Call it from an init function. It
// panics when called after app.Main started, a second time, or with a nil
// resolver. Without one, a stage whose group is not DefaultGroup cannot be
// decided (an owner can discard it).
func RegisterGroups(r GroupResolver) { reviewstate.RegisterGroups(r) }

// PendingStage is one staged edit waiting at a stage.
type PendingStage = reviewstate.PendingStage

// MaxPending is the most rows one Pending call returns.
const MaxPending = reviewstate.MaxPending

// ErrUnavailable means Pending was called outside serve, where there is no
// database.
var ErrUnavailable = reviewstate.ErrUnavailable

// Pending lists the staged edits waiting at a stage, oldest first, at most
// MaxPending. It works in serve only (ErrUnavailable elsewhere) and checks
// no rights: callers are module jobs.
func Pending(ctx context.Context) ([]PendingStage, error) { return reviewstate.Pending(ctx) }
