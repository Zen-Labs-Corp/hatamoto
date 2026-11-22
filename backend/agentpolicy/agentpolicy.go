package agentpolicy

import (
	"context"
	"time"

	"hatamoto.dev/core/internal/agentpolicystate"
)

type (
	// Request is one change an API key wants to make to live content.
	Request = agentpolicystate.Request
	// Entity describes the content the change touches.
	Entity = agentpolicystate.Entity
	// Decision is the policy's answer; build it with Allow, RequireReview or Deny.
	Decision = agentpolicystate.Decision
	// Outcome is the kind of a Decision. The zero value is invalid and refuses.
	Outcome = agentpolicystate.Outcome
	// Policy decides whether a key's change is allowed.
	Policy = agentpolicystate.Policy
	// Change is one audit row written for an API key.
	Change = agentpolicystate.Change
)

// Outcomes of a Decision.
const (
	OutcomeAllow         = agentpolicystate.OutcomeAllow
	OutcomeRequireReview = agentpolicystate.OutcomeRequireReview
	OutcomeDeny          = agentpolicystate.OutcomeDeny
)

// Values of Request.Action.
const (
	ActionPublish  = agentpolicystate.ActionPublish
	ActionSchedule = agentpolicystate.ActionSchedule
	ActionEdit     = agentpolicystate.ActionEdit
	ActionRestore  = agentpolicystate.ActionRestore
	// ActionUnpublish (offline or archive) cannot be staged: RequireReview
	// refuses it.
	ActionUnpublish = agentpolicystate.ActionUnpublish
)

// MaxChanges is the most rows one Changes call returns.
const MaxChanges = agentpolicystate.MaxChanges

// Errors. ErrPolicy wraps a policy failure (error, panic or invalid decision).
var (
	ErrPolicy      = agentpolicystate.ErrPolicy
	ErrUnavailable = agentpolicystate.ErrUnavailable
	ErrInvalidKey  = agentpolicystate.ErrInvalidKey
)

// Allow lets the change through (the site's own review rule still applies).
func Allow() Decision { return Decision{Outcome: OutcomeAllow} }

// RequireReview stages the change for a person's review.
func RequireReview(reason string) Decision {
	return Decision{Outcome: OutcomeRequireReview, Reason: reason}
}

// Deny refuses the change; reason is shown to the agent.
func Deny(reason string) Decision { return Decision{Outcome: OutcomeDeny, Reason: reason} }

// Register installs the policy. It panics after app.Main started, on a second
// call, and with a nil (or typed-nil) policy. Call it from an init function.
func Register(p Policy) { agentpolicystate.Register(p) }

// Changes lists what the key changed since the given time, oldest first, at
// most MaxChanges rows. It returns ErrInvalidKey for a key id that is not a
// uuid and ErrUnavailable outside serve.
func Changes(ctx context.Context, keyID string, since time.Time) ([]Change, error) {
	return agentpolicystate.Changes(ctx, keyID, since)
}
