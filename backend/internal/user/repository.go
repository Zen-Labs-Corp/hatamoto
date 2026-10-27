package user

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"hatamoto.dev/core/internal/database"
)

// Repository defines the interface for user data operations.
//
// Path B PR 2b/3: List and Count no longer take a tenantID — there is only
// one tenant. The signature change forces callers off the legacy code path
// rather than silently ignoring the argument.
type Repository interface {
	Create(ctx context.Context, input *CreateUserInput, passwordHash string) (*User, error)
	GetByID(ctx context.Context, id uuid.UUID) (*User, error)
	GetByEmail(ctx context.Context, email string) (*User, error)
	ListByEmail(ctx context.Context, email string) ([]*User, error)
	Update(ctx context.Context, id uuid.UUID, input *UpdateUserInput) (*User, error)
	UpdateLastLogin(ctx context.Context, id uuid.UUID) error
	// RecordFailedLogin increments the consecutive-failure counter and, once it
	// reaches maxAttempts, sets locked_until to now()+lockFor.
	RecordFailedLogin(ctx context.Context, id uuid.UUID, maxAttempts int, lockFor time.Duration) error
	// ResetFailedLogins clears the failure counter and any lock (called on a
	// successful login).
	ResetFailedLogins(ctx context.Context, id uuid.UUID) error
	Delete(ctx context.Context, id uuid.UUID) error
	List(ctx context.Context, limit, offset int) ([]*User, error)
	Count(ctx context.Context) (int, error)
}

// PostgresRepository implements Repository using PostgreSQL.
type PostgresRepository struct {
	db *database.DB
}

// NewPostgresRepository creates a new PostgreSQL user repository.
func NewPostgresRepository(db *database.DB) *PostgresRepository {
	return &PostgresRepository{db: db}
}

// Create inserts a new user into the database. Multi-tenancy was removed
// in Path B; hatamoto_users has no tenant_id column and the User model has
// no TenantID field.
func (r *PostgresRepository) Create(ctx context.Context, input *CreateUserInput, passwordHash string) (*User, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	id := uuid.New()
	now := time.Now()

	query := `
		INSERT INTO hatamoto_users (
			id, email, password_hash, role, name, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, email, role, name, created_at, updated_at
	`

	var u User
	err = conn.QueryRow(ctx, query,
		id, input.Email, passwordHash, input.Role, input.Name, now, now,
	).Scan(&u.ID, &u.Email, &u.Role, &u.Name, &u.CreatedAt, &u.UpdatedAt)

	if err != nil {
		if isPgUniqueViolation(err) {
			return nil, ErrEmailAlreadyExists
		}
		return nil, fmt.Errorf("failed to create user: %w", err)
	}

	return &u, nil
}

