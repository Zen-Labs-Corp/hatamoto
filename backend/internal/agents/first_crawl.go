package agents

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"hatamoto.dev/core/internal/database"
)

const (
	firstCrawlQueue   = 256
	firstCrawlTimeout = 5 * time.Second
	firstCrawlSeenMax = 10_000
)

// FirstCrawl is the first served AI fetch of an entity.
type FirstCrawl struct {
	EntityID    uuid.UUID
	AgentID     string
	Operator    string
	Purpose     string
	At          time.Time
	PublishedAt *time.Time
}

// FirstCrawlStore persists the first crawl per entity.
type FirstCrawlStore interface {
	// RecordFirstCrawl inserts the row unless the entity already has one (or
	// no longer exists). inserted reports whether this call created it;
	// publishedAt is the entity's publish time at that moment.
	RecordFirstCrawl(ctx context.Context, entityID uuid.UUID, agentID string, at time.Time) (publishedAt *time.Time, inserted bool, err error)
}

// FirstCrawlRepository is the Postgres FirstCrawlStore.
type FirstCrawlRepository struct{ db *database.DB }

// NewFirstCrawlRepository creates the Postgres-backed store.
func NewFirstCrawlRepository(db *database.DB) *FirstCrawlRepository {
	return &FirstCrawlRepository{db: db}
}

func (r *FirstCrawlRepository) RecordFirstCrawl(ctx context.Context, entityID uuid.UUID, agentID string, at time.Time) (*time.Time, bool, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("get connection: %w", err)
	}
	defer conn.Release()
	// Selecting from hatamoto_entities makes a deleted/unknown entity a no-op
	// instead of a foreign key error.
	var published *time.Time
	err = conn.QueryRow(ctx, `
		INSERT INTO hatamoto_agent_first_crawl (entity_id, agent_id, first_at, published_at)
		SELECT e.id, $2, $3, e.published_at FROM hatamoto_entities e WHERE e.id = $1
		ON CONFLICT (entity_id) DO NOTHING
		RETURNING published_at`, entityID, agentID, at).Scan(&published)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("record first crawl: %w", err)
	}
	return published, true, nil
}

// FirstCrawlTracker watches stored hits and reports the first served AI fetch
// per entity. It is installed as the Writer observer, which must not block, so
// OnHits only enqueues; a goroutine does the database work. A full queue drops
// the candidate: a later hit for the same entity retries.
//
// Accepted risk: hits are stored as pending, so a spoofed crawler can claim
// the "first crawl". The row is informational (latency card + hook).
type FirstCrawlTracker struct {
	store   FirstCrawlStore
	reg     *Registry
	onFirst func(FirstCrawl)
	logger  *slog.Logger

	ch     chan Hit
	stop   chan struct{}
	done   chan struct{}
	closed atomic.Bool
	once   sync.Once

	seen map[uuid.UUID]struct{} // entities known to have a row; goroutine-owned
}

// NewFirstCrawlTracker starts the tracker. onFirst may be nil; it runs on the
// tracker goroutine and must not block.
func NewFirstCrawlTracker(store FirstCrawlStore, reg *Registry, onFirst func(FirstCrawl), logger *slog.Logger) *FirstCrawlTracker {
	t := &FirstCrawlTracker{
		store: store, reg: reg, onFirst: onFirst, logger: logger,
		ch:   make(chan Hit, firstCrawlQueue),
		stop: make(chan struct{}),
		done: make(chan struct{}),
		seen: make(map[uuid.UUID]struct{}),
	}
	go t.run()
	return t
}

// OnHits is the Writer observer. Only AI-purpose, status 200 hits with an
// entity qualify. Never blocks.
func (t *FirstCrawlTracker) OnHits(hits []Hit) {
	if t.closed.Load() {
		return
	}
	for _, h := range hits {
		if h.EntityID == nil || h.Status != 200 || !h.Purpose.IsAI() {
			continue
		}
		select {
		case t.ch <- h:
		default:
		}
	}
}

// Close drains what is queued and stops the goroutine. Idempotent.
func (t *FirstCrawlTracker) Close() {
	t.once.Do(func() {
		t.closed.Store(true)
		close(t.stop)
	})
	<-t.done
}

func (t *FirstCrawlTracker) run() {
	defer close(t.done)
	for {
		select {
		case h := <-t.ch:
			t.process(h)
		case <-t.stop:
			for {
				select {
				case h := <-t.ch:
					t.process(h)
				default:
					return
				}
			}
		}
	}
}

func (t *FirstCrawlTracker) process(h Hit) {
	id := *h.EntityID
	if _, ok := t.seen[id]; ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), firstCrawlTimeout)
	defer cancel()
	published, inserted, err := t.store.RecordFirstCrawl(ctx, id, h.AgentID, h.At)
	if err != nil {
		t.logger.Warn("agent first crawl: store failed", slog.String("entity_id", id.String()), slog.String("error", err.Error()))
		return // not remembered: the next hit retries
	}
	if len(t.seen) >= firstCrawlSeenMax {
		clear(t.seen)
	}
	t.seen[id] = struct{}{}
	if inserted && t.onFirst != nil {
		t.onFirst(FirstCrawl{
			EntityID: id, AgentID: h.AgentID, Operator: h.Operator, Purpose: string(h.Purpose),
			At: h.At, PublishedAt: published,
		})
	}
}
