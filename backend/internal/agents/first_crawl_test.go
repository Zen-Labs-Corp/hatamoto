package agents

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

type fakeFirstCrawlStore struct {
	mu    sync.Mutex
	rows  map[uuid.UUID]string
	calls int
	pub   *time.Time
}

func (f *fakeFirstCrawlStore) RecordFirstCrawl(_ context.Context, id uuid.UUID, agent string, _ time.Time) (*time.Time, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.rows == nil {
		f.rows = map[uuid.UUID]string{}
	}
	if _, ok := f.rows[id]; ok {
		return nil, false, nil
	}
	f.rows[id] = agent
	return f.pub, true, nil
}

func aiHit(entity *uuid.UUID, agent string, p Purpose, status int) Hit {
	return Hit{At: time.Now(), AgentID: agent, Operator: "OpenAI", Purpose: p, EntityID: entity, Status: status}
}

func TestFirstCrawlTracker(t *testing.T) {
	a, b, c, d := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	pub := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	store := &fakeFirstCrawlStore{pub: &pub}
	var got []FirstCrawl
	tr := NewFirstCrawlTracker(store, Default(), func(fc FirstCrawl) { got = append(got, fc) }, slog.New(slog.NewTextHandler(io.Discard, nil)))

	tr.OnHits([]Hit{
		aiHit(&a, "gptbot", PurposeTraining, 200),
		aiHit(&a, "claudebot", PurposeTraining, 200), // second hit, same entity: no callback
		aiHit(&b, "googlebot", PurposeSearch, 200),   // not AI
		aiHit(&c, "gptbot", PurposeTraining, 404),    // not served
		aiHit(&d, "gptbot", PurposeTraining, 301),    // not served
		aiHit(nil, "gptbot", PurposeTraining, 200),   // no entity
		aiHit(&d, "chatgpt-user", PurposeUserFetch, 200),
	})
	tr.Close() // drains

	if len(got) != 2 {
		t.Fatalf("callbacks = %d, want 2: %+v", len(got), got)
	}
	if got[0].EntityID != a || got[0].AgentID != "gptbot" || got[0].PublishedAt == nil || !got[0].PublishedAt.Equal(pub) {
		t.Errorf("first = %+v", got[0])
	}
	if got[1].EntityID != d || got[1].AgentID != "chatgpt-user" || got[1].Purpose != "user_fetch" {
		t.Errorf("second = %+v", got[1])
	}
	if store.calls != 2 {
		t.Errorf("store calls = %d, want 2 (seen cache must skip the repeat for a)", store.calls)
	}
}

func TestFirstCrawlTrackerExistingRowNoCallback(t *testing.T) {
	a := uuid.New()
	store := &fakeFirstCrawlStore{rows: map[uuid.UUID]string{a: "gptbot"}}
	called := false
	tr := NewFirstCrawlTracker(store, Default(), func(FirstCrawl) { called = true }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	tr.OnHits([]Hit{aiHit(&a, "claudebot", PurposeTraining, 200)})
	tr.Close()
	if called {
		t.Error("callback fired for an entity that already had a first crawl")
	}
}

func TestFirstCrawlTrackerAfterClose(t *testing.T) {
	tr := NewFirstCrawlTracker(&fakeFirstCrawlStore{}, Default(), nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	tr.Close()
	tr.Close()
	id := uuid.New()
	tr.OnHits([]Hit{aiHit(&id, "gptbot", PurposeTraining, 200)}) // must not panic or block
}
