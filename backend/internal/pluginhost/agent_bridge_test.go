package pluginhost

import (
	"context"
	"net/netip"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/agents"
	"hatamoto.dev/core/internal/hooks"
	"hatamoto.dev/sdk"
)

func TestAgentHitObserverFiresAction(t *testing.T) {
	e := hooks.New(nil)
	e.SetLifecycleContext(context.Background())
	got := make(chan sdk.AgentHitEvent, 2)
	hooks.OnAction(e, sdk.AgentHit, 10, func(_ context.Context, ev sdk.AgentHitEvent) { got <- ev })

	ip := netip.MustParseAddr("20.171.207.1")
	entity := uuid.New()
	at := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	AgentHitObserver(e)([]agents.Hit{
		{At: at, AgentID: "gptbot", Operator: "OpenAI", Purpose: agents.PurposeTraining, Verification: agents.VerificationPending,
			IP: &ip, Method: "GET", Path: "/seo", EntityID: &entity, Status: 200, Bytes: 512, DurationMS: 7, Source: agents.SourceMiddleware},
		{At: at, AgentID: "claudebot", Operator: "Anthropic", Purpose: agents.PurposeTraining, Path: "/robots.txt", Status: 200, Source: agents.SourceMiddleware},
	})

	var byAgent = map[string]sdk.AgentHitEvent{}
	for range 2 {
		select {
		case ev := <-got:
			byAgent[ev.AgentID] = ev
		case <-time.After(2 * time.Second):
			t.Fatal("agent.hit not fired")
		}
	}
	ev := byAgent["gptbot"]
	want := sdk.AgentHitEvent{AgentID: "gptbot", Operator: "OpenAI", Purpose: "training", Verification: "pending",
		Method: "GET", Path: "/seo", EntityID: entity.String(), Source: "middleware", Status: 200, Bytes: 512, DurationMS: 7, OccurredAt: at}
	if ev != want {
		t.Fatalf("event = %+v, want %+v", ev, want)
	}
	if byAgent["claudebot"].EntityID != "" {
		t.Errorf("entity id for non-entity path = %q", byAgent["claudebot"].EntityID)
	}
}

// ISC-18 / ISC-A-1: no IP (or user agent) anywhere in the hook payloads, and
// no field value of a hit built from an IP-carrying agents.Hit contains it.
func TestAgentHookPayloadsHaveNoIP(t *testing.T) {
	for _, ty := range []reflect.Type{reflect.TypeFor[sdk.AgentHitEvent](), reflect.TypeFor[sdk.AgentFirstCrawlEvent]()} {
		for field := range ty.Fields() {
			switch strings.ToLower(field.Name) {
			case "ip", "ipaddress", "clientip", "remoteaddr", "useragent":
				t.Errorf("%s has field %s", ty.Name(), field.Name)
			}
		}
	}
	ip := netip.MustParseAddr("203.0.113.55")
	ev := toSDKAgentHit(agents.Hit{AgentID: "gptbot", IP: &ip, Path: "/x"})
	v := reflect.ValueOf(ev)
	for i := 0; i < v.NumField(); i++ {
		if v.Field(i).Kind() == reflect.String && strings.Contains(v.Field(i).String(), "203.0.113.55") {
			t.Errorf("field %s leaks the ip", v.Type().Field(i).Name)
		}
	}
}

func TestAgentHitObserverSkipsWithoutSubscriber(t *testing.T) {
	e := hooks.New(nil)
	e.SetLifecycleContext(context.Background())
	if e.HasAction(sdk.AgentHit.Name) {
		t.Fatal("unexpected subscriber")
	}
	// Must be a cheap no-op (and not panic) with no subscriber.
	AgentHitObserver(e)([]agents.Hit{{AgentID: "gptbot"}})
	AgentFirstCrawlObserver(e)(agents.FirstCrawl{EntityID: uuid.New()})
	e.Wait()
}

func TestAgentFirstCrawlObserverFiresAction(t *testing.T) {
	e := hooks.New(nil)
	e.SetLifecycleContext(context.Background())
	got := make(chan sdk.AgentFirstCrawlEvent, 1)
	hooks.OnAction(e, sdk.AgentFirstCrawl, 10, func(_ context.Context, ev sdk.AgentFirstCrawlEvent) { got <- ev })

	id := uuid.New()
	at := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	pub := at.Add(-3 * time.Hour)
	AgentFirstCrawlObserver(e)(agents.FirstCrawl{EntityID: id, AgentID: "gptbot", Operator: "OpenAI", Purpose: "training", At: at, PublishedAt: &pub})
	select {
	case ev := <-got:
		if ev.EntityID != id.String() || ev.AgentID != "gptbot" || !ev.FirstAt.Equal(at) || ev.PublishedAt == nil || !ev.PublishedAt.Equal(pub) {
			t.Fatalf("event = %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("agent.first_crawl not fired")
	}
}
