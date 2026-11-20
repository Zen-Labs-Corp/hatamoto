package comment

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ModerationBatch is what one moderator is mailed about in one run.
type ModerationBatch struct {
	UserID   uuid.UUID
	Comments []Comment // still pending, oldest queued first
}

// QueueModeration notes a held comment for each of users who has not muted
// comment mail. Unknown users are skipped. Idempotent per (user, comment).
func (s *Service) QueueModeration(ctx context.Context, commentID uuid.UUID, users []uuid.UUID) error {
	if len(users) == 0 {
		return nil
	}
	now := s.utcNow()
	return s.db.WithTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO hatamoto_comment_moderator_notify (user_id)
			SELECT u.id FROM hatamoto_users u WHERE u.id = ANY($1::uuid[])
			ON CONFLICT DO NOTHING`, users); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO hatamoto_comment_notify_queue (user_id, comment_id, queued_at)
			SELECT m.user_id, $2, $3 FROM hatamoto_comment_moderator_notify m
			WHERE m.user_id = ANY($1::uuid[]) AND NOT m.muted
			ON CONFLICT DO NOTHING`, users, commentID, now)
		return err
	})
}

// PruneModerationQueue deletes queue rows whose comment is no longer pending.
// Without it the queue only shrinks where a replica sends mail.
func (s *Service) PruneModerationQueue(ctx context.Context) (int64, error) {
	tag, err := s.db.Pool().Exec(ctx, `DELETE FROM hatamoto_comment_notify_queue q
		WHERE NOT EXISTS (SELECT 1 FROM hatamoto_comments c WHERE c.id = q.comment_id AND c.status = 'pending')`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// ClaimModeration takes, in one transaction, the queued comments of every
// moderator whose last mail is at least gap old: rows of comments that are no
// longer pending and of muted moderators are dropped first; the claim is the
// conditional UPDATE of last_sent_at, so two replicas never both send.
func (s *Service) ClaimModeration(ctx context.Context, gap time.Duration) ([]ModerationBatch, error) {
	now := s.utcNow()
	cutoff := now.Add(-gap)
	type queued struct {
		user, comment uuid.UUID
		at            time.Time
	}
	var claimed []queued
	var comments map[uuid.UUID]Comment
	err := s.db.WithTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM hatamoto_comment_notify_queue q USING hatamoto_comments c
			WHERE c.id = q.comment_id AND c.status <> 'pending'`); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM hatamoto_comment_notify_queue q USING hatamoto_comment_moderator_notify m
			WHERE m.user_id = q.user_id AND m.muted`); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `WITH due AS (
				UPDATE hatamoto_comment_moderator_notify m SET last_sent_at = $1
				WHERE (m.last_sent_at IS NULL OR m.last_sent_at <= $2) AND NOT m.muted
				  AND EXISTS (SELECT 1 FROM hatamoto_comment_notify_queue q WHERE q.user_id = m.user_id)
				RETURNING m.user_id)
			DELETE FROM hatamoto_comment_notify_queue q USING due WHERE q.user_id = due.user_id
			RETURNING q.user_id, q.comment_id, q.queued_at`, now, cutoff)
		if err != nil {
			return err
		}
		for rows.Next() {
			var q queued
			if err := rows.Scan(&q.user, &q.comment, &q.at); err != nil {
				rows.Close()
				return err
			}
			claimed = append(claimed, q)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if len(claimed) == 0 {
			return nil
		}
		ids := make([]uuid.UUID, len(claimed))
		for i, q := range claimed {
			ids[i] = q.comment
		}
		crows, err := tx.Query(ctx, `SELECT `+commentColumns+` FROM hatamoto_comments WHERE id = ANY($1)`, ids)
		if err != nil {
			return err
		}
		defer crows.Close()
		comments = make(map[uuid.UUID]Comment, len(ids))
		for crows.Next() {
			c, err := scanComment(crows)
			if err != nil {
				return err
			}
			comments[c.ID] = c
		}
		return crows.Err()
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(claimed, func(i, j int) bool {
		if !claimed[i].at.Equal(claimed[j].at) {
			return claimed[i].at.Before(claimed[j].at)
		}
		ci, cj := comments[claimed[i].comment], comments[claimed[j].comment]
		if !ci.CreatedAt.Equal(cj.CreatedAt) {
			return ci.CreatedAt.Before(cj.CreatedAt)
		}
		return claimed[i].comment.String() < claimed[j].comment.String()
	})
	var out []ModerationBatch
	index := map[uuid.UUID]int{}
	for _, q := range claimed {
		c, ok := comments[q.comment]
		if !ok {
			continue
		}
		i, seen := index[q.user]
		if !seen {
			i = len(out)
			index[q.user] = i
			out = append(out, ModerationBatch{UserID: q.user})
		}
		out[i].Comments = append(out[i].Comments, c)
	}
	return out, nil
}

// ClearModeration drops every queued row (mode off).
func (s *Service) ClearModeration(ctx context.Context) error {
	_, err := s.db.Pool().Exec(ctx, `DELETE FROM hatamoto_comment_notify_queue`)
	return err
}

// SetModeratorMuted turns comment mail off (true) or on for one user and drops
// what is queued for a muted one. Unknown users are a no-op.
func (s *Service) SetModeratorMuted(ctx context.Context, userID uuid.UUID, muted bool) error {
	return s.db.WithTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO hatamoto_comment_moderator_notify (user_id, muted)
			SELECT id, $2 FROM hatamoto_users WHERE id = $1
			ON CONFLICT (user_id) DO UPDATE SET muted = EXCLUDED.muted`, userID, muted); err != nil {
			return err
		}
		if !muted {
			return nil
		}
		_, err := tx.Exec(ctx, `DELETE FROM hatamoto_comment_notify_queue WHERE user_id = $1`, userID)
		return err
	})
}
