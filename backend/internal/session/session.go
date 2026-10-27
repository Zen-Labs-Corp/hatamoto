// Package session is the server-side session store for admin logins.
// Sessions are stored in hatamoto_sessions and looked up by SHA-256 hash of
// the opaque cookie value — the raw value never lives in the DB. The row is
// the source of truth for expiry, validity and revocation.
package session

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/database"
)

// Kind is the session kind. Mirrors the chk_sessions_kind DB constraint.
type Kind string

const (
	KindAdmin Kind = "admin"
	// KindAPI is a session issued by the JSON API login. It is never valid
	// for the admin UI.
	KindAPI Kind = "api"
)

// IdleTimeout is how long a session may go unused before Validate refuses
// (and deletes) it. The absolute lifetime is the row's expires_at.
const IdleTimeout = 7 * 24 * time.Hour

// Errors returned by the store. Deliberately opaque — handlers must not
// leak which check failed to clients (timing attacks, enumeration).
var (
	ErrInvalid = errors.New("invalid session")
	ErrExpired = errors.New("session expired")
)

// Session is a row from hatamoto_sessions.
type Session struct {
	ID         uuid.UUID
	Kind       Kind
	UserID     *uuid.UUID
	ExpiresAt  time.Time
	CreatedAt  time.Time
	LastUsedAt *time.Time
	UserAgent  string
	IP         string
}

// IsExpired returns true if the session has aged out.
func (s *Session) IsExpired() bool {
	return time.Now().After(s.ExpiresAt)
}

// Store reads and writes sessions in hatamoto_sessions. It runs under
// superadmin context for writes (session rows have no tenant_id;
// lookups by token hash are global by design).
type Store struct {
	db *database.DB
}

// NewStore constructs a session store backed by the given database.
func NewStore(db *database.DB) *Store {
	return &Store{db: db}
}

// IssueAdmin creates a new admin session and returns the opaque cookie
// value (the caller must set this on the response cookie). The cookie
// value is 32 bytes of cryptographic randomness, base64-encoded.
func (s *Store) IssueAdmin(ctx context.Context, userID uuid.UUID, ttl time.Duration, userAgent, ip string) (token string, sess *Session, err error) {
	return s.issue(ctx, KindAdmin, &userID, ttl, userAgent, ip)
}

// IssueAPI creates a new JSON API session and returns the opaque token. It is
// the same shape as an admin session but a different kind, so an API cookie
// can never act as the admin form session.
func (s *Store) IssueAPI(ctx context.Context, userID uuid.UUID, ttl time.Duration, userAgent, ip string) (token string, sess *Session, err error) {
	return s.issue(ctx, KindAPI, &userID, ttl, userAgent, ip)
}

// Validate looks up a session by the opaque cookie value. Returns
// ErrInvalid for missing/unknown tokens, ErrExpired for aged-out or idle
// (IdleTimeout) sessions; an idle session is revoked.
// On success, updates last_used_at as a side effect (fire-and-forget; a
// failed update is logged but doesn't fail the request).
func (s *Store) Validate(ctx context.Context, token string) (*Session, error) {
	if token == "" {
		return nil, ErrInvalid
	}
	hash := hashToken(token)

	conn, err := s.db.GetConn(database.WithSuperadmin(ctx))
	if err != nil {
		return nil, fmt.Errorf("acquire db conn: %w", err)
	}
	defer conn.Release()

	sess := &Session{}
	var userID *uuid.UUID
	var lastUsed *time.Time
	var userAgent, ip *string

	row := conn.QueryRow(ctx, `
		SELECT id, kind, user_id, expires_at, created_at,
		       last_used_at, user_agent, host(ip)
		FROM hatamoto_sessions
		WHERE session_token_hash = $1
		LIMIT 1
	`, hash)

	var kind string
	if err := row.Scan(
		&sess.ID, &kind, &userID,
		&sess.ExpiresAt, &sess.CreatedAt, &lastUsed, &userAgent, &ip,
	); err != nil {
		return nil, ErrInvalid
	}

	sess.Kind = Kind(kind)
	sess.UserID = userID
	sess.LastUsedAt = lastUsed
	if userAgent != nil {
		sess.UserAgent = *userAgent
	}
	if ip != nil {
		sess.IP = *ip
	}

	if sess.IsExpired() {
		return nil, ErrExpired
	}

	// An idle session is dead for good: delete it before anything can bump
	// last_used_at, or the next request would find it freshly used.
	if lastUsed != nil && time.Since(*lastUsed) > IdleTimeout {
		_, _ = conn.Exec(ctx, `DELETE FROM hatamoto_sessions WHERE id = $1`, sess.ID)
		return nil, ErrExpired
	}

	// Sliding-touch: bump last_used_at without blocking the request.
	go func() {
		bgCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.touch(bgCtx, sess.ID, lastUsed)
	}()

	return sess, nil
}

