// Package authztest registers fake authorizers for tests of the packages that
// ask authz.Can. It is imported only from _test.go files.
package authztest

import (
	"context"
	"sync"
	"testing"

	"hatamoto.dev/core/authz"
	"hatamoto.dev/core/internal/authzstate"
)

// Func adapts a function to authz.Authorizer.
type Func func(ctx context.Context, s authz.Subject, a authz.Action, o authz.Object) (bool, error)

// Can calls f.
func (f Func) Can(ctx context.Context, s authz.Subject, a authz.Action, o authz.Object) (bool, error) {
	return f(ctx, s, a, o)
}

// DenyAll refuses every request.
var DenyAll = Func(func(context.Context, authz.Subject, authz.Action, authz.Object) (bool, error) {
	return false, nil
})

// Call is one question a Recorder was asked.
type Call struct {
	Subject authz.Subject
	Action  authz.Action
	Object  authz.Object
}

// Recorder remembers every question and answers with Answer, or with
// authz.Default() when Answer is nil.
type Recorder struct {
	Answer authz.Authorizer

	mu    sync.Mutex
	calls []Call
}

// Can records the question and forwards it.
func (r *Recorder) Can(ctx context.Context, s authz.Subject, a authz.Action, o authz.Object) (bool, error) {
	r.mu.Lock()
	r.calls = append(r.calls, Call{Subject: s, Action: a, Object: o})
	r.mu.Unlock()
	if r.Answer != nil {
		return r.Answer.Can(ctx, s, a, o)
	}
	return authz.Default().Can(ctx, s, a, o)
}

// Calls returns a copy of the recorded questions.
func (r *Recorder) Calls() []Call {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Call(nil), r.calls...)
}

// Use registers a for the test and restores the default afterwards. The
// registration is process-wide, so callers must not use t.Parallel.
func Use(t testing.TB, a authz.Authorizer) {
	t.Helper()
	authzstate.Reset()
	authz.Register(a)
	t.Cleanup(authzstate.Reset)
}
