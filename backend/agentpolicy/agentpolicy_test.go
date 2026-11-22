package agentpolicy_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"hatamoto.dev/core/agentpolicy"
)

type fake struct {
	d   agentpolicy.Decision
	err error
	got agentpolicy.Request
}

func (f *fake) Decide(_ context.Context, r agentpolicy.Request) (agentpolicy.Decision, error) {
	f.got = r
	return f.d, f.err
}

func reset(t *testing.T) {
	t.Helper()
	agentpolicy.ResetForTest()
	t.Cleanup(agentpolicy.ResetForTest)
}

func mustPanic(t *testing.T, name string, fn func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Errorf("%s: expected panic", name)
		}
	}()
	fn()
}

func TestModuleRegistersAFake(t *testing.T) {
	reset(t)
	f := &fake{d: agentpolicy.Deny("closed")}
	agentpolicy.Register(f)
	req := agentpolicy.Request{KeyID: "k", Action: agentpolicy.ActionPublish, Entity: agentpolicy.Entity{ID: "e"}}
	d, err := agentpolicy.DecideForTest(context.Background(), req)
	if err != nil || d.Outcome != agentpolicy.OutcomeDeny || d.Reason != "closed" {
		t.Fatalf("got %+v, %v", d, err)
	}
	if f.got.KeyID != "k" || f.got.Entity.ID != "e" {
		t.Fatalf("request %+v", f.got)
	}
}

func TestZeroDecisionFailsClosed(t *testing.T) {
	reset(t)
	agentpolicy.Register(&fake{})
	if _, err := agentpolicy.DecideForTest(context.Background(), agentpolicy.Request{}); !errors.Is(err, agentpolicy.ErrPolicy) {
		t.Fatalf("err %v", err)
	}
}

func TestConstructors(t *testing.T) {
	if d := agentpolicy.Allow(); d.Outcome != agentpolicy.OutcomeAllow {
		t.Errorf("Allow %+v", d)
	}
	if d := agentpolicy.RequireReview("r"); d.Outcome != agentpolicy.OutcomeRequireReview || d.Reason != "r" {
		t.Errorf("RequireReview %+v", d)
	}
	if d := agentpolicy.Deny("r"); d.Outcome != agentpolicy.OutcomeDeny || d.Reason != "r" {
		t.Errorf("Deny %+v", d)
	}
}

func TestRegisterPanics(t *testing.T) {
	reset(t)
	mustPanic(t, "nil", func() { agentpolicy.Register(nil) })
	mustPanic(t, "typed nil", func() { agentpolicy.Register((*fake)(nil)) })
	agentpolicy.Register(&fake{})
	mustPanic(t, "twice", func() { agentpolicy.Register(&fake{}) })
	agentpolicy.ResetForTest()
	agentpolicy.FreezeForTest()
	mustPanic(t, "after freeze", func() { agentpolicy.Register(&fake{}) })
}

func TestChangesWithoutRuntime(t *testing.T) {
	reset(t)
	if _, err := agentpolicy.Changes(context.Background(), "11111111-1111-1111-1111-111111111111", time.Time{}); !errors.Is(err, agentpolicy.ErrUnavailable) {
		t.Fatalf("err %v", err)
	}
	if _, err := agentpolicy.Changes(context.Background(), "x", time.Time{}); !errors.Is(err, agentpolicy.ErrInvalidKey) {
		t.Fatalf("err %v", err)
	}
}
