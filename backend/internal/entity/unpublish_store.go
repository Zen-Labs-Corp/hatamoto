package entity

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"hatamoto.dev/core/internal/database"
)

// Unpublish is an entity's scheduled unpublish: when it leaves the site and,
// optionally, where its old URL then points.
type Unpublish struct {
	EntityID       uuid.UUID
	At             time.Time
	RedirectTarget string
}

// UnpublishStore persists scheduled unpublishes in hatamoto_entity_schedule,
// a side table so the Entity SELECT lists stay unchanged.
type UnpublishStore struct {
	db *database.DB
}

// NewUnpublishStore builds the store.
func NewUnpublishStore(db *database.DB) *UnpublishStore { return &UnpublishStore{db: db} }

// Set stores (or replaces) the pending schedule.
func (s *UnpublishStore) Set(ctx context.Context, id uuid.UUID, at time.Time, target string) error {
	_, err := s.db.Pool().Exec(ctx, `
		INSERT INTO hatamoto_entity_schedule (entity_id, unpublish_at, redirect_target)
		VALUES ($1, $2, $3)
		ON CONFLICT (entity_id) DO UPDATE
		   SET unpublish_at = EXCLUDED.unpublish_at,
		       redirect_target = EXCLUDED.redirect_target`,
		id, at.UTC(), target)
	if err != nil {
		return fmt.Errorf("set unpublish: %w", err)
	}
	return nil
}

// Clear removes the pending schedule.
func (s *UnpublishStore) Clear(ctx context.Context, id uuid.UUID) error {
	_, err := s.db.Pool().Exec(ctx,
		`DELETE FROM hatamoto_entity_schedule WHERE entity_id = $1`, id)
	if err != nil {
		return fmt.Errorf("clear unpublish: %w", err)
	}
	return nil
}

// Get returns the schedule, or nil when none is set.
func (s *UnpublishStore) Get(ctx context.Context, id uuid.UUID) (*Unpublish, error) {
	m, err := s.Map(ctx, []uuid.UUID{id})
	if err != nil {
		return nil, err
	}
	if u, ok := m[id]; ok {
		return &u, nil
	}
	return nil, nil
}

// Map returns the schedules of the given entities that are still pending (a fired one is deleted).
func (s *UnpublishStore) Map(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]Unpublish, error) {
	out := map[uuid.UUID]Unpublish{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.db.Pool().Query(ctx, `
		SELECT entity_id, unpublish_at, redirect_target
		  FROM hatamoto_entity_schedule WHERE entity_id = ANY($1)`, ids)
	if err != nil {
		return nil, fmt.Errorf("load unpublish schedules: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var u Unpublish
		if err := rows.Scan(&u.EntityID, &u.At, &u.RedirectTarget); err != nil {
			return nil, fmt.Errorf("scan unpublish schedule: %w", err)
		}
		out[u.EntityID] = u
	}
	return out, rows.Err()
}

// SetRedirect records the redirect created when the entity was unpublished.
func (s *UnpublishStore) SetRedirect(ctx context.Context, id, redirectID uuid.UUID) error {
	_, err := s.db.Pool().Exec(ctx, `
		INSERT INTO hatamoto_unpublish_redirects (entity_id, redirect_id) VALUES ($1, $2)
		ON CONFLICT (entity_id) DO UPDATE SET redirect_id = EXCLUDED.redirect_id`, id, redirectID)
	if err != nil {
		return fmt.Errorf("record unpublish redirect: %w", err)
	}
	return nil
}

// TakeRedirect returns and forgets the redirect id created by an unpublish
// (nil when none).
func (s *UnpublishStore) TakeRedirect(ctx context.Context, id uuid.UUID) (*uuid.UUID, error) {
	var rid *uuid.UUID
	err := s.db.Pool().QueryRow(ctx,
		`DELETE FROM hatamoto_unpublish_redirects WHERE entity_id = $1 RETURNING redirect_id`, id).Scan(&rid)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("take unpublish redirect: %w", err)
	}
	return rid, nil
}
