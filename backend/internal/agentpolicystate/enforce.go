package agentpolicystate

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/audit"
	"hatamoto.dev/core/internal/auditctx"
	"hatamoto.dev/core/internal/auditstate"
	"hatamoto.dev/core/internal/authzctx"
)

// MaxChanges is the most rows one Changes call returns.
const MaxChanges = 1000

var (
	// ErrUnavailable is returned by Changes outside serve, where there is no database.
	ErrUnavailable = errors.New("agent policy: no database in this process")
	// ErrInvalidKey is returned by Changes for a key id that is not a uuid.
	ErrInvalidKey = errors.New("agent policy: invalid key id")
)

// DenyAction is the audit action written for every refusal.
const DenyAction = "agent_policy.deny"

// Change is one audit row written for an API key.
type Change struct {
	ID                                 int64
	At                                 time.Time
	Action, Target, Result, Via, Actor string
	Detail                             map[string]any
}

// Runtime is what core gives the package in serve; core only.
type Runtime struct {
	// Record writes a best-effort audit row.
	Record func(ctx context.Context, ev auditstate.Event)
	// Changes reads the rows of one key, oldest first.
	Changes func(ctx context.Context, keyID string, since time.Time, limit int) ([]Change, error)
}

var rt atomic.Pointer[Runtime]

// SetRuntime installs the runtime; core only. It panics when a func is nil.
func SetRuntime(r Runtime) {
	if r.Record == nil || r.Changes == nil {
		panic("agentpolicystate: nil runtime func")
	}
	rt.Store(&r)
}

// Changes lists what the key changed since `since`, oldest first, at most
// MaxChanges rows.
func Changes(ctx context.Context, keyID string, since time.Time) ([]Change, error) {
	if _, err := uuid.Parse(keyID); err != nil {
		return nil, ErrInvalidKey
	}
	r := rt.Load()
	if r == nil {
		return nil, ErrUnavailable
	}
	return r.Changes(ctx, keyID, since, MaxChanges)
}

// KeyActor returns the actor in ctx when it is an API key with a key id.
func KeyActor(ctx context.Context) (auditctx.Actor, bool) {
	a, ok := auditctx.From(ctx)
	if !ok || a.Kind != audit.KindAPIKey || a.KeyID == "" {
		return auditctx.Actor{}, false
	}
	return a, true
}

// AsksFor reports whether a write under ctx is one the policy decides: one is
// registered and the write is made with an API key, known from the audit actor
// or from the authorization subject.
func AsksFor(ctx context.Context) bool {
	if !Active() {
		return false
	}
	if _, ok := KeyActor(ctx); ok {
		return true
	}
	return subjectIsKey(ctx)
}

// subjectIsKey reports whether the authorization subject is an API key.
func subjectIsKey(ctx context.Context) bool {
	s, ok := authzctx.From(ctx)
	return ok && s.KeyID != ""
}

// RequestFor builds the policy request for a key's change.
func RequestFor(a auditctx.Actor, action string, e Entity) Request {
	return Request{
		KeyID: a.KeyID, KeyName: strings.TrimPrefix(a.Name, "api-key:"),
		Via: a.Via, Action: action, Entity: e,
	}
}

// Enforce asks the policy for a key's change. Not active, or not a key: Allow,
// no work. A key (by its authorization subject) with no audit actor on ctx
// cannot be named to the policy: it fails closed with ErrPolicy. A Deny records agent_policy.deny and returns a *DeniedError.
// A policy failure records the row (outcome error) and returns ErrPolicy.
// RequireReview with canStage false records the row (outcome needs_review) and
// returns ErrNeedsReview. RequireReview with canStage true returns the
// decision: the caller stages it and the review engine records the request.
func Enforce(ctx context.Context, action string, e Entity, canStage bool) (Decision, error) {
	allow := Decision{Outcome: OutcomeAllow}
	if !Active() {
		return allow, nil
	}
	a, ok := KeyActor(ctx)
	if !ok {
		if subjectIsKey(ctx) {
			return Decision{}, fmt.Errorf("%w: a key's write has no audit actor", ErrPolicy)
		}
		return allow, nil
	}
	d, err := Decide(ctx, RequestFor(a, action, e))
	switch {
	case err != nil:
		recordDeny(ctx, a, action, e, "the policy failed", "error")
		return Decision{}, err
	case d.Outcome == OutcomeDeny:
		recordDeny(ctx, a, action, e, d.Reason, "deny")
		return d, &DeniedError{Reason: d.Reason}
	case d.Outcome == OutcomeRequireReview && !canStage:
		recordDeny(ctx, a, action, e, d.Reason, "needs_review")
		return d, ErrNeedsReview
	}
	return d, nil
}

func recordDeny(ctx context.Context, a auditctx.Actor, action string, e Entity, reason, outcome string) {
	r := rt.Load()
	if r == nil {
		return
	}
	defer func() { _ = recover() }() // a failing audit write never changes the refusal
	r.Record(ctx, auditstate.Event{
		ActorID: a.ID, Actor: a.Name, ActorKind: a.Kind, KeyID: a.KeyID,
		Action: DenyAction, Target: e.ID, Result: audit.ResultDenied,
		Detail: map[string]any{"action": action, "reason": reason, "via": a.Via, "outcome": outcome},
		IP:     a.IP, UserAgent: a.UserAgent, RequestID: a.RequestID,
	})
}
