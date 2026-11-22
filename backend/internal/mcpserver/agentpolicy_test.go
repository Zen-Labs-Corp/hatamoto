package mcpserver

import (
	"context"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/agentpolicystate"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/reviewflow"
)

// capGate is the site's own gate with review switched off; it records the plan
// it was asked to submit.
type capGate struct {
	requester string
	submits   int
}

func (g *capGate) Plan(context.Context, *entity.Entity, entity.ReviewKind) (entity.ReviewPlan, error) {
	return entity.ReviewPlan{}, nil
}

func (g *capGate) Submit(_ context.Context, _ entity.ReviewChange, p entity.ReviewPlan) (*entity.SentForReviewError, error) {
	g.submits++
	g.requester = p.Requester
	return &entity.SentForReviewError{ChangesetID: 42}, nil
}

type decidePolicy struct{ d agentpolicystate.Decision }

func (p decidePolicy) Decide(context.Context, agentpolicystate.Request) (agentpolicystate.Decision, error) {
	return p.d, nil
}

func registerPolicy(t *testing.T, o agentpolicystate.Outcome, reason string) {
	t.Helper()
	agentpolicystate.Reset()
	t.Cleanup(agentpolicystate.Reset)
	agentpolicystate.Register(decidePolicy{agentpolicystate.Decision{Outcome: o, Reason: reason}})
}

func TestAgentPolicyMCPRequireReviewStagesWithTheKeyAsRequester(t *testing.T) {
	registerPolicy(t, agentpolicystate.OutcomeRequireReview, "")
	inner := &capGate{}
	run, repo, _ := reviewSession(t, "owner", &reviewflow.AgentGate{Inner: inner})
	for tool, args := range map[string]map[string]any{
		"entities_publish": {},
		"entities_update":  {"name": "New"},
	} {
		text, isErr := run(tool, args)
		if isErr {
			t.Fatalf("%s: a staged change is a result, not an error: %s", tool, text)
		}
		if m := decode(t, text); m["status"] != "pending_review" || m["changeset_id"] != float64(42) {
			t.Fatalf("%s: result = %s", tool, text)
		}
		if want := "api-key:" + fakeKeyID.String(); inner.requester != want {
			t.Fatalf("%s: requester = %q, want %q", tool, inner.requester, want)
		}
	}
	if repo.writes.Load() != 0 {
		t.Fatalf("the live row was written %d times", repo.writes.Load())
	}
}

func TestAgentPolicyMCPDenyShowsTheReason(t *testing.T) {
	registerPolicy(t, agentpolicystate.OutcomeDeny, "not on weekends")
	inner := &capGate{}
	run, repo, _ := reviewSession(t, "owner", &reviewflow.AgentGate{Inner: inner})
	for tool, args := range map[string]map[string]any{
		"entities_publish": {},
		"entities_update":  {"name": "New"},
	} {
		text, isErr := run(tool, args)
		if !isErr || !strings.Contains(text, "not on weekends") {
			t.Fatalf("%s: got isErr=%v %q, want an error with the reason", tool, isErr, text)
		}
	}
	if repo.writes.Load() != 0 || inner.submits != 0 {
		t.Fatalf("writes = %d, submits = %d, want none", repo.writes.Load(), inner.submits)
	}
}

func TestAgentPolicyMCPPolicyFailureRefuses(t *testing.T) {
	registerPolicy(t, 0, "") // the zero decision is invalid
	run, repo, _ := reviewSession(t, "owner", &reviewflow.AgentGate{Inner: &capGate{}})
	text, isErr := run("entities_publish", map[string]any{})
	if !isErr || repo.writes.Load() != 0 {
		t.Fatalf("got isErr=%v %q, writes %d, want a refusal and no write", isErr, text, repo.writes.Load())
	}
}

func TestAgentPolicyMCPNothingRegisteredWritesAsToday(t *testing.T) {
	agentpolicystate.Reset()
	inner := &capGate{}
	run, repo, _ := reviewSession(t, "owner", &reviewflow.AgentGate{Inner: inner})
	text, isErr := run("entities_publish", map[string]any{})
	if isErr || repo.writes.Load() != 1 || inner.submits != 0 {
		t.Fatalf("got isErr=%v %q, writes %d, submits %d, want one write", isErr, text, repo.writes.Load(), inner.submits)
	}
}
