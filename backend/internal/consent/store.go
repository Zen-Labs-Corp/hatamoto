package consent

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"hatamoto.dev/core/internal/jsonx"
)

const cacheTTL = 30 * time.Second

// Querier is the part of a pgx pool the Store and Counter need.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Store reads and writes the single hatamoto_privacy_settings row, with a short
// cache because every public page render reads it.
type Store struct {
	db  Querier
	now func() time.Time

	mu       sync.Mutex
	cached   Settings
	cachedAt time.Time
	have     bool
	gen      uint64 // bumped by Invalidate, so a load that raced a save is not cached
}

// NewStore returns a Store over db (a *pgxpool.Pool fits).
func NewStore(db Querier) *Store { return &Store{db: db, now: time.Now} }

// Get returns the stored settings, or Defaults when no row exists. A warm cache
// does not touch the database; on a read error the last good copy is served.
func (s *Store) Get(ctx context.Context) (Settings, error) {
	s.mu.Lock()
	if s.have && s.now().Sub(s.cachedAt) < cacheTTL {
		c := s.cached
		s.mu.Unlock()
		return c, nil
	}
	stale, haveStale, gen := s.cached, s.have, s.gen
	s.mu.Unlock()

	set, err := s.load(ctx)
	if err != nil {
		if haveStale {
			return stale, nil
		}
		return Defaults(), err
	}
	s.mu.Lock()
	if s.gen == gen {
		s.cached, s.cachedAt, s.have = set, s.now(), true
	}
	s.mu.Unlock()
	return set, nil
}

// Invalidate drops the cached copy so the next Get reads the database.
func (s *Store) Invalidate() {
	s.mu.Lock()
	s.have = false
	s.gen++
	s.mu.Unlock()
}

func (s *Store) load(ctx context.Context) (Settings, error) {
	var raw []byte
	err := s.db.QueryRow(ctx, `SELECT settings FROM hatamoto_privacy_settings WHERE id`).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return Defaults(), nil
	}
	if err != nil {
		return Defaults(), fmt.Errorf("load consent settings: %w", err)
	}
	return Decode(raw)
}

// ValidationError carries the field errors that stopped a save.
type ValidationError struct{ Errors []FieldError }

func (e *ValidationError) Error() string {
	if len(e.Errors) == 0 {
		return "invalid consent settings"
	}
	return "invalid consent settings: " + e.Errors[0].Field + ": " + e.Errors[0].Message
}

// Save validates set and stores it, then drops the cache.
func (s *Store) Save(ctx context.Context, set Settings) error {
	if errs := set.Validate(); len(errs) > 0 {
		return &ValidationError{Errors: errs}
	}
	raw, err := jsonx.Marshal(set)
	if err != nil {
		return fmt.Errorf("encode consent settings: %w", err)
	}
	_, err = s.db.Exec(ctx, `
		INSERT INTO hatamoto_privacy_settings (id, settings, updated_at)
		VALUES (TRUE, $1::jsonb, now())
		ON CONFLICT (id) DO UPDATE SET settings = EXCLUDED.settings, updated_at = now()`,
		string(raw))
	if err != nil {
		return fmt.Errorf("save consent settings: %w", err)
	}
	s.Invalidate()
	return nil
}
