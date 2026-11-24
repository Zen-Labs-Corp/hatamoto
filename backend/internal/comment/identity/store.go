package identity

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"hatamoto.dev/core/internal/comment"
	"hatamoto.dev/core/internal/database"
)

// Limits caps the confirmation mails sent in 24 hours.
type Limits struct{ PerCommenter, PerIP, PerNet int }

// DefaultLimits are the per-address, per-ip_hash and per-net_hash caps.
var DefaultLimits = Limits{PerCommenter: 3, PerIP: 10, PerNet: 30}

// limitWindow is the period the send counters look back over.
const limitWindow = 24 * time.Hour

type pgStore struct{ db *database.DB }

type issueRow struct {
	Hash        []byte
	CommentID   *uuid.UUID // nil for a sign-in without a comment
	CommenterID uuid.UUID
	// IPHash and NetHash are the stored comment's hashes; "" when the IP was
	// unparsable, which is never counted.
	IPHash, NetHash string
	Now             time.Time
}

// Issue records one confirmation unless a limit is reached. The counts and the
// insert run under advisory transaction locks, so parallel requests cannot
// overshoot: one per commenter, one per ip_hash and one per net_hash. Every
// caller takes them in that fixed order (commenter, ip, net; empty hashes are
// skipped), so two requests can never wait on each other in a cycle. The
// INSERT then takes foreign-key share locks on the comment row and the
// commenter row; no other path holds those while waiting for these advisory
// locks. A mail that later fails to send still counts: the row is written first.
func (s *pgStore) Issue(ctx context.Context, r issueRow, l Limits) (limited bool, err error) {
	err = s.db.WithTx(ctx, func(tx pgx.Tx) error {
		for _, key := range []string{"hm-comment-confirm:" + r.CommenterID.String(), "hm-comment-confirm-ip:" + r.IPHash, "hm-comment-confirm-net:" + r.NetHash} {
			if strings.HasSuffix(key, ":") {
				continue // no hash to count under
			}
			if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, key); err != nil {
				return err
			}
		}
		since := r.Now.Add(-limitWindow)
		var byCommenter, byIP, byNet int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM hatamoto_comment_confirmations WHERE commenter_id = $1 AND created_at > $2`, r.CommenterID, since).Scan(&byCommenter); err != nil {
			return err
		}
		if r.IPHash != "" {
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM hatamoto_comment_confirmations WHERE ip_hash = $1 AND created_at > $2`, r.IPHash, since).Scan(&byIP); err != nil {
				return err
			}
		}
		if r.NetHash != "" {
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM hatamoto_comment_confirmations WHERE net_hash = $1 AND created_at > $2`, r.NetHash, since).Scan(&byNet); err != nil {
				return err
			}
		}
		if byCommenter >= l.PerCommenter || byIP >= l.PerIP || byNet >= l.PerNet {
			limited = true
			return nil
		}
		_, err := tx.Exec(ctx, `INSERT INTO hatamoto_comment_confirmations (token_hash, comment_id, commenter_id, ip_hash, net_hash, created_at, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			r.Hash, r.CommentID, r.CommenterID, r.IPHash, r.NetHash, r.Now, r.Now.Add(TokenTTL))
		return err
	})
	return limited, err
}

// Peek looks a token up without consuming it. The comment must still be a draft.
func (s *pgStore) Peek(ctx context.Context, hash []byte, now time.Time) (commentID, entityID, commenterID uuid.UUID, ok bool, err error) {
	err = s.db.Pool().QueryRow(ctx, `SELECT k.comment_id, m.entity_id, k.commenter_id
		FROM hatamoto_comment_confirmations k
		JOIN hatamoto_comments m ON m.id = k.comment_id
		WHERE k.token_hash = $1 AND k.used_at IS NULL AND k.expires_at > $2 AND m.status = 'draft'`,
		hash, now).Scan(&commentID, &entityID, &commenterID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, uuid.Nil, uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, uuid.Nil, uuid.Nil, false, err
	}
	return commentID, entityID, commenterID, true, nil
}