// GetByID retrieves a user by ID.
func (r *PostgresRepository) GetByID(ctx context.Context, id uuid.UUID) (*User, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	query := `
		SELECT id, email, password_hash, role, name, avatar_url,
		       last_login_at, created_at, updated_at
		FROM hatamoto_users
		WHERE id = $1
	`

	var u User
	err = conn.QueryRow(ctx, query, id).Scan(
		&u.ID, &u.Email, &u.PasswordHash, &u.Role, &u.Name,
		&u.AvatarURL, &u.LastLoginAt, &u.CreatedAt, &u.UpdatedAt,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	return &u, nil
}

// GetByEmail retrieves a user by email address.
func (r *PostgresRepository) GetByEmail(ctx context.Context, email string) (*User, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	query := `
		SELECT id, email, password_hash, role, name, avatar_url,
		       last_login_at, failed_login_attempts, locked_until,
		       created_at, updated_at
		FROM hatamoto_users
		WHERE email = $1
	`

	var u User
	err = conn.QueryRow(ctx, query, email).Scan(
		&u.ID, &u.Email, &u.PasswordHash, &u.Role, &u.Name,
		&u.AvatarURL, &u.LastLoginAt, &u.FailedLoginAttempts, &u.LockedUntil,
		&u.CreatedAt, &u.UpdatedAt,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("failed to get user by email: %w", err)
	}

	return &u, nil
}

// ListByEmail retrieves all users with the given email. Pre-Path-B this
// scanned across tenants for the multi-tenant cross-account login feature;
// post-strip the result set is at most 1 row but the API contract stays
// the same so callers (auth.Service) don't need adjusting.
func (r *PostgresRepository) ListByEmail(ctx context.Context, email string) ([]*User, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	query := `
		SELECT id, email, password_hash, role, name, avatar_url,
		       last_login_at, created_at, updated_at
		FROM hatamoto_users
		WHERE email = $1
		ORDER BY created_at
	`

	rows, err := conn.Query(ctx, query, email)
	if err != nil {
		return nil, fmt.Errorf("failed to list users by email: %w", err)
	}
	defer rows.Close()

	var users []*User
	for rows.Next() {
		var u User
		if err := rows.Scan(
			&u.ID, &u.Email, &u.PasswordHash, &u.Role, &u.Name,
			&u.AvatarURL, &u.LastLoginAt, &u.CreatedAt, &u.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan user: %w", err)
		}
		users = append(users, &u)
	}

	return users, rows.Err()
}

// Update modifies a user's information.
func (r *PostgresRepository) Update(ctx context.Context, id uuid.UUID, input *UpdateUserInput) (*User, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	// Demoting an owner must leave another owner; check and update under one
	// advisory lock so two owners demoting each other cannot both succeed.
	if input.Role != nil && *input.Role != RoleOwner {
		tx, err := conn.Begin(ctx)
		if err != nil {
			return nil, err
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if err := guardLastOwner(ctx, tx, id); err != nil {
			return nil, err
		}
		u, err := updateUser(ctx, tx, id, input)
		if err != nil {
			return nil, err
		}
		return u, tx.Commit(ctx)
	}
	return updateUser(ctx, conn, id, input)
}

// ownerGuardKey is the advisory-lock key serialising owner removal/demotion.
const ownerGuardKey int64 = 0x68746d5f6f776e67 // "htm_owng"

type queryer interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
}

// guardLastOwner takes the owner lock and returns ErrLastOwner when id is the
// only owner. A missing user passes (the caller's statement reports it).
func guardLastOwner(ctx context.Context, q queryer, id uuid.UUID) error {
	if _, err := q.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, ownerGuardKey); err != nil {
		return err
	}
	var isOwner bool
	var others int
	err := q.QueryRow(ctx, `SELECT
		COALESCE((SELECT role = 'owner' FROM hatamoto_users WHERE id = $1), false),
		(SELECT count(*) FROM hatamoto_users WHERE role = 'owner' AND id <> $1)`, id).Scan(&isOwner, &others)
	if err != nil {
		return err
	}
	if isOwner && others == 0 {
		return ErrLastOwner
	}
	return nil
}

func updateUser(ctx context.Context, conn queryer, id uuid.UUID, input *UpdateUserInput) (*User, error) {
	query := `
		UPDATE hatamoto_users
		SET name = COALESCE($2, name),
		    avatar_url = COALESCE($3, avatar_url),
		    role = COALESCE($4, role),
		    updated_at = NOW()
		WHERE id = $1
		RETURNING id, email, role, name, avatar_url, last_login_at, created_at, updated_at
	`

	var roleStr *string
	if input.Role != nil {
		s := string(*input.Role)
		roleStr = &s
	}

	var u User
	err := conn.QueryRow(ctx, query, id, input.Name, input.AvatarURL, roleStr).Scan(
		&u.ID, &u.Email, &u.Role, &u.Name,
		&u.AvatarURL, &u.LastLoginAt, &u.CreatedAt, &u.UpdatedAt,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("failed to update user: %w", err)
	}

	return &u, nil
}

// UpdateLastLogin updates the user's last login timestamp.
func (r *PostgresRepository) UpdateLastLogin(ctx context.Context, id uuid.UUID) error {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	query := `UPDATE hatamoto_users SET last_login_at = NOW() WHERE id = $1`
	_, err = conn.Exec(ctx, query, id)
	return err
}

