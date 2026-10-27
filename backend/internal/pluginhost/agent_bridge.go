package pluginhost

import (
	"context"

	"hatamoto.dev/core/internal/agents"
	"hatamoto.dev/core/internal/hooks"
	"hatamoto.dev/sdk"
)

// AgentHitObserver returns a callback for agents.Writer.SetObserver that
// re-fires every stored hit as the agent.hit action. Nothing is built when no
// plugin subscribes. The IP never leaves the agents package: sdk.AgentHitEvent
// has no such field.
func AgentHitObserver(e *hooks.Engine) func([]agents.Hit) {
	return func(hits []agents.Hit) {
		if !e.HasAction(sdk.AgentHit.Name) {
			return
		}
		for _, h := range hits {
			e.Fire(context.Background(), sdk.AgentHit.Name, toSDKAgentHit(h))
		}
	}
}

// AgentFirstCrawlObserver returns the callback for agents.NewFirstCrawlTracker
// that fires agent.first_crawl.
func AgentFirstCrawlObserver(e *hooks.Engine) func(agents.FirstCrawl) {
	return func(fc agents.FirstCrawl) {
		if !e.HasAction(sdk.AgentFirstCrawl.Name) {
			return
		}
		e.Fire(context.Background(), sdk.AgentFirstCrawl.Name, sdk.AgentFirstCrawlEvent{
			EntityID:    fc.EntityID.String(),
			AgentID:     fc.AgentID,
			Operator:    fc.Operator,
			Purpose:     fc.Purpose,
			FirstAt:     fc.At,
			PublishedAt: fc.PublishedAt,
		})
	}
}

func toSDKAgentHit(h agents.Hit) sdk.AgentHitEvent {
	ev := sdk.AgentHitEvent{
		AgentID:      h.AgentID,
		Operator:     h.Operator,
		Purpose:      string(h.Purpose),
		Verification: h.Verification,
		Method:       h.Method,
		Path:         h.Path,
		Source:       h.Source,
		Status:       h.Status,
		Bytes:        int(h.Bytes),
		DurationMS:   h.DurationMS,
		OccurredAt:   h.At,
	}
	if h.EntityID != nil {
		ev.EntityID = h.EntityID.String()
	}
	return ev
}
