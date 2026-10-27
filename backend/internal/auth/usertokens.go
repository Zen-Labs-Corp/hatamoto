package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/user"
)

// MinPasswordLen is the shortest password the admin accepts (also used by the
// CLI): length is the only rule, no composition requirements.
const MinPasswordLen = 12

// Token lifetimes and the per-user cap on reset mails.
const (
	ResetTokenTTL   = time.Hour
	InviteTokenTTL  = 7 * 24 * time.Hour
	maxResetsPerHr  = 3
	tokenKindReset  = "password_reset"
	tokenKindInvite = "invite"
)

// Errors of the one-time token flows. ErrTokenInvalid deliberately covers
// unknown, expired and already-used tokens alike.
var (
	ErrTokenInvalid  = errors.New("invalid or expired token")
	ErrInvalidEmail  = errors.New("enter a valid email address")
	ErrPasswordShort = fmt.Errorf("password must be at least %d characters", MinPasswordLen)
	ErrResetThrottle = errors.New("too many password reset requests; try again later")
)

// Invite is a pending invitation.
type Invite struct {
	ID        uuid.UUID
	Email     string
	Role      user.Role
	InvitedBy *uuid.UUID
	ExpiresAt time.Time
	CreatedAt time.Time
}

// UserTokens issues and consumes password-reset and invite tokens. Consuming a
// token is a single UPDATE ... WHERE used_at IS NULL AND expires_at > now()
// RETURNING, so a token works exactly once even under concurrent requests.
type UserTokens struct {
	db  *database.DB
	now func() time.Time
}

// NewUserTokens builds the service.
func NewUserTokens(db *database.DB) *UserTokens {
	return &UserTokens{db: db, now: time.Now}
}

func newToken() (token string, hash []byte, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", nil, err
	}
	token = base64.RawURLEncoding.EncodeToString(b)
	return token, hashToken(token), nil
}

func hashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

func (t *UserTokens) tx(ctx context.Context, fn func(pgx.Tx) error) error {
	return t.db.WithTx(database.WithSuperadmin(ctx), fn)
}

// IssueReset creates a reset token for userID (valid ResetTokenTTL). Earlier
// unused reset tokens of that user stop working. More than three requests an
// hour for one user return ErrResetThrottle.
func (t *UserTokens) IssueReset(ctx context.Context, userID uuid.UUID) (string, error) {
	token, hash, err := newToken()
	if err != nil {
		return "", err
	}
	now := t.now()
	err = t.tx(ctx, func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM hatamoto_user_tokens WHERE user_id = $1 AND kind = $2 AND created_at > $3`,
			userID, tokenKindReset, now.Add(-time.Hour)).Scan(&n); err != nil {
			return err
		}
		if n >= maxResetsPerHr {
			return ErrResetThrottle
		}
		if _, err := tx.Exec(ctx, `UPDATE hatamoto_user_tokens SET used_at = $3 WHERE user_id = $1 AND kind = $2 AND used_at IS NULL`,
			userID, tokenKindReset, now); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO hatamoto_user_tokens (kind, token_hash, user_id, expires_at, created_at) VALUES ($1, $2, $3, $4, $5)`,
			tokenKindReset, hash, userID, now.Add(ResetTokenTTL), now)
		return err
	})
	if err != nil {
		return "", err
	}
	return token, nil
}

// ResetValid reports whether token is a live reset token, without consuming it
// (the reset page checks it before showing the form).
func (t *UserTokens) ResetValid(ctx context.Context, token string) bool {
	return t.live(ctx, token, tokenKindReset)
}

