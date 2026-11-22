package reviewflow

import (
	"context"

	"hatamoto.dev/core/internal/agentpolicystate"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/reviewstate"
)

// AgentGate adds the agent publishing policy to the review gate: for a change
// made with an API key it asks the registered policy after the site's own
// rule, and the stricter answer wins. With no policy registered it is Inner.
type AgentGate struct{ Inner entity.ReviewGate }

var _ entity.ReviewGate = (*AgentGate)(nil)

// Plan implements entity.ReviewGate. A refusal is a *agentpolicystate.DeniedError
// or agentpolicystate.ErrPolicy; the policy's Allow never clears a held plan.
func (g *AgentGate) Plan(ctx context.Context, e *entity.Entity, kind entity.ReviewKind) (entity.ReviewPlan, error) {
	p, err := g.Inner.Plan(ctx, e, kind)
	if err != nil || !agentpolicystate.Active() {
		return p, err
	}
	a, ok := agentpolicystate.KeyActor(ctx)
	if !ok {
		return p, nil
	}
	d, err := agentpolicystate.Enforce(ctx, string(kind), entityDTO(e), true)
	if err != nil {
		return entity.ReviewPlan{}, err
	}
	// A plan the site's own rule held keeps its requester (the key's creator),
	// so nobody decides their own change. Only a change held by the agent
	// policy alone is recorded for the key.
	if p.Held || d.Outcome != agentpolicystate.OutcomeRequireReview {
		return p, nil
	}
	return entity.ReviewPlan{Held: true, Requester: "api-key:" + a.KeyID, Stages: []entity.ReviewStage{
		{Name: reviewstate.DefaultStageName, Group: reviewstate.DefaultGroup},
	}}, nil
}

// Submit stages through the inner gate; the engine's edit-submit audit row
// already names the key.
func (g *AgentGate) Submit(ctx context.Context, c entity.ReviewChange, p entity.ReviewPlan) (*entity.SentForReviewError, error) {
	return g.Inner.Submit(ctx, c, p)
}

func entityDTO(e *entity.Entity) agentpolicystate.Entity {
	return agentpolicystate.Entity{
		ID: e.ID.String(), Schema: schemaSlug(e), Locale: e.Locale, Slug: e.Slug, Status: string(e.Status),
	}
}
