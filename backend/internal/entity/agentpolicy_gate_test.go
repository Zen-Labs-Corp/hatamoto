package entity

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/audit"
	"hatamoto.dev/core/authz"
	"hatamoto.dev/core/internal/agentpolicystate"
	"hatamoto.dev/core/internal/auditctx"
	"hatamoto.dev/core/internal/authzctx"
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

// apAsk records the requests it is asked and answers a fixed Decision.
type apAsk struct {
	d    agentpolicystate.Decision
	reqs []agentpolicystate.Request
}

func (a *apAsk) Decide(_ context.Context, r agentpolicystate.Request) (agentpolicystate.Decision, error) {
	a.reqs = append(a.reqs, r)
	return a.d, nil
}

// keyCtx is a write made with an API key whose creator is an owner.
func keyCtx(me uuid.UUID) context.Context {
	ctx := authzctx.With(context.Background(), authz.Subject{
		UserID: me.String(), CreatorRole: "owner", KeyID: uuid.NewString(), Scopes: []string{"write:entities"},
	})
	return auditctx.With(ctx, auditctx.Actor{ID: me.String(), Name: "api-key:ci", Kind: audit.KindAPIKey, KeyID: uuid.NewString(), Via: "api"})
}

func askRig(t *testing.T, d agentpolicystate.Decision) (*authzRig, uuid.UUID, *apAsk) {
	t.Helper()
	agentpolicystate.Reset()
	t.Cleanup(agentpolicystate.Reset)
	p := &apAsk{d: d}
	agentpolicystate.Register(p)
	g := newAuthzRig(t)
	return g, uuid.New(), p
}

func reuseEntity(t *testing.T, g *authzRig, slug string, me uuid.UUID) *Entity {
	t.Helper()
	sc := seedSchema(t, g.sr, func(s *EntitySchema) { s.Slug = slug; s.IsBuiltin = true })
	return seedEntity(t, g.repo.mockEntityRepository, sc, func(e *Entity) { e.CreatedBy = &me; e.Status = StatusDraft })
}

func TestAgentPolicyAsksBeforeUnpublishAndReuseEdit(t *testing.T) {
	deny := agentpolicystate.Decision{Outcome: agentpolicystate.OutcomeDeny, Reason: "frozen"}
	g, me, pol := askRig(t, deny)
	ctx := keyCtx(me)
	pub := g.entity(t, &me, StatusPublished)
	block := reuseEntity(t, g, BlockSchemaSlug, me)
	name := "N"

	var de *agentpolicystate.DeniedError
	for what, call := range map[string]func() error{
		"unpublish": func() error { _, err := g.svc.UpdateEntityStatus(ctx, pub.ID, &me, StatusDraft); return err },
		"archive":   func() error { _, err := g.svc.UpdateEntityStatus(ctx, pub.ID, &me, StatusArchived); return err },
		"edit person": func() error {
			_, err := g.svc.UpdateEntity(ctx, reuseEntity(t, g, personSchemaSlug, me).ID, &me, &UpdateEntityInput{Name: &name})
			return err
		},
		"edit block": func() error {
			_, err := g.svc.UpdateEntity(ctx, block.ID, &me, &UpdateEntityInput{Name: &name})
			return err
		},
	} {
		if err := call(); !errors.As(err, &de) || de.Reason != "frozen" {
			t.Errorf("%s: err = %v, want the policy's refusal", what, err)
		}
	}
	if pub.Status != StatusPublished || g.repo.updated != 0 {
		t.Errorf("a write happened: status %s, updated %d", pub.Status, g.repo.updated)
	}
	got := map[string]bool{}
	for _, r := range pol.reqs {
		got[r.Action] = true
	}
	for _, a := range []string{agentpolicystate.ActionUnpublish, agentpolicystate.ActionEdit} {
		if !got[a] {
			t.Errorf("action %q was never asked (asked %v)", a, got)
		}
	}
}

func TestAgentPolicyRequireReviewRefusesWhatCannotStage(t *testing.T) {
	g, me, _ := askRig(t, agentpolicystate.Decision{Outcome: agentpolicystate.OutcomeRequireReview})
	ctx := keyCtx(me)
	pub := g.entity(t, &me, StatusPublished)
	if _, err := g.svc.UpdateEntityStatus(ctx, pub.ID, &me, StatusDraft); !errors.Is(err, agentpolicystate.ErrNeedsReview) {
		t.Fatalf("unpublish: err = %v", err)
	}
}

func TestAgentPolicyDoesNotAskForPeopleDraftsOrAllowed(t *testing.T) {
	g, me, pol := askRig(t, agentpolicystate.Decision{Outcome: agentpolicystate.OutcomeDeny, Reason: "x"})
	pub, draft := g.entity(t, &me, StatusPublished), g.entity(t, &me, StatusDraft)
	// A person is never asked.
	if _, err := g.svc.UpdateEntityStatus(asRole("owner", me), pub.ID, &me, StatusDraft); err != nil {
		t.Fatalf("person unpublish: %v", err)
	}
	// A key unpublishing a draft (nothing live) is not asked.
	if _, err := g.svc.UpdateEntityStatus(keyCtx(me), draft.ID, &me, StatusArchived); err != nil {
		t.Fatalf("key archives a draft: %v", err)
	}
	if len(pol.reqs) != 0 {
		t.Fatalf("policy asked %d times", len(pol.reqs))
	}
	// Allow lets a key unpublish live content.
	g2, me2, _ := askRig(t, agentpolicystate.Decision{Outcome: agentpolicystate.OutcomeAllow})
	if _, err := g2.svc.UpdateEntityStatus(keyCtx(me2), g2.entity(t, &me2, StatusPublished).ID, &me2, StatusDraft); err != nil {
		t.Fatalf("allowed unpublish: %v", err)
	}
}

func TestAgentPolicyNothingRegisteredUnpublishesAsBefore(t *testing.T) {
	agentpolicystate.Reset()
	g := newAuthzRig(t)
	me := uuid.New()
	pub := g.entity(t, &me, StatusPublished)
	if _, err := g.svc.UpdateEntityStatus(keyCtx(me), pub.ID, &me, StatusDraft); err != nil {
		t.Fatalf("unpublish: %v", err)
	}
}
