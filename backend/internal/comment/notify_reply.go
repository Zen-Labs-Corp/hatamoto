package comment

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ReplyNotice is one reply mail to send: to the parent's commenter.
type ReplyNotice struct {
	Reply       Comment
	ParentID    uuid.UUID
	CommenterID uuid.UUID // the parent's commenter: the recipient
	Email       string    // opened from the sealed copy
}

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

// ClaimReplies marks up to limit queued rows sent and returns those still
// eligible: reply approved; parent approved, opted in and confirmed; the parent's
// commenter exists and is not blocked; the reply not written by that same
// commenter. A row whose address cannot be opened is claimed and dropped.
func (s *Service) ClaimReplies(ctx context.Context, limit int) ([]ReplyNotice, error) {
	type recipient struct {
		parent, commenter uuid.UUID
		hash              string
		sealed            []byte
	}
	var out []ReplyNotice
	var rcpt []recipient
	err := s.db.WithTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `WITH c AS (
				SELECT comment_id FROM hatamoto_comment_reply_queue WHERE sent_at IS NULL
				ORDER BY queued_at, comment_id LIMIT $1 FOR UPDATE SKIP LOCKED)
			UPDATE hatamoto_comment_reply_queue q SET sent_at = $2 FROM c
			WHERE q.comment_id = c.comment_id RETURNING q.comment_id`, limit, s.utcNow())
		if err != nil {
			return err
		}
		var ids []uuid.UUID
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil || len(ids) == 0 {
			return err
		}
		rows, err = tx.Query(ctx, `SELECT r.id, p.id, cm.id, cm.email_hash, cm.email_sealed
			FROM hatamoto_comments r
			JOIN hatamoto_comments p ON p.id = r.parent_id
			JOIN hatamoto_commenters cm ON cm.id = p.commenter_id
			WHERE r.id = ANY($1) AND r.status = 'approved' AND p.status = 'approved'
			  AND p.notify_replies AND p.confirmed_at IS NOT NULL AND cm.trust_level <> 'blocked'
			  AND (r.commenter_id IS NULL OR r.commenter_id <> p.commenter_id)
			ORDER BY r.created_at, r.id`, ids)
		if err != nil {
			return err
		}
		var order []uuid.UUID
		for rows.Next() {
			var reply uuid.UUID
			var r recipient
			if err := rows.Scan(&reply, &r.parent, &r.commenter, &r.hash, &r.sealed); err != nil {
				rows.Close()
				return err
			}
			order = append(order, reply)
			rcpt = append(rcpt, r)
		}
		rows.Close()
		if err := rows.Err(); err != nil || len(order) == 0 {
			return err
		}
		crows, err := tx.Query(ctx, `SELECT `+commentColumns+` FROM hatamoto_comments WHERE id = ANY($1)`, order)
		if err != nil {
			return err
		}
		defer crows.Close()
		byID := make(map[uuid.UUID]Comment, len(order))
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
		for i, id := range order {
			out = append(out, ReplyNotice{Reply: byID[id], ParentID: rcpt[i].parent, CommenterID: rcpt[i].commenter})
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	kept := out[:0]
	for i, n := range out {
		email, ok := s.keys.OpenEmail(rcpt[i].sealed, rcpt[i].hash)
		if !ok || email == "" {
			continue
		}
		n.Email = email
		kept = append(kept, n)
	}
	return kept, nil
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
