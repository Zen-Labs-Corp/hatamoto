// Package siteid reads the install's stable site id (hatamoto_site_identity).
// Core never sends it anywhere; it is exposed only through the edition package.
package siteid

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/jackc/pgx/v5"
)

// DB is what Store needs; *pgxpool.Pool satisfies it.
type DB interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// ErrMissing means the identity row does not exist: the migration has not run.
var ErrMissing = errors.New("site id missing: run hatamoto migrate")

// Store reads the site id lazily and caches it.
type Store struct {
	db DB
	mu sync.Mutex // guards id only; never held across a query
	id string
}

// New returns a Store. It does not touch the database until Get is called.
func New(db DB) *Store { return &Store{db: db} }

// Get returns the site id, reading it once; later calls return the cached
// value. Errors are not cached, so the next call retries. The query runs
// without the lock and with the caller's ctx, so a slow database never makes
// other callers wait past their own deadline. Concurrent first calls may each
// query; the id is the same row, so they store the same value.
func (s *Store) Get(ctx context.Context) (string, error) {
	s.mu.Lock()
	id := s.id
	s.mu.Unlock()
	if id != "" {
		return id, nil
	}
	err := s.db.QueryRow(ctx, `SELECT site_id::text FROM hatamoto_site_identity WHERE id`).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrMissing
	}
	if err != nil {
		return "", fmt.Errorf("read site id: %w", err)
	}
	s.mu.Lock()
	s.id = id
	s.mu.Unlock()
	return id, nil
}