// Rotate atomically consumes an admin session token and issues a fresh one.
// The old row is DELETEd first (the DELETE ... RETURNING is the concurrency
// gate): exactly one caller can delete it, so two concurrent refreshes with the
// same token can't both succeed, and a replay of an already-rotated token
// deletes zero rows and returns ErrInvalid. Only after a successful delete is a
// new session issued. A stolen cookie is therefore valid only until the next
// legitimate refresh. (Non-admin/expired/unknown tokens also return ErrInvalid.)
//
// NOTE: hard-delete means a replayed token is indistinguishable from an unknown
// one, so there is no "kill the whole family" escalation on replay \u2014 that would
// need a soft-revoke column (documented follow-up).
func (s *Store) Rotate(ctx context.Context, oldToken string, ttl time.Duration) (string, *Session, error) {
	if oldToken == "" {
		return "", nil, ErrInvalid
	}
	hash := hashToken(oldToken)

	conn, err := s.db.GetConn(database.WithSuperadmin(ctx))
	if err != nil {
		return "", nil, fmt.Errorf("acquire db conn: %w", err)
	}
	defer conn.Release()

	var userID *uuid.UUID
	var userAgent, ip *string
	row := conn.QueryRow(ctx, `
		DELETE FROM hatamoto_sessions
		WHERE session_token_hash = $1 AND kind = 'admin' AND expires_at > NOW()
		RETURNING user_id, user_agent, host(ip)`, hash)
	if err := row.Scan(&userID, &userAgent, &ip); err != nil {
		return "", nil, ErrInvalid
	}
	if userID == nil {
		return "", nil, ErrInvalid
	}

	ua, ipv := "", ""
	if userAgent != nil {
		ua = *userAgent
	}
	if ip != nil {
		ipv = *ip
	}
	return s.IssueAdmin(ctx, *userID, ttl, ua, ipv)
}

// Revoke deletes a session by ID. Used for logout.
func (s *Store) Revoke(ctx context.Context, sessionID uuid.UUID) error {
	conn, err := s.db.GetConn(database.WithSuperadmin(ctx))
	if err != nil {
		return fmt.Errorf("acquire db conn: %w", err)
	}
	defer conn.Release()
	_, err = conn.Exec(ctx, `DELETE FROM hatamoto_sessions WHERE id = $1`, sessionID)
	return err
}

// RevokeAllForUser deletes every admin and API session for a given user. Used for
// "log out everywhere" and on password change.
func (s *Store) RevokeAllForUser(ctx context.Context, userID uuid.UUID) error {
	conn, err := s.db.GetConn(database.WithSuperadmin(ctx))
	if err != nil {
		return fmt.Errorf("acquire db conn: %w", err)
	}
	defer conn.Release()
	_, err = conn.Exec(ctx, `DELETE FROM hatamoto_sessions WHERE user_id = $1 AND kind IN ('admin','api')`, userID)
	return err
}

// PurgeExpired removes all sessions whose expires_at is in the past.
// Run periodically (e.g. via the scheduler) to keep the table bounded.
func (s *Store) PurgeExpired(ctx context.Context) (int64, error) {
	conn, err := s.db.GetConn(database.WithSuperadmin(ctx))
	if err != nil {
		return 0, fmt.Errorf("acquire db conn: %w", err)
	}
	defer conn.Release()
	tag, err := conn.Exec(ctx, `DELETE FROM hatamoto_sessions WHERE expires_at <= NOW()`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// ── internals ─────────────────────────────────────────────────────────

// issue is the shared INSERT path used by IssueAdmin.
func (s *Store) issue(ctx context.Context, kind Kind, userID *uuid.UUID, ttl time.Duration, userAgent, ip string) (string, *Session, error) {
	token, err := newOpaqueToken()
	if err != nil {
		return "", nil, fmt.Errorf("generate token: %w", err)
	}
	hash := hashToken(token)
	expiresAt := time.Now().Add(ttl)

	conn, err := s.db.GetConn(database.WithSuperadmin(ctx))
	if err != nil {
		return "", nil, fmt.Errorf("acquire db conn: %w", err)
	}
	defer conn.Release()

	// host() formats inet for storage; pass NULL if ip is empty.
	var ipArg any
	if ip == "" {
		ipArg = nil
	} else {
		ipArg = ip
	}

	sess := &Session{
		Kind:      kind,
		UserID:    userID,
		ExpiresAt: expiresAt,
		UserAgent: userAgent,
		IP:        ip,
	}

	err = conn.QueryRow(ctx, `
		INSERT INTO hatamoto_sessions (kind, user_id, session_token_hash, expires_at, user_agent, ip)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, created_at
	`, string(kind), userID, hash, expiresAt, nullableString(userAgent), ipArg).Scan(&sess.ID, &sess.CreatedAt)
	if err != nil {
		return "", nil, fmt.Errorf("insert session: %w", err)
	}

	return token, sess, nil
}

// touch updates last_used_at to keep "active session" lists current.
// Best-effort; called in a goroutine from Validate. The update only applies
// while last_used_at still holds the value Validate read, so a late touch can
// never revive a session that changed (or went idle) in the meantime.
func (s *Store) touch(ctx context.Context, sessionID uuid.UUID, seen *time.Time) error {
	conn, err := s.db.GetConn(database.WithSuperadmin(ctx))
	if err != nil {
		return err
	}
	defer conn.Release()
	_, err = conn.Exec(ctx, `UPDATE hatamoto_sessions SET last_used_at = NOW() WHERE id = $1 AND last_used_at IS NOT DISTINCT FROM $2`, sessionID, seen)
	return err
}

// newOpaqueToken returns 32 bytes of crypto/rand encoded as URL-safe base64.
// 32 bytes = 256 bits of entropy. Encoded length is 43 chars.
func newOpaqueToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// hashToken returns the SHA-256 hash of the token. The DB stores only
// the hash; the raw token lives in the user's cookie. Constant-time
// comparison happens implicitly via the UNIQUE index lookup.
func hashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// nullableString converts an empty string to nil for SQL NULL semantics.
func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
