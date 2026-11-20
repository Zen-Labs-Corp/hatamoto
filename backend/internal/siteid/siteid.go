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
	mu sync.Mutex
	id string
}

// New returns a Store. It does not touch the database until Get is called.
func New(db DB) *Store { return &Store{db: db} }

// Get returns the site id, reading it once; later calls return the cached
// value. Errors are not cached, so the next call retries.
func (s *Store) Get(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.id != "" {
		return s.id, nil
	}
	var id string
	err := s.db.QueryRow(ctx, `SELECT site_id::text FROM hatamoto_site_identity WHERE id`).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrMissing
	}
	if err != nil {
		return "", fmt.Errorf("read site id: %w", err)
	}
	s.id = id
	return id, nil
}
