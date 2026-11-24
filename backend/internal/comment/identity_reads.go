package comment

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// PolicyInfo is what the identity flow needs to know about an entity's schema.
type PolicyInfo struct {
	Schema  string // schema slug
	Enabled bool   // settings.comments.policy.enabled
}

// PolicyInfo reads the entity's schema slug and settings.comments.policy.enabled;
// a missing or non-boolean value reads as false. Unknown entity: ErrNotFound.
func (s *Service) PolicyInfo(ctx context.Context, entityID uuid.UUID) (PolicyInfo, error) {
	var p PolicyInfo
	err := s.db.Pool().QueryRow(ctx, `SELECT s.slug,
			CASE WHEN jsonb_typeof(s.settings->'comments'->'policy'->'enabled') = 'boolean'
			     THEN (s.settings->'comments'->'policy'->>'enabled')::boolean ELSE false END
		FROM hatamoto_entities e JOIN hatamoto_entity_schemas s ON s.id = e.schema_id
		WHERE e.id = $1`, entityID).Scan(&p.Schema, &p.Enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return PolicyInfo{}, ErrNotFound
	}
	return p, err
}

// FindCommenter returns the id of the commenter with this address without
// creating one. ok is false when there is none.
func (s *Service) FindCommenter(ctx context.Context, email string) (id uuid.UUID, ok bool, err error) {
	err = s.db.Pool().QueryRow(ctx, `SELECT id FROM hatamoto_commenters WHERE email_hash = $1`, s.keys.HashEmail(email)).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	return id, err == nil, err
}

// EnsureCommenter creates the commenter row for email if it is missing and
// returns its id.
func (s *Service) EnsureCommenter(ctx context.Context, email string) (uuid.UUID, error) {
	h := s.keys.HashEmail(email)
	sealed, err := s.keys.SealEmail(email, h)
	if err != nil {
		return uuid.Nil, err
	}
	now := time.Now().UTC()
	if _, err := s.db.Pool().Exec(ctx, `INSERT INTO hatamoto_commenters (email_hash, email_sealed, created_at, last_seen_at)
		VALUES ($1, $2, $3, $3) ON CONFLICT (email_hash) DO NOTHING`, h, sealed, now); err != nil {
		return uuid.Nil, err
	}
	var id uuid.UUID
	err = s.db.Pool().QueryRow(ctx, `SELECT id FROM hatamoto_commenters WHERE email_hash = $1`, h).Scan(&id)
	return id, err
}

// CommenterEmail returns the commenter and its opened email; the email is ""
// when it cannot be opened. Unknown commenter: ErrNotFound.
func (s *Service) CommenterEmail(ctx context.Context, id uuid.UUID) (Commenter, string, error) {
	var c Commenter
	var trust string
	var sealed []byte
	err := s.db.Pool().QueryRow(ctx, `SELECT id, email_hash, email_sealed, trust_level, approved_count, first_approved_at,
			last_spam_at, token_version, created_at, last_seen_at
		FROM hatamoto_commenters WHERE id = $1`, id).
		Scan(&c.ID, &c.EmailHash, &sealed, &trust, &c.ApprovedCount, &c.FirstApprovedAt, &c.LastSpamAt,
			&c.TokenVersion, &c.CreatedAt, &c.LastSeenAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Commenter{}, "", ErrNotFound
	}
	if err != nil {
		return Commenter{}, "", err
	}
	c.Trust = TrustLevel(trust)
	email, _ := s.keys.OpenEmail(sealed, c.EmailHash)
	return c, email, nil
}

// LastAuthorName returns the author_name of the commenter's newest comment on
// an entity of the same schema as entityID (the name the draft hook gave last
// time); "" when there is none.
func (s *Service) LastAuthorName(ctx context.Context, commenterID, entityID uuid.UUID) (string, error) {
	var name string
	err := s.db.Pool().QueryRow(ctx, `SELECT c.author_name FROM hatamoto_comments c
		JOIN hatamoto_entities e ON e.id = c.entity_id
		WHERE c.commenter_id = $1 AND c.author_user_id IS NULL AND c.author_name <> ''
		  AND c.status <> 'trash'
		  AND e.schema_id = (SELECT schema_id FROM hatamoto_entities WHERE id = $2)
		ORDER BY c.created_at DESC LIMIT 1`, commenterID, entityID).Scan(&name)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return name, err
}
