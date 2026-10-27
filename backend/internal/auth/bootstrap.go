package auth

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/user"
)

// ErrSetupDone is returned by CreateFirstOwner when a user already exists.
var ErrSetupDone = errors.New("setup already completed")

// setupLockKey is the advisory-lock key serialising first-owner creation.
const setupLockKey int64 = 0x68746d5f73657475 // "htm_setu"

// Bootstrap creates the first owner of a fresh install.
type Bootstrap struct{ db *database.DB }

// NewBootstrap builds the service.
func NewBootstrap(db *database.DB) *Bootstrap { return &Bootstrap{db: db} }

// CreateFirstOwner inserts an owner only while the users table is empty. The
// check and the insert run in one transaction under an advisory lock, so two
// people racing through the setup wizard cannot both become owner: the loser
// gets ErrSetupDone.
func (b *Bootstrap) CreateFirstOwner(ctx context.Context, name, email, password string) (*user.User, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" || !strings.Contains(email, "@") {
		return nil, errors.New("enter a valid email address")
	}
	if len(password) < MinPasswordLen {
		return nil, ErrPasswordShort
	}
	hash, err := HashPassword(password)
	if err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	var namePtr *string
	if name != "" {
		namePtr = &name
	}
	var u user.User
	err = b.db.WithTx(database.WithSuperadmin(ctx), func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, setupLockKey); err != nil {
			return err
		}
		err := tx.QueryRow(ctx, `
			INSERT INTO hatamoto_users (id, email, password_hash, role, name, created_at, updated_at)
			SELECT $1, $2, $3, 'owner', $4, NOW(), NOW()
			WHERE NOT EXISTS (SELECT 1 FROM hatamoto_users)
			RETURNING id, email, role, name`, uuid.New(), email, hash, namePtr).Scan(&u.ID, &u.Email, &u.Role, &u.Name)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrSetupDone
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return &u, nil
}