func (t *UserTokens) live(ctx context.Context, token, kind string) bool {
	if token == "" {
		return false
	}
	conn, err := t.db.GetConn(database.WithSuperadmin(ctx))
	if err != nil {
		return false
	}
	defer conn.Release()
	var ok bool
	err = conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM hatamoto_user_tokens WHERE token_hash = $1 AND kind = $2 AND used_at IS NULL AND expires_at > $3)`,
		hashToken(token), kind, t.now()).Scan(&ok)
	return err == nil && ok
}

// ConsumeReset sets the new password for the token's user, clears the login
// lockout, revokes every session of that user and burns the token, all in one
// transaction. The returned user may be signed in with a fresh session.
func (t *UserTokens) ConsumeReset(ctx context.Context, token, newPassword string) (*user.User, error) {
	if len(newPassword) < MinPasswordLen {
		return nil, ErrPasswordShort
	}
	// A junk token costs one lookup, not a 64 MB hash.
	if !t.live(ctx, token, tokenKindReset) {
		return nil, ErrTokenInvalid
	}
	hash, err := HashPasswordContext(ctx, newPassword)
	if err != nil {
		return nil, err
	}
	var u user.User
	err = t.tx(ctx, func(tx pgx.Tx) error {
		var uid uuid.UUID
		err := tx.QueryRow(ctx, `UPDATE hatamoto_user_tokens SET used_at = $3 WHERE token_hash = $1 AND kind = $2 AND used_at IS NULL AND expires_at > $3 RETURNING user_id`,
			hashToken(token), tokenKindReset, t.now()).Scan(&uid)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrTokenInvalid
		}
		if err != nil {
			return err
		}
		err = tx.QueryRow(ctx, `UPDATE hatamoto_users SET password_hash = $2, failed_login_attempts = 0, locked_until = NULL, updated_at = NOW() WHERE id = $1 RETURNING id, email, role, name`,
			uid, hash).Scan(&u.ID, &u.Email, &u.Role, &u.Name)
		if err != nil {
			return ErrTokenInvalid
		}
		_, err = tx.Exec(ctx, `DELETE FROM hatamoto_sessions WHERE user_id = $1 AND kind IN ('admin','api')`, uid)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// IssueInvite creates an invite (valid InviteTokenTTL) for email with role. An
// address that already has an account is rejected with user.ErrEmailAlreadyExists;
// an earlier pending invite for the same address is replaced.
func (t *UserTokens) IssueInvite(ctx context.Context, email string, role user.Role, invitedBy uuid.UUID) (string, *Invite, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" || !strings.Contains(email, "@") {
		return "", nil, ErrInvalidEmail
	}
	if !user.ValidRole(role) {
		return "", nil, user.ErrInvalidRole
	}
	token, hash, err := newToken()
	if err != nil {
		return "", nil, err
	}
	now := t.now()
	inv := &Invite{Email: email, Role: role, InvitedBy: &invitedBy, ExpiresAt: now.Add(InviteTokenTTL), CreatedAt: now}
	err = t.tx(ctx, func(tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM hatamoto_users WHERE lower(email) = $1)`, email).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return user.ErrEmailAlreadyExists
		}
		if _, err := tx.Exec(ctx, `DELETE FROM hatamoto_user_tokens WHERE kind = $1 AND email = $2 AND used_at IS NULL`, tokenKindInvite, email); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `INSERT INTO hatamoto_user_tokens (kind, token_hash, email, role, invited_by, expires_at, created_at) VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
			tokenKindInvite, hash, email, string(role), invitedBy, inv.ExpiresAt, now).Scan(&inv.ID)
	})
	if err != nil {
		return "", nil, err
	}
	return token, inv, nil
}

// PeekInvite returns the invite behind a live token without consuming it (the
// accept page shows the address and role).
func (t *UserTokens) PeekInvite(ctx context.Context, token string) (*Invite, error) {
	conn, err := t.db.GetConn(database.WithSuperadmin(ctx))
	if err != nil {
		return nil, err
	}
	defer conn.Release()
	var inv Invite
	var role string
	err = conn.QueryRow(ctx, `SELECT id, email, role, invited_by, expires_at, created_at FROM hatamoto_user_tokens WHERE token_hash = $1 AND kind = $2 AND used_at IS NULL AND expires_at > $3`,
		hashToken(token), tokenKindInvite, t.now()).Scan(&inv.ID, &inv.Email, &role, &inv.InvitedBy, &inv.ExpiresAt, &inv.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrTokenInvalid
	}
	if err != nil {
		return nil, err
	}
	inv.Role = user.Role(role)
	return &inv, nil
}

// ConsumeInvite creates the invited user (name, password) and burns the token
// in one transaction.
func (t *UserTokens) ConsumeInvite(ctx context.Context, token, name, password string) (*user.User, error) {
	if len(password) < MinPasswordLen {
		return nil, ErrPasswordShort
	}
	name = strings.TrimSpace(name)
	// A junk token costs one lookup, not a 64 MB hash.
	if _, err := t.PeekInvite(ctx, token); err != nil {
		return nil, err
	}
	hash, err := HashPasswordContext(ctx, password)
	if err != nil {
		return nil, err
	}
	var u user.User
	err = t.tx(ctx, func(tx pgx.Tx) error {
		var email, role string
		err := tx.QueryRow(ctx, `UPDATE hatamoto_user_tokens SET used_at = $3 WHERE token_hash = $1 AND kind = $2 AND used_at IS NULL AND expires_at > $3 RETURNING email, role`,
			hashToken(token), tokenKindInvite, t.now()).Scan(&email, &role)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrTokenInvalid
		}
		if err != nil {
			return err
		}
		var namePtr *string
		if name != "" {
			namePtr = &name
		}
		err = tx.QueryRow(ctx, `INSERT INTO hatamoto_users (id, email, password_hash, role, name, created_at, updated_at) VALUES ($1, $2, $3, $4, $5, NOW(), NOW()) RETURNING id, email, role, name`,
			uuid.New(), email, hash, role, namePtr).Scan(&u.ID, &u.Email, &u.Role, &u.Name)
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique_violation
				return user.ErrEmailAlreadyExists
			}
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// ListInvites returns the unexpired, unused invites, newest first.
func (t *UserTokens) ListInvites(ctx context.Context) ([]Invite, error) {
	conn, err := t.db.GetConn(database.WithSuperadmin(ctx))
	if err != nil {
		return nil, err
	}
	defer conn.Release()
	rows, err := conn.Query(ctx, `SELECT id, email, role, invited_by, expires_at, created_at FROM hatamoto_user_tokens WHERE kind = $1 AND used_at IS NULL AND expires_at > $2 ORDER BY created_at DESC`,
		tokenKindInvite, t.now())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Invite
	for rows.Next() {
		var inv Invite
		var role string
		if err := rows.Scan(&inv.ID, &inv.Email, &role, &inv.InvitedBy, &inv.ExpiresAt, &inv.CreatedAt); err != nil {
			return nil, err
		}
		inv.Role = user.Role(role)
		out = append(out, inv)
	}
	return out, rows.Err()
}

// RevokeInvite deletes a pending invite.
func (t *UserTokens) RevokeInvite(ctx context.Context, id uuid.UUID) error {
	conn, err := t.db.GetConn(database.WithSuperadmin(ctx))
	if err != nil {
		return err
	}
	defer conn.Release()
	_, err = conn.Exec(ctx, `DELETE FROM hatamoto_user_tokens WHERE id = $1 AND kind = $2 AND used_at IS NULL`, id, tokenKindInvite)
	return err
}

// PurgeExpired removes tokens that expired or were used more than a day ago.
func (t *UserTokens) PurgeExpired(ctx context.Context) (int64, error) {
	conn, err := t.db.GetConn(database.WithSuperadmin(ctx))
	if err != nil {
		return 0, err
	}
	defer conn.Release()
	tag, err := conn.Exec(ctx, `DELETE FROM hatamoto_user_tokens WHERE expires_at < $1 OR used_at < $1`, t.now().Add(-24*time.Hour))
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// ChangePassword sets a new password after checking the current one, clears
// the lockout and signs out every other session of the user (keepSession, the
// caller's own, survives).
func (t *UserTokens) ChangePassword(ctx context.Context, userID uuid.UUID, current, newPassword string, keepSession *uuid.UUID) error {
	if len(newPassword) < MinPasswordLen {
		return ErrPasswordShort
	}
	// Both argon2 runs happen before the transaction: waiting for a hashing
	// slot must not hold a pool connection or the row lock.
	old, err := t.passwordHash(ctx, userID)
	if err != nil {
		return err
	}
	ok, err := VerifyPasswordContext(ctx, current, old)
	if cerr := ctx.Err(); cerr != nil {
		return cerr
	}
	if err != nil || !ok {
		err = ErrInvalidCredentials
	} else {
		var hash string
		if hash, err = HashPasswordContext(ctx, newPassword); err == nil {
			err = t.setPassword(ctx, userID, old, hash, keepSession)
		}
	}
	if errors.Is(err, ErrInvalidCredentials) {
		// Wrong current password counts like a wrong login (same lockout), so a
		// stolen session cannot guess it without limit.
		_ = user.NewPostgresRepository(t.db).RecordFailedLogin(database.WithSuperadmin(ctx), userID, maxFailedLogins, lockoutDuration)
	}
	return err
}

// passwordHash returns the user's stored hash, or ErrAccountLocked while the
// account is locked.
func (t *UserTokens) passwordHash(ctx context.Context, userID uuid.UUID) (string, error) {
	conn, err := t.db.GetConn(database.WithSuperadmin(ctx))
	if err != nil {
		return "", err
	}
	defer conn.Release()
	var hash string
	var lockedUntil *time.Time
	if err := conn.QueryRow(ctx, `SELECT password_hash, locked_until FROM hatamoto_users WHERE id = $1`, userID).Scan(&hash, &lockedUntil); err != nil {
		return "", ErrInvalidCredentials
	}
	if lockedUntil != nil && lockedUntil.After(t.now()) {
		return "", ErrAccountLocked
	}
	return hash, nil
}

// setPassword stores hash if the password is still old (no concurrent change
// slipped in since it was verified) and signs out the other sessions.
func (t *UserTokens) setPassword(ctx context.Context, userID uuid.UUID, old, hash string, keepSession *uuid.UUID) error {
	return t.tx(ctx, func(tx pgx.Tx) error {
		var cur string
		if err := tx.QueryRow(ctx, `SELECT password_hash FROM hatamoto_users WHERE id = $1 FOR UPDATE`, userID).Scan(&cur); err != nil || cur != old {
			return ErrInvalidCredentials
		}
		if _, err := tx.Exec(ctx, `UPDATE hatamoto_users SET password_hash = $2, failed_login_attempts = 0, locked_until = NULL, updated_at = NOW() WHERE id = $1`, userID, hash); err != nil {
			return err
		}
		var err error
		if keepSession != nil {
			_, err = tx.Exec(ctx, `DELETE FROM hatamoto_sessions WHERE user_id = $1 AND kind IN ('admin','api') AND id <> $2`, userID, *keepSession)
		} else {
			_, err = tx.Exec(ctx, `DELETE FROM hatamoto_sessions WHERE user_id = $1 AND kind IN ('admin','api')`, userID)
		}
		return err
	})
}
