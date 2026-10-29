package seo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const cacheTTL = 30 * time.Second

// Querier is the part of a pgx pool the Store needs.
type Querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Store reads and writes the single hatamoto_seo_settings row, with a short
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
// The returned maps are shared with the cache: treat them as read-only.
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
	err := s.db.QueryRow(ctx, `SELECT settings FROM hatamoto_seo_settings WHERE id`).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return Defaults(), nil
	}
	if err != nil {
		return Defaults(), fmt.Errorf("load seo settings: %w", err)
	}
	return Decode(raw)
}

// ValidationError carries the field errors that stopped a save.
type ValidationError struct{ Errors []FieldError }

func (e *ValidationError) Error() string {
	if len(e.Errors) == 0 {
		return "invalid seo settings"
	}
	return "invalid seo settings: " + e.Errors[0].Field + ": " + e.Errors[0].Message
}

// Save validates set and stores it, then drops the cache.
func (s *Store) Save(ctx context.Context, set Settings) error {
	if errs := set.Validate(); len(errs) > 0 {
		return &ValidationError{Errors: errs}
	}
	raw, err := json.Marshal(set)
	if err != nil {
		return fmt.Errorf("encode seo settings: %w", err)
	}
	_, err = s.db.Exec(ctx, `
		INSERT INTO hatamoto_seo_settings (id, settings, updated_at)
		VALUES (TRUE, $1::jsonb, now())
		ON CONFLICT (id) DO UPDATE SET settings = EXCLUDED.settings, updated_at = now()`,
		string(raw))
	if err != nil {
		return fmt.Errorf("save seo settings: %w", err)
	}
	s.Invalidate()
	return nil
}

// txBeginner is implemented by a *pgxpool.Pool. Merge uses it when present;
// a Querier without it (a test fake) falls back to an unlocked read-modify-write.
type txBeginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// Merge reads the stored settings (never the cache), lets fn change the keys a
// tab owns, and saves the result, so one tab cannot overwrite another's keys.
// With a transaction-capable database the read-modify-write holds a row lock,
// so concurrent merges apply one after the other and none is lost.
func (s *Store) Merge(ctx context.Context, fn func(*Settings)) error {
	b, ok := s.db.(txBeginner)
	if !ok {
		cur, err := s.load(ctx)
		if err != nil {
			return err
		}
		fn(&cur)
		return s.Save(ctx, cur)
	}
	if err := pgx.BeginFunc(ctx, b, func(tx pgx.Tx) error { return s.MergeTx(ctx, tx, fn) }); err != nil {
		return err
	}
	s.Invalidate()
	return nil
}

// MergeTx is Merge inside the caller's transaction: it makes sure the row
// exists, locks it, applies fn to the locked value, validates and writes.
// It does not touch the cache: call Invalidate after the transaction commits.
func (s *Store) MergeTx(ctx context.Context, tx pgx.Tx, fn func(*Settings)) error {
	seed, err := json.Marshal(Defaults())
	if err != nil {
		return fmt.Errorf("encode seo settings: %w", err)
	}
	// Without a row FOR UPDATE would lock nothing, so the first save upserts.
	if _, err := tx.Exec(ctx, `
		INSERT INTO hatamoto_seo_settings (id, settings, updated_at)
		VALUES (TRUE, $1::jsonb, now())
		ON CONFLICT (id) DO NOTHING`, string(seed)); err != nil {
		return fmt.Errorf("seed seo settings: %w", err)
	}
	var raw []byte
	if err := tx.QueryRow(ctx, `SELECT settings FROM hatamoto_seo_settings WHERE id FOR UPDATE`).Scan(&raw); err != nil {
		return fmt.Errorf("lock seo settings: %w", err)
	}
	cur, err := Decode(raw)
	if err != nil {
		return err
	}
	fn(&cur)
	if errs := cur.Validate(); len(errs) > 0 {
		return &ValidationError{Errors: errs}
	}
	out, err := json.Marshal(cur)
	if err != nil {
		return fmt.Errorf("encode seo settings: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE hatamoto_seo_settings SET settings = $1::jsonb, updated_at = now() WHERE id`, string(out)); err != nil {
		return fmt.Errorf("save seo settings: %w", err)
	}
	return nil
}
