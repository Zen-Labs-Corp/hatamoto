package reviewflow

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"hatamoto.dev/core/audit"
	"hatamoto.dev/core/authz"
	"hatamoto.dev/core/internal/agentpolicystate"
	"hatamoto.dev/core/internal/auditctx"
	"hatamoto.dev/core/internal/auditstate"
	"hatamoto.dev/core/internal/authzctx"
	"hatamoto.dev/core/internal/entity"
)

type fakeInner struct {
	plan      entity.ReviewPlan
	planErr   error
	submitted []entity.ReviewPlan
}

func (f *fakeInner) Plan(context.Context, *entity.Entity, entity.ReviewKind) (entity.ReviewPlan, error) {
	return f.plan, f.planErr
}

func (f *fakeInner) Submit(_ context.Context, _ entity.ReviewChange, p entity.ReviewPlan) (*entity.SentForReviewError, error) {
	f.submitted = append(f.submitted, p)
	return &entity.SentForReviewError{ChangesetID: 9}, nil
}

type fakePolicy struct {
	d     Decision
	err   error
	calls []agentpolicystate.Request
}

type Decision = agentpolicystate.Decision

func (f *fakePolicy) Decide(_ context.Context, r agentpolicystate.Request) (Decision, error) {
	f.calls = append(f.calls, r)
	return f.d, f.err
}

const testKeyID = "6f1c0c0e-5d0a-4a43-9a52-0d3f4c1f9e11"

func keyCtx() context.Context {
	ctx := authzctx.With(context.Background(), authz.Subject{UserID: "creator", Role: "owner"})
	return auditctx.With(ctx, auditctx.Actor{ID: "creator", Name: "api-key:ci", Kind: audit.KindAPIKey, KeyID: testKeyID, Via: "mcp"})
}

// agentRig registers the policy and a recording runtime; it returns the recorded events.
func agentRig(t *testing.T, p agentpolicystate.Policy) *[]auditstate.Event {
	t.Helper()
	agentpolicystate.Reset()
	t.Cleanup(agentpolicystate.Reset)
	events := &[]auditstate.Event{}
	if p != nil {
		agentpolicystate.Register(p)
	}
	agentpolicystate.SetRuntime(agentpolicystate.Runtime{
		Record: func(_ context.Context, ev auditstate.Event) { *events = append(*events, ev) },
		Changes: func(context.Context, string, time.Time, int) ([]agentpolicystate.Change, error) {
			return nil, nil
		},
	})
	return events
}

func TestAgentGateInactiveIsInner(t *testing.T) {
	events := agentRig(t, nil)
	inner := &fakeInner{plan: entity.ReviewPlan{Held: true, Stages: []entity.ReviewStage{{Name: "S", Group: "g"}}}}
	got, err := (&AgentGate{Inner: inner}).Plan(keyCtx(), testEntity(), entity.ReviewPublish)
	if err != nil || !reflect.DeepEqual(got, inner.plan) || len(*events) != 0 {
		t.Fatalf("got %+v, %v, events %d", got, err, len(*events))
	}
}

func TestAgentGateNonKeyActorIsInner(t *testing.T) {
	pol := &fakePolicy{d: Decision{Outcome: agentpolicystate.OutcomeDeny, Reason: "no"}}
	events := agentRig(t, pol)
	inner := &fakeInner{}
	ctx := authzctx.With(context.Background(), authz.Subject{UserID: "u", Role: "owner"})
	got, err := (&AgentGate{Inner: inner}).Plan(ctx, testEntity(), entity.ReviewPublish)
	if err != nil || !reflect.DeepEqual(got, inner.plan) || len(pol.calls) != 0 || len(*events) != 0 {
		t.Fatalf("got %+v, %v, calls %d", got, err, len(pol.calls))
	}
}

func TestAgentGateInnerErrorWins(t *testing.T) {
	pol := &fakePolicy{d: Decision{Outcome: agentpolicystate.OutcomeAllow}}
	agentRig(t, pol)
	boom := errors.New("boom")
	_, err := (&AgentGate{Inner: &fakeInner{planErr: boom}}).Plan(keyCtx(), testEntity(), entity.ReviewPublish)
	if !errors.Is(err, boom) || len(pol.calls) != 0 {
		t.Fatalf("err = %v, calls %d", err, len(pol.calls))
	}
}

