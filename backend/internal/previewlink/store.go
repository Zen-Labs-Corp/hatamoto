// Package previewlink stores shareable draft preview links. A link is a
// 32-byte random bearer token; only its SHA-256 is persisted, so a database
// read cannot reveal a usable link, and the token is shown once on creation.
package previewlink

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"hatamoto.dev/core/internal/database"
)

const (
	// Min and Max bound a link's lifetime.
	Min = time.Hour
	Max = 30 * 24 * time.Hour
	// MaxActive is the cap of live links per entity.
	MaxActive = 10
	// tailLen is how many token characters are kept to tell links apart.
	tailLen = 4
)

var (
	// ErrTTL is returned for a lifetime outside Min..Max.
	ErrTTL = errors.New("previewlink: lifetime must be between 1 hour and 30 days")
	// ErrTooMany is returned when an entity already has MaxActive live links.
	ErrTooMany = errors.New("previewlink: too many active links")
)

// Link is one stored link. The token is not part of it.
type Link struct {
	ID            uuid.UUID
	EntityID      uuid.UUID
	Tail          string
	CreatedByName string
	CreatedAt     time.Time
	ExpiresAt     time.Time
}

// Store persists links in hatamoto_preview_links.
type Store struct {
	db  *database.DB
	now func() time.Time
}

// New builds the store.
func New(db *database.DB) *Store { return &Store{db: db, now: time.Now} }

func hash(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

// Create issues a link for the entity and returns the token, which is not
// recoverable afterwards.
func (s *Store) Create(ctx context.Context, entityID uuid.UUID, createdBy *uuid.UUID, name string, ttl time.Duration) (string, *Link, error) {
	if ttl < Min || ttl > Max {
		return "", nil, ErrTTL
	}
	n, err := s.CountActive(ctx, entityID)
	if err != nil {
		return "", nil, err
	}
	if n >= MaxActive {
		return "", nil, ErrTooMany
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, fmt.Errorf("previewlink: random token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	l := &Link{
		ID: uuid.New(), EntityID: entityID, Tail: token[len(token)-tailLen:],
		CreatedByName: name, CreatedAt: s.now().UTC(), ExpiresAt: s.now().UTC().Add(ttl),
	}
	_, err = s.db.Pool().Exec(ctx, `
		INSERT INTO hatamoto_preview_links
		       (id, entity_id, token_hash, created_by, created_by_name, token_tail, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		l.ID, entityID, hash(token), createdBy, name, l.Tail, l.CreatedAt, l.ExpiresAt)
	if err != nil {
		return "", nil, fmt.Errorf("previewlink: create: %w", err)
	}
	return token, l, nil
}

// Resolve maps a token to its entity; ok is false for an unknown, expired or
// revoked token.
func (s *Store) Resolve(ctx context.Context, token string) (uuid.UUID, bool) {
	var id uuid.UUID
	err := s.db.Pool().QueryRow(ctx, `
		SELECT entity_id FROM hatamoto_preview_links
		 WHERE token_hash = $1 AND revoked_at IS NULL AND expires_at > $2`,
		hash(token), s.now().UTC()).Scan(&id)
	if err != nil {
		return uuid.Nil, false
	}
	return id, true
}

// List returns the entity's live links, newest first.
func (s *Store) List(ctx context.Context, entityID uuid.UUID) ([]Link, error) {
	rows, err := s.db.Pool().Query(ctx, `
		SELECT id, entity_id, token_tail, created_by_name, created_at, expires_at
		  FROM hatamoto_preview_links
		 WHERE entity_id = $1 AND revoked_at IS NULL AND expires_at > $2
		 ORDER BY created_at DESC`, entityID, s.now().UTC())
	if err != nil {
		return nil, fmt.Errorf("previewlink: list: %w", err)
	}
	defer rows.Close()
	var out []Link
	for rows.Next() {
		var l Link
		if err := rows.Scan(&l.ID, &l.EntityID, &l.Tail, &l.CreatedByName, &l.CreatedAt, &l.ExpiresAt); err != nil {
			return nil, fmt.Errorf("previewlink: scan: %w", err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// Revoke ends a link at once. It reports whether a live link was revoked.
func (s *Store) Revoke(ctx context.Context, entityID, linkID uuid.UUID) (bool, error) {
	tag, err := s.db.Pool().Exec(ctx, `
		UPDATE hatamoto_preview_links SET revoked_at = $3
		 WHERE id = $1 AND entity_id = $2 AND revoked_at IS NULL`,
		linkID, entityID, s.now().UTC())
	if err != nil {
		return false, fmt.Errorf("previewlink: revoke: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// CountActive counts the entity's live links.
func (s *Store) CountActive(ctx context.Context, entityID uuid.UUID) (int, error) {
	var n int
	err := s.db.Pool().QueryRow(ctx, `
		SELECT count(*) FROM hatamoto_preview_links
		 WHERE entity_id = $1 AND revoked_at IS NULL AND expires_at > $2`,
		entityID, s.now().UTC()).Scan(&n)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return 0, fmt.Errorf("previewlink: count: %w", err)
	}
	return n, nil
}
