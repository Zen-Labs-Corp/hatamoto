// Package agentpolicystate holds the agent publishing policy a compiled-in
// module registers (public facade: hatamoto.dev/core/agentpolicy). It is a
// leaf package so the entity service can recognise its refusals.
package agentpolicystate

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
)

// Outcome is what a policy decides for one change. The zero value is invalid
// on purpose: a policy that forgets to set it refuses instead of allowing.
type Outcome uint8

const (
	OutcomeAllow Outcome = iota + 1
	OutcomeRequireReview
	OutcomeDeny
)

// Decision is a policy's answer. Reason is shown to the agent on a refusal.
type Decision struct {
	Outcome Outcome
	Reason  string
}

// Entity describes the content a change touches.
type Entity struct{ ID, Schema, Locale, Slug, Status string }

// Request is one change an API key wants to make to live content.
type Request struct {
	KeyID, KeyName, Via, Action string
	Entity                      Entity
}

// Policy decides whether a key's change is allowed.
type Policy interface {
	Decide(ctx context.Context, r Request) (Decision, error)
}

// Actions, the same strings as the entity review gate's kinds.
const (
	ActionPublish  = "publish"
	ActionSchedule = "schedule"
	ActionEdit     = "edit"
	ActionRestore  = "restore"
	// ActionUnpublish takes a live entity offline (to draft) or archives it.
	// It cannot be staged, so RequireReview refuses it.
	ActionUnpublish = "unpublish"
)

var (
	// ErrPolicy wraps a policy failure: an error, a panic or an invalid decision.
	ErrPolicy = errors.New("agent policy: the policy failed")
	// ErrDenied is what a DeniedError unwraps to.
	ErrDenied = errors.New("refused by the agent policy")
	// ErrNeedsReview is returned when a change needs a review but cannot be staged here.
	ErrNeedsReview = errors.New("this change needs a person's review and cannot be sent for review from here")
)

// DeniedError carries the policy's reason; it unwraps to ErrDenied.
type DeniedError struct{ Reason string }

func (e *DeniedError) Error() string { return "refused by the agent policy: " + e.Reason }
func (e *DeniedError) Unwrap() error { return ErrDenied }

const maxReasonRunes = 200

// decideTimeout bounds one call to the policy; a var so tests can shorten it.
var decideTimeout = 5 * time.Second

type registration struct{ p Policy }

var (
	mu     sync.Mutex
	frozen bool
	reg    atomic.Pointer[registration]
)

// Register installs the policy. It panics after Freeze, on a second call, and
// with a nil (or typed-nil) policy.
func Register(p Policy) {
	mu.Lock()
	defer mu.Unlock()
	switch {
	case frozen:
		panic("agentpolicy: Register called after app.Main started; register from an init function")
	case reg.Load() != nil:
		panic("agentpolicy: Register called twice")
	case isNil(p):
		panic("agentpolicy: Register with a nil policy")
	}
	reg.Store(&registration{p: p})
}

// isNil reports whether p is nil or an interface holding a nil pointer, map,
// slice, func or chan, which would panic on first use.
func isNil(p Policy) bool {
	if p == nil {
		return true
	}
	switch v := reflect.ValueOf(p); v.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan, reflect.Interface:
		return v.IsNil()
	}
	return false
}

// Freeze makes any later Register panic. Core calls it first thing in Main.
func Freeze() {
	mu.Lock()
	frozen = true
	mu.Unlock()
}

// Reset clears all state; tests only.
func Reset() {
	mu.Lock()
	defer mu.Unlock()
	frozen = false
	reg.Store(nil)
	rt.Store(nil)
}

// Active reports whether a policy is registered. Callers skip all work when not.
func Active() bool { return reg.Load() != nil }

// Decide asks the registered policy. Nothing registered: Allow. A policy
// error, a panic (recovered) or an outcome outside Allow, RequireReview and
// Deny yields ErrPolicy. A refusal reason is trimmed, newlines become spaces
// and it is capped at 200 runes; control and format characters are dropped;
// empty becomes "no reason given". A policy that takes longer than 5 seconds
// yields ErrPolicy.
func Decide(ctx context.Context, r Request) (d Decision, err error) {
	cur := reg.Load()
	if cur == nil {
		return Decision{Outcome: OutcomeAllow}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, decideTimeout)
	defer cancel()
	type answer struct {
		d   Decision
		err error
	}
	done := make(chan answer, 1) // buffered: a policy that answers late does not leak its goroutine
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				done <- answer{err: fmt.Errorf("%w: panic: %v", ErrPolicy, rec)}
			}
		}()
		d, err := cur.p.Decide(ctx, r)
		done <- answer{d, err}
	}()
	select {
	case a := <-done:
		d, err = a.d, a.err
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return Decision{}, fmt.Errorf("%w: no answer within %s", ErrPolicy, decideTimeout)
		}
		return Decision{}, fmt.Errorf("%w: request cancelled: %v", ErrPolicy, ctx.Err())
	}
	if errors.Is(err, ErrPolicy) { // a recovered panic
		return Decision{}, err
	}
	if err != nil {
		return Decision{}, fmt.Errorf("%w: %v", ErrPolicy, err)
	}
	switch d.Outcome {
	case OutcomeAllow, OutcomeRequireReview, OutcomeDeny:
	default:
		return Decision{}, fmt.Errorf("%w: invalid outcome %d", ErrPolicy, d.Outcome)
	}
	d.Reason = cleanReason(d.Reason)
	return d, nil
}

func cleanReason(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && !unicode.IsSpace(r) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		return "no reason given"
	}
	if rs := []rune(s); len(rs) > maxReasonRunes {
		s = string(rs[:maxReasonRunes])
	}
	return s
}