func TestAgentGateRequireReviewHoldsWithDefaultStage(t *testing.T) {
	pol := &fakePolicy{d: Decision{Outcome: agentpolicystate.OutcomeRequireReview}}
	agentRig(t, pol)
	got, err := (&AgentGate{Inner: &fakeInner{}}).Plan(keyCtx(), testEntity(), entity.ReviewPublish)
	if err != nil {
		t.Fatal(err)
	}
	want := entity.ReviewPlan{Held: true, Stages: []entity.ReviewStage{{Name: "Review", Group: "reviewers"}}, Requester: "api-key:" + testKeyID}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("plan = %+v, want %+v", got, want)
	}
	r := pol.calls[0]
	if r.KeyID != testKeyID || r.KeyName != "ci" || r.Via != "mcp" || r.Action != "publish" ||
		r.Entity.Schema != "page" || r.Entity.Slug != "home" || r.Entity.Status != "published" {
		t.Fatalf("request = %+v", r)
	}
}

func TestAgentGateAllowKeepsHeldPlan(t *testing.T) {
	agentRig(t, &fakePolicy{d: Decision{Outcome: agentpolicystate.OutcomeAllow}})
	inner := &fakeInner{plan: entity.ReviewPlan{Held: true, Stages: []entity.ReviewStage{{Name: "Legal", Group: "legal"}}}}
	got, err := (&AgentGate{Inner: inner}).Plan(keyCtx(), testEntity(), entity.ReviewEdit)
	if err != nil || !got.Held || got.Stages[0].Group != "legal" || got.Requester != "api-key:"+testKeyID {
		t.Fatalf("plan = %+v, %v", got, err)
	}
}

func TestAgentGateAllowUnheldIsInner(t *testing.T) {
	agentRig(t, &fakePolicy{d: Decision{Outcome: agentpolicystate.OutcomeAllow}})
	got, err := (&AgentGate{Inner: &fakeInner{}}).Plan(keyCtx(), testEntity(), entity.ReviewEdit)
	if err != nil || !reflect.DeepEqual(got, entity.ReviewPlan{}) {
		t.Fatalf("plan = %+v, %v", got, err)
	}
}

func TestAgentGateDeny(t *testing.T) {
	events := agentRig(t, &fakePolicy{d: Decision{Outcome: agentpolicystate.OutcomeDeny, Reason: "not now"}})
	_, err := (&AgentGate{Inner: &fakeInner{}}).Plan(keyCtx(), testEntity(), entity.ReviewPublish)
	var d *agentpolicystate.DeniedError
	if !errors.As(err, &d) || d.Reason != "not now" {
		t.Fatalf("err = %v", err)
	}
	if len(*events) != 1 || (*events)[0].Action != agentpolicystate.DenyAction || (*events)[0].KeyID != testKeyID {
		t.Fatalf("events = %+v", *events)
	}
}

func TestAgentGatePolicyFailureFailsClosed(t *testing.T) {
	for name, pol := range map[string]*fakePolicy{
		"error": {err: errors.New("down")},
		"zero":  {},
	} {
		t.Run(name, func(t *testing.T) {
			events := agentRig(t, pol)
			_, err := (&AgentGate{Inner: &fakeInner{}}).Plan(keyCtx(), testEntity(), entity.ReviewPublish)
			if !errors.Is(err, agentpolicystate.ErrPolicy) || len(*events) != 1 {
				t.Fatalf("err = %v, events %d", err, len(*events))
			}
		})
	}
}

func TestAgentGateSubmitDelegates(t *testing.T) {
	inner := &fakeInner{}
	p := entity.ReviewPlan{Held: true, Requester: "api-key:x"}
	sent, err := (&AgentGate{Inner: inner}).Submit(keyCtx(), entity.ReviewChange{}, p)
	if err != nil || sent.ChangesetID != 9 || len(inner.submitted) != 1 || !reflect.DeepEqual(inner.submitted[0], p) {
		t.Fatalf("sent %+v, %v, submitted %+v", sent, err, inner.submitted)
	}
}

func TestSubmitRequesterFromPlan(t *testing.T) {
	ctx := authzctx.With(context.Background(), authz.Subject{UserID: "u-creator", Role: "owner"})
	for _, c := range []struct{ requester, want string }{{"", "u-creator"}, {"api-key:abc", "api-key:abc"}} {
		eng := &fakeEngine{}
		_, err := (&Flow{Engine: eng}).Submit(ctx, entity.ReviewChange{Entity: testEntity(), Status: entity.StatusPublished},
			entity.ReviewPlan{Held: true, Requester: c.requester})
		if err != nil || eng.opts.Requester != c.want {
			t.Fatalf("requester %q: got %q, %v", c.requester, eng.opts.Requester, err)
		}
	}
}
