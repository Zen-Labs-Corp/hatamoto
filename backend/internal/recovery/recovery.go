// Package recovery keeps the unsaved work of an editor or builder session on
// the server: one copy per (user, entity), so a closed tab or another browser
// can offer it back. Nothing is kept in the browser.
package recovery

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"hatamoto.dev/core/internal/database"
)

// Copy is one saved recovery copy. Content is the post body (blog) or the
// builder document as JSON text (builder). BaseVersion is the entity version
// the copy was started from.
type Copy struct {
	Content       string
	Name          string
	Excerpt       string
	FeaturedImage string
	BaseVersion   int
	SavedAt       time.Time
}

// Store reads and writes recovery copies in hatamoto_admin_recovery_copies.
type Store struct{ pool *pgxpool.Pool }

// New returns a Store on db's pool.
func New(db *database.DB) *Store { return &Store{pool: db.Pool()} }

// Put stores c for (user, entity), replacing an earlier copy. SavedAt is set
// by the database.
func (s *Store) Put(ctx context.Context, user, entity uuid.UUID, c Copy) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO hatamoto_admin_recovery_copies
			(user_id, entity_id, content, name, excerpt, featured_image, base_version, saved_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, now())
		ON CONFLICT (user_id, entity_id) DO UPDATE SET
			content = EXCLUDED.content, name = EXCLUDED.name, excerpt = EXCLUDED.excerpt,
			featured_image = EXCLUDED.featured_image, base_version = EXCLUDED.base_version,
			saved_at = now()`,
		user, entity, c.Content, c.Name, c.Excerpt, c.FeaturedImage, c.BaseVersion)
	if err != nil {
		return fmt.Errorf("recovery put: %w", err)
	}
	return nil
}

// Get returns the copy of (user, entity), or nil, nil when there is none.
func (s *Store) Get(ctx context.Context, user, entity uuid.UUID) (*Copy, error) {
	var c Copy
	err := s.pool.QueryRow(ctx, `
		SELECT content, name, excerpt, featured_image, base_version, saved_at
		FROM hatamoto_admin_recovery_copies WHERE user_id = $1 AND entity_id = $2`,
		user, entity).Scan(&c.Content, &c.Name, &c.Excerpt, &c.FeaturedImage, &c.BaseVersion, &c.SavedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("recovery get: %w", err)
	}
	return &c, nil
}

// Delete removes the copy of (user, entity); no copy is not an error.
func (s *Store) Delete(ctx context.Context, user, entity uuid.UUID) error {
	if _, err := s.pool.Exec(ctx,
		`DELETE FROM hatamoto_admin_recovery_copies WHERE user_id = $1 AND entity_id = $2`,
		user, entity); err != nil {
		return fmt.Errorf("recovery delete: %w", err)
	}
	return nil
}
