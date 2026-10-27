// Package analytics keeps the only first-party analytics Hatamoto needs:
// pageviews and AI-assistant referrals, rolled up per day. There are no
// visitors, sessions or per-event rows; crawler hits live in internal/agents.
package analytics

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"hatamoto.dev/core/internal/database"
)

// Rollup kinds stored in hatamoto_analytics_daily.kind.
const (
	KindPageView   = "pageview"
	KindAIReferral = "ai_referral"
)

// Repository adds to the daily rollup.
type Repository interface {
	// Add increments the (day, kind, key) counter by n.
	Add(ctx context.Context, day time.Time, kind, key string, n int64) error
}

// PostgresRepository is the Repository backed by hatamoto_analytics_daily.
type PostgresRepository struct{ db *database.DB }

// NewPostgresRepository creates the Postgres-backed repository.
func NewPostgresRepository(db *database.DB) *PostgresRepository {
	return &PostgresRepository{db: db}
}

// Add implements Repository.
func (r *PostgresRepository) Add(ctx context.Context, day time.Time, kind, key string, n int64) error {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return fmt.Errorf("get connection: %w", err)
	}
	defer conn.Release()
	_, err = conn.Exec(ctx, `
		INSERT INTO hatamoto_analytics_daily (day, kind, key, count)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (day, kind, key)
		DO UPDATE SET count = hatamoto_analytics_daily.count + EXCLUDED.count`,
		day, kind, key, n)
	if err != nil {
		return fmt.Errorf("add %s: %w", kind, err)
	}
	return nil
}

// flushInterval is how often buffered counts are written to the rollup.
const flushInterval = 30 * time.Second

type bucket struct {
	day       time.Time
	kind, key string
}

// Service records pageviews and AI referrals. Counts are buffered in memory and
// written by Flush (every flushInterval once Start is called, and on Close), so
// a busy site does not hammer one hot rollup row per request.
type Service struct {
	repo   Repository
	logger *slog.Logger
	now    func() time.Time

	mu      sync.Mutex
	pending map[bucket]int64

	stop chan struct{}
	done chan struct{}
}

// NewService creates the recording service.
func NewService(repo Repository, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{repo: repo, logger: logger, now: time.Now, pending: map[bucket]int64{}}
}

func (s *Service) today() time.Time {
	y, m, d := s.now().UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func (s *Service) add(kind, key string, n int64) {
	if n <= 0 {
		return
	}
	s.mu.Lock()
	s.pending[bucket{s.today(), kind, key}] += n
	s.mu.Unlock()
}

// RecordPageViews buffers n pageviews for today.
func (s *Service) RecordPageViews(n int64) { s.add(KindPageView, "", n) }

// RecordAIReferral buffers one visit referred by the AI assistant source
// (chatgpt, claude, ...).
func (s *Service) RecordAIReferral(source string) { s.add(KindAIReferral, source, 1) }

// Flush writes the buffered counts. A failed write is put back so the next
// flush retries it.
func (s *Service) Flush(ctx context.Context) error {
	s.mu.Lock()
	batch := s.pending
	s.pending = map[bucket]int64{}
	s.mu.Unlock()

	var firstErr error
	for b, n := range batch {
		if err := s.repo.Add(ctx, b.day, b.kind, b.key, n); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			s.mu.Lock()
			s.pending[b] += n
			s.mu.Unlock()
		}
	}
	return firstErr
}

// Start runs the periodic flush until Close.
func (s *Service) Start() {
	s.stop, s.done = make(chan struct{}), make(chan struct{})
	go func() {
		defer close(s.done)
		t := time.NewTicker(flushInterval)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				if err := s.Flush(context.Background()); err != nil {
					s.logger.Warn("analytics: flush failed", slog.String("error", err.Error()))
				}
			case <-s.stop:
				return
			}
		}
	}()
}

// Close stops the periodic flush and writes what is still buffered.
func (s *Service) Close(ctx context.Context) {
	if s.stop != nil {
		close(s.stop)
		<-s.done
		s.stop = nil
	}
	if err := s.Flush(ctx); err != nil {
		s.logger.Warn("analytics: final flush failed", slog.String("error", err.Error()))
	}
}
