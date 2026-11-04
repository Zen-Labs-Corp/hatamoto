package twofactor

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when a row does not exist (or a ceremony is spent or expired).
var ErrNotFound = errors.New("twofactor: not found")

// TOTPRecord is the stored authenticator-app state of one user.
type TOTPRecord struct {
	SecretSealed []byte
	Confirmed    bool
	LastStep     int64
}

// Passkey is one registered WebAuthn credential.
type Passkey struct {
	ID           uuid.UUID
	UserID       uuid.UUID
	CredentialID []byte
	Credential   json.RawMessage // go-webauthn Credential as JSON
	Name         string
	CreatedAt    time.Time
	LastUsedAt   *time.Time
}

// Ceremony is a single-use WebAuthn challenge.
type Ceremony struct {
	ID      uuid.UUID
	UserID  *uuid.UUID
	Purpose string
	Data    json.RawMessage
	Expires time.Time
}

// Factors summarises what a user has set up.
type Factors struct {
	TOTP         bool
	Passkeys     int
	RecoveryLeft int
}

// Removed counts what Reset deleted.
type Removed struct {
	TOTP          bool
	Passkeys      int
	RecoveryCodes int
}

// Repository is the storage the Service and Passkeys need.
type Repository interface {
	GetTOTP(ctx context.Context, uid uuid.UUID) (*TOTPRecord, error)
	// SaveTOTP stores a new unconfirmed secret; it replaces an unconfirmed one
	// and never touches a confirmed one (returns false then).
	SaveTOTP(ctx context.Context, uid uuid.UUID, sealed []byte) (bool, error)
	// ConfirmTOTP marks an unconfirmed secret confirmed and records the step
	// that proved it. False when nothing was pending.
	ConfirmTOTP(ctx context.Context, uid uuid.UUID, step int64) (bool, error)
	// AdvanceTOTPStep atomically moves last_step forward on a confirmed secret.
	// False when step is not greater than the stored one (a replay).
	AdvanceTOTPStep(ctx context.Context, uid uuid.UUID, step int64) (bool, error)
	DeleteTOTP(ctx context.Context, uid uuid.UUID) (bool, error)

	ReplaceRecoveryCodes(ctx context.Context, uid uuid.UUID, hashes [][]byte) error
	// SpendRecoveryCode marks one unused code spent; false when none matched.
	SpendRecoveryCode(ctx context.Context, uid uuid.UUID, hash []byte) (bool, error)
	RecoveryLeft(ctx context.Context, uid uuid.UUID) (int, error)

	ListPasskeys(ctx context.Context, uid uuid.UUID) ([]Passkey, error)
	AddPasskey(ctx context.Context, p Passkey) (uuid.UUID, error)
	PasskeyByCredentialID(ctx context.Context, credID []byte) (*Passkey, error)
	// UpdatePasskey stores the refreshed credential (sign counter, flags) and last use.
	UpdatePasskey(ctx context.Context, id uuid.UUID, credential json.RawMessage, usedAt time.Time) error
	DeletePasskey(ctx context.Context, uid, id uuid.UUID) (bool, error)

	PutCeremony(ctx context.Context, c Ceremony) error
	// TakeCeremony deletes and returns an unexpired ceremony of that purpose:
	// the second call for the same id finds nothing.
	TakeCeremony(ctx context.Context, id uuid.UUID, purpose string, now time.Time) (*Ceremony, error)

	// Reset removes every factor of a user in one transaction.
	Reset(ctx context.Context, uid uuid.UUID) (Removed, error)
	Factors(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]Factors, error)
}

// Store is the Postgres Repository.
type Store struct{ pool *pgxpool.Pool }

// NewStore wraps a pool.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

var _ Repository = (*Store)(nil)

func (s *Store) GetTOTP(ctx context.Context, uid uuid.UUID) (*TOTPRecord, error) {
	var r TOTPRecord
	var confirmed *time.Time
	err := s.pool.QueryRow(ctx, `SELECT secret_sealed, confirmed_at, last_step FROM hatamoto_user_totp WHERE user_id = $1`, uid).
		Scan(&r.SecretSealed, &confirmed, &r.LastStep)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	r.Confirmed = confirmed != nil
	return &r, nil
}