// RecordFailedLogin increments the failure counter and locks the account once
// the threshold is reached, in a single statement.
func (r *PostgresRepository) RecordFailedLogin(ctx context.Context, id uuid.UUID, maxAttempts int, lockFor time.Duration) error {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	// lockUntil is precomputed in Go (pgx encodes time.Time, not time.Duration).
	lockUntil := time.Now().Add(lockFor)
	// If a PRIOR lock has already expired, this failure starts a fresh count of
	// 1 (and clears the stale lock) rather than immediately re-locking. Without
	// this, one failed attempt every lock-window would keep an account locked
	// forever after its first lockout.
	query := `
		UPDATE hatamoto_users
		SET failed_login_attempts = CASE
		        WHEN locked_until IS NOT NULL AND locked_until < NOW() THEN 1
		        ELSE failed_login_attempts + 1
		    END,
		    locked_until = CASE
		        WHEN locked_until IS NOT NULL AND locked_until < NOW() THEN NULL
		        WHEN failed_login_attempts + 1 >= $2 THEN $3
		        ELSE locked_until
		    END,
		    updated_at = NOW()
		WHERE id = $1`
	_, err = conn.Exec(ctx, query, id, maxAttempts, lockUntil)
	return err
}

// ResetFailedLogins clears the failure counter and unlocks the account.
func (r *PostgresRepository) ResetFailedLogins(ctx context.Context, id uuid.UUID) error {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	query := `UPDATE hatamoto_users SET failed_login_attempts = 0, locked_until = NULL, updated_at = NOW() WHERE id = $1`
	_, err = conn.Exec(ctx, query, id)
	return err
}

// SetPassword replaces a user's password hash and clears any login lockout.
func (r *PostgresRepository) SetPassword(ctx context.Context, id uuid.UUID, passwordHash string) error {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	tag, err := conn.Exec(ctx, `
		UPDATE hatamoto_users
		SET password_hash = $2, failed_login_attempts = 0, locked_until = NULL, updated_at = NOW()
		WHERE id = $1`, id, passwordHash)
	if err != nil {
		return fmt.Errorf("failed to set password: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrUserNotFound
	}
	return nil
}

// Delete removes a user from the database.
func (r *PostgresRepository) Delete(ctx context.Context, id uuid.UUID) error {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// The last owner cannot be deleted (checked under the owner lock).
	if err := guardLastOwner(ctx, tx, id); err != nil {
		return err
	}
	result, err := tx.Exec(ctx, `DELETE FROM hatamoto_users WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("failed to delete user: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrUserNotFound
	}
	return tx.Commit(ctx)
}

// List retrieves users with pagination.
//
// Path B PR 2b/3: dropped the tenantID parameter — there's a single tenant
// so per-tenant filtering is meaningless. Callers must update their call
// sites accordingly.
func (r *PostgresRepository) List(ctx context.Context, limit, offset int) ([]*User, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	query := `
		SELECT id, email, role, name, avatar_url, last_login_at, created_at, updated_at
		FROM hatamoto_users
		ORDER BY created_at DESC
		LIMIT $1 OFFSET $2
	`

	rows, err := conn.Query(ctx, query, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to list users: %w", err)
	}
	defer rows.Close()

	var users []*User
	for rows.Next() {
		var u User
		if err := rows.Scan(
			&u.ID, &u.Email, &u.Role, &u.Name,
			&u.AvatarURL, &u.LastLoginAt, &u.CreatedAt, &u.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan user: %w", err)
		}
		users = append(users, &u)
	}

	return users, rows.Err()
}

// Count returns the total number of users.
//
// Path B PR 2b/3: dropped the tenantID parameter (see List).
func (r *PostgresRepository) Count(ctx context.Context) (int, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return 0, err
	}
	defer conn.Release()

	var count int
	err = conn.QueryRow(ctx, `SELECT COUNT(*) FROM hatamoto_users`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count users: %w", err)
	}

	return count, nil
}

// isPgUniqueViolation checks if an error is a PostgreSQL unique constraint violation.
func isPgUniqueViolation(err error) bool {
	// pgx wraps errors, so we check the error message
	return err != nil && !errors.Is(err, pgx.ErrNoRows) &&
		(fmt.Sprintf("%v", err) == "ERROR: duplicate key value violates unique constraint" ||
			containsString(err.Error(), "duplicate key") ||
			containsString(err.Error(), "23505"))
}

func containsString(s, substr string) bool {
	return strings.Contains(s, substr)
}