// PeekSignIn looks a sign-in token (one without a comment) up without
// consuming it and returns its commenter.
func (s *pgStore) PeekSignIn(ctx context.Context, hash []byte, now time.Time) (commenterID uuid.UUID, ok bool, err error) {
	err = s.db.Pool().QueryRow(ctx, `SELECT commenter_id FROM hatamoto_comment_confirmations
		WHERE token_hash = $1 AND used_at IS NULL AND expires_at > $2 AND comment_id IS NULL`,
		hash, now).Scan(&commenterID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, err
	}
	return commenterID, true, nil
}

// Consume spends a token in one statement that locks only its own row. A
// comment token (signIn false) must carry a comment, a sign-in token must not;
// commentID is uuid.Nil for the latter.
func (s *pgStore) Consume(ctx context.Context, hash []byte, now time.Time, signIn bool) (commentID, commenterID uuid.UUID, ok bool, err error) {
	var cid *uuid.UUID
	err = s.db.Pool().QueryRow(ctx, `UPDATE hatamoto_comment_confirmations SET used_at = $2
		WHERE token_hash = $1 AND used_at IS NULL AND expires_at > $2 AND (comment_id IS NULL) = $3
		RETURNING comment_id, commenter_id`, hash, now, signIn).Scan(&cid, &commenterID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, uuid.Nil, false, err
	}
	if cid != nil {
		commentID = *cid
	}
	return commentID, commenterID, true, nil
}

// Release clears used_at of a token, after its confirmation failed.
func (s *pgStore) Release(ctx context.Context, hash []byte) error {
	_, err := s.db.Pool().Exec(ctx, `UPDATE hatamoto_comment_confirmations SET used_at = NULL WHERE token_hash = $1`, hash)
	return err
}

// TokenVersion reads the commenter's current token_version.
func (s *pgStore) TokenVersion(ctx context.Context, commenterID uuid.UUID) (int, bool, error) {
	var v int
	err := s.db.Pool().QueryRow(ctx, `SELECT token_version FROM hatamoto_commenters WHERE id = $1`, commenterID).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return v, true, nil
}

// Purge deletes what the 24 hour draft window leaves behind, in lock order:
// comment rows, then confirmations, then commenter rows, each its own
// statement. Drafts hold no counts, so nothing is recounted and no observer
// fires. It selects status = 'draft' (a moderator-approved draft keeps
// confirmed_at NULL) and only leaf drafts, so the parent_id cascade cannot
// take a published reply with it.
func (s *pgStore) Purge(ctx context.Context, now time.Time) (drafts, tokens, commenters int64, err error) {
	cutoff := now.Add(-comment.DraftTTL)
	pool := s.db.Pool()
	tag, err := pool.Exec(ctx, `DELETE FROM hatamoto_comments c WHERE c.status = 'draft' AND c.created_at < $1
		AND NOT EXISTS (SELECT 1 FROM hatamoto_comments r WHERE r.parent_id = c.id)`, cutoff)
	if err != nil {
		return 0, 0, 0, err
	}
	drafts = tag.RowsAffected()
	tag, err = pool.Exec(ctx, `DELETE FROM hatamoto_comment_confirmations WHERE created_at < $1`, cutoff)
	if err != nil {
		return drafts, 0, 0, err
	}
	tokens = tag.RowsAffected()
	tag, err = pool.Exec(ctx, `DELETE FROM hatamoto_commenters m
		WHERE m.trust_level = 'new' AND m.approved_count = 0 AND m.first_approved_at IS NULL AND m.last_spam_at IS NULL
		AND m.last_seen_at < $1
		AND NOT EXISTS (SELECT 1 FROM hatamoto_comments c WHERE c.commenter_id = m.id)
		AND NOT EXISTS (SELECT 1 FROM hatamoto_comment_confirmations k WHERE k.commenter_id = m.id)`, cutoff)
	if err != nil {
		return drafts, tokens, 0, err
	}
	return drafts, tokens, tag.RowsAffected(), nil
}