func (s *Store) SaveTOTP(ctx context.Context, uid uuid.UUID, sealed []byte) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		INSERT INTO hatamoto_user_totp (user_id, secret_sealed) VALUES ($1, $2)
		ON CONFLICT (user_id) DO UPDATE SET secret_sealed = EXCLUDED.secret_sealed, last_step = 0, created_at = now()
		WHERE hatamoto_user_totp.confirmed_at IS NULL`, uid, sealed)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func (s *Store) ConfirmTOTP(ctx context.Context, uid uuid.UUID, step int64) (bool, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE hatamoto_user_totp SET confirmed_at = now(), last_step = $2 WHERE user_id = $1 AND confirmed_at IS NULL`, uid, step)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func (s *Store) AdvanceTOTPStep(ctx context.Context, uid uuid.UUID, step int64) (bool, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE hatamoto_user_totp SET last_step = $2 WHERE user_id = $1 AND confirmed_at IS NOT NULL AND last_step < $2`, uid, step)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func (s *Store) DeleteTOTP(ctx context.Context, uid uuid.UUID) (bool, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM hatamoto_user_totp WHERE user_id = $1`, uid)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func (s *Store) ReplaceRecoveryCodes(ctx context.Context, uid uuid.UUID, hashes [][]byte) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `DELETE FROM hatamoto_user_recovery_codes WHERE user_id = $1`, uid); err != nil {
		return err
	}
	for _, h := range hashes {
		if _, err := tx.Exec(ctx, `INSERT INTO hatamoto_user_recovery_codes (user_id, code_hash) VALUES ($1, $2)`, uid, h); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) SpendRecoveryCode(ctx context.Context, uid uuid.UUID, hash []byte) (bool, error) {
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `
		UPDATE hatamoto_user_recovery_codes SET used_at = now()
		WHERE id = (SELECT id FROM hatamoto_user_recovery_codes WHERE user_id = $1 AND code_hash = $2 AND used_at IS NULL LIMIT 1 FOR UPDATE)
		  AND used_at IS NULL
		RETURNING id`, uid, hash).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (s *Store) RecoveryLeft(ctx context.Context, uid uuid.UUID) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM hatamoto_user_recovery_codes WHERE user_id = $1 AND used_at IS NULL`, uid).Scan(&n)
	return n, err
}

func (s *Store) ListPasskeys(ctx context.Context, uid uuid.UUID) ([]Passkey, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, user_id, credential_id, credential, name, created_at, last_used_at FROM hatamoto_user_passkeys WHERE user_id = $1 ORDER BY created_at, id`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Passkey
	for rows.Next() {
		var p Passkey
		if err := rows.Scan(&p.ID, &p.UserID, &p.CredentialID, &p.Credential, &p.Name, &p.CreatedAt, &p.LastUsedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) AddPasskey(ctx context.Context, p Passkey) (uuid.UUID, error) {
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `INSERT INTO hatamoto_user_passkeys (user_id, credential_id, credential, name) VALUES ($1, $2, $3, $4) RETURNING id`,
		p.UserID, p.CredentialID, []byte(p.Credential), p.Name).Scan(&id)
	return id, err
}

func (s *Store) PasskeyByCredentialID(ctx context.Context, credID []byte) (*Passkey, error) {
	var p Passkey
	err := s.pool.QueryRow(ctx, `SELECT id, user_id, credential_id, credential, name, created_at, last_used_at FROM hatamoto_user_passkeys WHERE credential_id = $1`, credID).
		Scan(&p.ID, &p.UserID, &p.CredentialID, &p.Credential, &p.Name, &p.CreatedAt, &p.LastUsedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (s *Store) UpdatePasskey(ctx context.Context, id uuid.UUID, credential json.RawMessage, usedAt time.Time) error {
	_, err := s.pool.Exec(ctx, `UPDATE hatamoto_user_passkeys SET credential = $2, last_used_at = $3 WHERE id = $1`, id, []byte(credential), usedAt)
	return err
}

func (s *Store) DeletePasskey(ctx context.Context, uid, id uuid.UUID) (bool, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM hatamoto_user_passkeys WHERE id = $1 AND user_id = $2`, id, uid)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func (s *Store) PutCeremony(ctx context.Context, c Ceremony) error {
	// Opportunistic purge keeps the table small without a janitor.
	if _, err := s.pool.Exec(ctx, `DELETE FROM hatamoto_webauthn_ceremonies WHERE expires_at < now() - interval '1 hour'`); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO hatamoto_webauthn_ceremonies (id, user_id, purpose, data, expires_at) VALUES ($1, $2, $3, $4, $5)`,
		c.ID, c.UserID, c.Purpose, []byte(c.Data), c.Expires)
	return err
}

func (s *Store) TakeCeremony(ctx context.Context, id uuid.UUID, purpose string, now time.Time) (*Ceremony, error) {
	c := Ceremony{ID: id}
	err := s.pool.QueryRow(ctx, `DELETE FROM hatamoto_webauthn_ceremonies WHERE id = $1 AND purpose = $2 AND expires_at > $3 RETURNING user_id, purpose, data, expires_at`,
		id, purpose, now).Scan(&c.UserID, &c.Purpose, &c.Data, &c.Expires)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *Store) Reset(ctx context.Context, uid uuid.UUID) (Removed, error) {
	var r Removed
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return r, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `DELETE FROM hatamoto_user_totp WHERE user_id = $1`, uid)
	if err != nil {
		return r, err
	}
	r.TOTP = tag.RowsAffected() > 0
	if tag, err = tx.Exec(ctx, `DELETE FROM hatamoto_user_passkeys WHERE user_id = $1`, uid); err != nil {
		return r, err
	}
	r.Passkeys = int(tag.RowsAffected())
	if tag, err = tx.Exec(ctx, `DELETE FROM hatamoto_user_recovery_codes WHERE user_id = $1`, uid); err != nil {
		return r, err
	}
	r.RecoveryCodes = int(tag.RowsAffected())
	if _, err = tx.Exec(ctx, `DELETE FROM hatamoto_webauthn_ceremonies WHERE user_id = $1`, uid); err != nil {
		return r, err
	}
	return r, tx.Commit(ctx)
}

func (s *Store) Factors(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]Factors, error) {
	out := make(map[uuid.UUID]Factors, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT u.id,
		       EXISTS (SELECT 1 FROM hatamoto_user_totp t WHERE t.user_id = u.id AND t.confirmed_at IS NOT NULL),
		       (SELECT count(*) FROM hatamoto_user_passkeys p WHERE p.user_id = u.id),
		       (SELECT count(*) FROM hatamoto_user_recovery_codes c WHERE c.user_id = u.id AND c.used_at IS NULL)
		FROM unnest($1::uuid[]) AS u(id)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var f Factors
		if err := rows.Scan(&id, &f.TOTP, &f.Passkeys, &f.RecoveryLeft); err != nil {
			return nil, err
		}
		out[id] = f
	}
	return out, rows.Err()
}
