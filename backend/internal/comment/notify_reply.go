package comment

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// QueueReply notes an approved reply when its parent asked for reply mail and is
// confirmed. Once per reply (primary key), so a re-approval never mails again.
func (s *Service) QueueReply(ctx context.Context, reply Comment) error {
	if reply.ParentID == nil {
		return nil
	}
	_, err := s.db.Pool().Exec(ctx, `INSERT INTO hatamoto_comment_reply_queue (comment_id, queued_at)
		SELECT $1, $2 FROM hatamoto_comments p
		WHERE p.id = $3 AND p.notify_replies AND p.confirmed_at IS NOT NULL AND p.commenter_id IS NOT NULL
		ON CONFLICT DO NOTHING`, reply.ID, s.utcNow(), *reply.ParentID)
	return err
}

// ReplyBatch is the reply mail due for one commenter: one reply is a single
// mail, several are one summary.
type ReplyBatch struct {
	CommenterID uuid.UUID // the recipient: the parents' commenter
	Email       string    // opened from the sealed copy
	Replies     []Comment // oldest queued first
}

// replyLockPrefix namespaces the per-recipient advisory lock.
const replyLockPrefix = "hatamoto.comment.reply_mail:"

// ClaimReplyBatches claims reply mail for up to limit commenters. A commenter
// gets at most one mail per window: when nothing was sent to them within the
// window, only their oldest queued reply is claimed (mailed at once); replies
// queued while a window was open are claimed together after it ends. Rows that
// are no longer eligible are deleted, so they never count as a send. Several
// replicas may run it: each commenter is claimed under an advisory lock. On an
// error the batches already claimed (and stamped) are returned with it.
func (s *Service) ClaimReplyBatches(ctx context.Context, limit int, window time.Duration) ([]ReplyBatch, error) {
	now := s.utcNow()
	rows, err := s.db.Pool().Query(ctx, `SELECT p.commenter_id FROM hatamoto_comment_reply_queue q
		JOIN hatamoto_comments r ON r.id = q.comment_id
		JOIN hatamoto_comments p ON p.id = r.parent_id
		WHERE q.sent_at IS NULL AND p.commenter_id IS NOT NULL
		  AND NOT EXISTS (SELECT 1 FROM hatamoto_comment_reply_queue q2
			JOIN hatamoto_comments r2 ON r2.id = q2.comment_id
			JOIN hatamoto_comments p2 ON p2.id = r2.parent_id
			WHERE p2.commenter_id = p.commenter_id AND q2.sent_at > $1)
		GROUP BY p.commenter_id ORDER BY min(q.queued_at), p.commenter_id LIMIT $2`,
		now.Add(-window), limit)
	if err != nil {
		return nil, err
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []ReplyBatch
	for _, id := range ids {
		b, ok, err := s.claimReplyRecipient(ctx, id, now, window)
		if err != nil {
			return out, err
		}
		if ok {
			out = append(out, b)
		}
	}
	return out, nil
}

// claimReplyRecipient claims the due reply mail of one commenter in one short
// transaction. ok is false when nothing is due (held, locked elsewhere, or
// nothing eligible).
func (s *Service) claimReplyRecipient(ctx context.Context, id uuid.UUID, now time.Time, window time.Duration) (ReplyBatch, bool, error) {
	var batch ReplyBatch
	var claimed bool
	err := s.db.WithTx(ctx, func(tx pgx.Tx) error {
		var got bool
		if err := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended($1 || $2::text, 0))`,
			replyLockPrefix, id).Scan(&got); err != nil || !got {
			return err
		}
		// A new statement is a new READ COMMITTED snapshot: a replica that
		// committed just before the lock was granted is seen here.
		var last *time.Time
		if err := tx.QueryRow(ctx, `SELECT max(q.sent_at) FROM hatamoto_comment_reply_queue q
			JOIN hatamoto_comments r ON r.id = q.comment_id
			JOIN hatamoto_comments p ON p.id = r.parent_id
			WHERE p.commenter_id = $1 AND q.sent_at IS NOT NULL`, id).Scan(&last); err != nil {
			return err
		}
		if last != nil && last.After(now.Add(-window)) {
			return nil
		}
		rows, err := tx.Query(ctx, `SELECT q.comment_id, q.queued_at,
				COALESCE(r.status = 'approved' AND p.status = 'approved' AND p.notify_replies
					AND p.confirmed_at IS NOT NULL AND cm.trust_level <> 'blocked'
					AND (r.commenter_id IS NULL OR r.commenter_id <> p.commenter_id), false)
			FROM hatamoto_comment_reply_queue q
			JOIN hatamoto_comments r ON r.id = q.comment_id
			JOIN hatamoto_comments p ON p.id = r.parent_id
			JOIN hatamoto_commenters cm ON cm.id = p.commenter_id
			WHERE p.commenter_id = $1 AND q.sent_at IS NULL
			ORDER BY q.queued_at, q.comment_id FOR UPDATE OF q`, id)
		if err != nil {
			return err
		}
		type pending struct {
			id       uuid.UUID
			queuedAt time.Time
		}
		var eligible []pending
		var drop []uuid.UUID
		for rows.Next() {
			var p pending
			var ok bool
			if err := rows.Scan(&p.id, &p.queuedAt, &ok); err != nil {
				rows.Close()
				return err
			}
			if ok {
				eligible = append(eligible, p)
			} else {
				drop = append(drop, p.id)
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		del := func(ids []uuid.UUID) error {
			if len(ids) == 0 {
				return nil
			}
			_, err := tx.Exec(ctx, `DELETE FROM hatamoto_comment_reply_queue WHERE comment_id = ANY($1)`, ids)
			return err
		}
		if err := del(drop); err != nil || len(eligible) == 0 {
			return err
		}
		var hash string
		var sealed []byte
		if err := tx.QueryRow(ctx, `SELECT email_hash, email_sealed FROM hatamoto_commenters WHERE id = $1`, id).
			Scan(&hash, &sealed); err != nil {
			return err
		}
		email, ok := s.keys.OpenEmail(sealed, hash)
		if !ok || email == "" {
			all := make([]uuid.UUID, len(eligible))
			for i, p := range eligible {
				all[i] = p.id
			}
			return del(all)
		}
		// Rows queued while the previous window was open were held back: send
		// them together. Otherwise only the oldest goes, at once.
		take := eligible[:1]
		if last != nil && eligible[0].queuedAt.Before(last.Add(window)) {
			take = eligible
		}
		takeIDs := make([]uuid.UUID, len(take))
		for i, p := range take {
			takeIDs[i] = p.id
		}
		if _, err := tx.Exec(ctx, `UPDATE hatamoto_comment_reply_queue SET sent_at = $2
			WHERE comment_id = ANY($1) AND sent_at IS NULL`, takeIDs, now); err != nil {
			return err
		}
		crows, err := tx.Query(ctx, `SELECT `+commentColumns+` FROM hatamoto_comments WHERE id = ANY($1)`, takeIDs)
		if err != nil {
			return err
		}
		defer crows.Close()
		byID := make(map[uuid.UUID]Comment, len(takeIDs))
		for crows.Next() {
			c, err := scanComment(crows)
			if err != nil {
				return err
			}
			byID[c.ID] = c
		}
		if err := crows.Err(); err != nil {
			return err
		}
		batch = ReplyBatch{CommenterID: id, Email: email}
		for _, rid := range takeIDs {
			batch.Replies = append(batch.Replies, byID[rid])
		}
		claimed = true
		return nil
	})
	if err != nil {
		return ReplyBatch{}, false, err
	}
	return batch, claimed, nil
}

// PruneReplyQueue deletes rows sent more than 30 days ago and unsent rows queued
// more than 7 days ago (mail that was never configured stays unsent).
func (s *Service) PruneReplyQueue(ctx context.Context) (int64, error) {
	now := s.utcNow()
	tag, err := s.db.Pool().Exec(ctx, `DELETE FROM hatamoto_comment_reply_queue
		WHERE (sent_at IS NOT NULL AND sent_at < $1) OR (sent_at IS NULL AND queued_at < $2)`,
		now.AddDate(0, 0, -30), now.AddDate(0, 0, -7))
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// UnsubscribeReplies turns reply mail off on every comment of a commenter. An
// unknown id is a no-op. Ticking the box on a later comment opts in again.
func (s *Service) UnsubscribeReplies(ctx context.Context, commenterID uuid.UUID) (int64, error) {
	tag, err := s.db.Pool().Exec(ctx, `UPDATE hatamoto_comments SET notify_replies = false
		WHERE commenter_id = $1 AND notify_replies`, commenterID)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
