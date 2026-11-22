package entity

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/agentpolicystate"
)

// planGate answers Plan with a fixed plan and error and records the plan it is
// asked to submit.
type planGate struct {
	plan    ReviewPlan
	err     error
	planned int
	subs    []ReviewPlan
}

func (g *planGate) Plan(context.Context, *Entity, ReviewKind) (ReviewPlan, error) {
	g.planned++
	return g.plan, g.err
}

func (g *planGate) Submit(_ context.Context, _ ReviewChange, p ReviewPlan) (*SentForReviewError, error) {
	g.subs = append(g.subs, p)
	return &SentForReviewError{ChangesetID: 7, Since: time.Unix(1, 0)}, nil
}

func policyRig(t *testing.T, gate *planGate) (*authzRig, uuid.UUID) {
	t.Helper()
	g := newAuthzRig(t)
	g.svc.SetReviewGate(gate)
	return g, uuid.New()
}

func TestAgentPolicyDeniedErrorPassesThrough(t *testing.T) {
	gate := &planGate{err: &agentpolicystate.DeniedError{Reason: "x"}}
	g, me := policyRig(t, gate)
	ctx := asRole("owner", me)
	name := "N"
	pub := g.entity(t, &me, StatusPublished)
	draft := g.entity(t, &me, StatusDraft)
	g.repo.versions[pub.ID] = []*EntityVersion{{ID: uuid.New(), EntityID: pub.ID, Version: 1, Attributes: []byte(`{"title":"Old","body":"B"}`)}}

	calls := map[string]func() error{
		"update": func() error {
			_, err := g.svc.UpdateEntity(ctx, pub.ID, &me, &UpdateEntityInput{Name: &name})
			return err
		},
		"status":  func() error { _, err := g.svc.UpdateEntityStatus(ctx, draft.ID, &me, StatusPublished); return err },
		"restore": func() error { _, err := g.svc.RestoreVersion(ctx, pub.ID, &me, 1); return err },
	}
	for what, call := range calls {
		err := call()
		var de *agentpolicystate.DeniedError
		if !errors.As(err, &de) || de.Reason != "x" || errors.Is(err, ErrReviewHold) {
			t.Errorf("%s: err = %v, want the DeniedError unchanged", what, err)
		}
	}
	if g.repo.updated != 0 || draft.Status != StatusDraft {
		t.Errorf("a write happened: updated %d, status %s", g.repo.updated, draft.Status)
	}
}

func TestAgentPolicyFailureStaysReviewHold(t *testing.T) {
	gate := &planGate{err: agentpolicystate.ErrPolicy}
	g, me := policyRig(t, gate)
	pub := g.entity(t, &me, StatusPublished)
	name := "N"
	_, err := g.svc.UpdateEntity(asRole("owner", me), pub.ID, &me, &UpdateEntityInput{Name: &name})
	if !errors.Is(err, ErrReviewHold) || errors.Is(err, agentpolicystate.ErrDenied) {
		t.Fatalf("err = %v, want ErrReviewHold", err)
	}
}

func TestAgentPolicyPlanRequesterReachesSubmit(t *testing.T) {
	gate := &planGate{plan: ReviewPlan{Held: true, Stages: []ReviewStage{{Name: "Review", Group: "reviewers"}}, Requester: "api-key:k"}}
	g, me := policyRig(t, gate)
	pub := g.entity(t, &me, StatusPublished)
	name := "N"
	_, err := g.svc.UpdateEntity(asRole("owner", me), pub.ID, &me, &UpdateEntityInput{Name: &name})
	sent(t, err, 7)
	if len(gate.subs) != 1 || gate.subs[0].Requester != "api-key:k" {
		t.Fatalf("submitted plans = %+v", gate.subs)
	}
}
