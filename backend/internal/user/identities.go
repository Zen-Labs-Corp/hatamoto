package user

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"hatamoto.dev/core/internal/database"
)

var (
	// ErrIdentityNotFound means no identity row matches.
	ErrIdentityNotFound = errors.New("identity not found")
	// ErrIdentityTaken means the (issuer, subject) pair is linked to another user.
	ErrIdentityTaken = errors.New("identity is linked to another user")
)

// IdentityStore links users to the (issuer, subject) pairs of sign-in providers.
type IdentityStore struct {
	db *database.DB
}

// NewIdentityStore returns an IdentityStore on db.
func NewIdentityStore(db *database.DB) *IdentityStore {
	return &IdentityStore{db: db}
}

// UserFor returns the user linked to (issuer, subject), or ErrIdentityNotFound.
func (s *IdentityStore) UserFor(ctx context.Context, issuer, subject string) (uuid.UUID, error) {
	conn, err := s.db.GetConn(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	defer conn.Release()

	var id uuid.UUID
	err = conn.QueryRow(ctx,
		`SELECT user_id FROM hatamoto_user_identities WHERE issuer = $1 AND subject = $2`,
		issuer, subject).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrIdentityNotFound
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("failed to look up identity: %w", err)
	}
	return id, nil
}

// Link attaches (issuer, subject) to userID. Linking it again to the same user
// is a no-op; a pair linked to another user is ErrIdentityTaken.
func (s *IdentityStore) Link(ctx context.Context, userID uuid.UUID, issuer, subject string) error {
	conn, err := s.db.GetConn(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	// The no-op update makes RETURNING yield the row on conflict, so one
	// statement tells "already mine" from "someone else's".
	var owner uuid.UUID
	err = conn.QueryRow(ctx, `
		INSERT INTO hatamoto_user_identities (user_id, issuer, subject)
		VALUES ($1, $2, $3)
		ON CONFLICT (issuer, subject) DO UPDATE SET issuer = EXCLUDED.issuer
		RETURNING user_id`, userID, issuer, subject).Scan(&owner)
	if err != nil {
		return fmt.Errorf("failed to link identity: %w", err)
	}
	if owner != userID {
		return ErrIdentityTaken
	}
	return nil
}

// Unlink removes (issuer, subject) from userID, or returns ErrIdentityNotFound.
func (s *IdentityStore) Unlink(ctx context.Context, userID uuid.UUID, issuer, subject string) error {
	conn, err := s.db.GetConn(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	tag, err := conn.Exec(ctx,
		`DELETE FROM hatamoto_user_identities WHERE user_id = $1 AND issuer = $2 AND subject = $3`,
		userID, issuer, subject)
	if err != nil {
		return fmt.Errorf("failed to unlink identity: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrIdentityNotFound
	}
	return nil
}

// Touch records a sign-in through (issuer, subject).
func (s *IdentityStore) Touch(ctx context.Context, issuer, subject string) error {
	conn, err := s.db.GetConn(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx,
		`UPDATE hatamoto_user_identities SET last_used_at = now() WHERE issuer = $1 AND subject = $2`,
		issuer, subject); err != nil {
		return fmt.Errorf("failed to touch identity: %w", err)
	}
	return nil
}
