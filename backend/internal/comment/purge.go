package comment

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// purgeRounds bounds the leaf-first delete loop: a thread deeper than this is
// finished by the next daily run.
const purgeRounds = 20

// PurgeSpamTrash deletes spam and trash comments whose updated_at (the time of
// the last status change) is older than Settings.PurgeDays. A delete cascades to
// replies, so leaves go first, in rounds; an old one that still has replies is
// blanked instead, like an erase. Needs no recount: spam and trash are not
// counted. Dead comments are never purged. Fires OnDeleted (batch context) for
// every deleted row that was ever approved, after the deletes; a blanked row was never public and fires
// nothing. Returns the number of rows deleted plus blanked.
func (s *Service) PurgeSpamTrash(ctx context.Context) (int64, error) {
	set, err := s.Settings(ctx)
	if err != nil {
		return 0, err
	}
	cutoff := s.utcNow().AddDate(0, 0, -set.PurgeDays)
	var n int64
	var gone []Comment
	defer func() { s.announceDeleted(ctx, gone) }() // also after a failed round: earlier rounds are committed
	for i := 0; i < purgeRounds; i++ {
		rows, err := s.db.Pool().Query(ctx, `DELETE FROM hatamoto_comments WHERE id IN (
			SELECT x.id FROM hatamoto_comments x
			WHERE x.status IN ('spam','trash') AND x.updated_at < $1
			  AND NOT EXISTS (SELECT 1 FROM hatamoto_comments ch WHERE ch.parent_id = x.id)
			LIMIT 1000)
		  AND status IN ('spam','trash') AND updated_at < $1
		RETURNING `+commentColumns, cutoff)
		if err != nil {
			return n, err
		}
		round, err := collectComments(rows)
		if err != nil {
			return n, err
		}
		n += int64(len(round))
		gone = append(gone, round...)
		if len(round) == 0 {
			break
		}
	}
	// What is left has replies (or the rounds ran out): blank it, keep the row.
	// updated_at stays, so the statement is idempotent through body_raw.
	tag, err := s.db.Pool().Exec(ctx, `UPDATE hatamoto_comments SET author_name = '', body_raw = $2, body_html = $2,
			has_links = false, link_domains = '{}', body_hash = '', ip_hash = '', net_hash = '', user_agent = '',
			notify_replies = false
		WHERE status IN ('spam','trash') AND updated_at < $1 AND body_raw <> $2`, cutoff, deletedBody)
	if err != nil {
		return n, err
	}
	return n + tag.RowsAffected(), nil
}

// collectComments reads and closes rows of commentColumns.
func collectComments(rows pgx.Rows) ([]Comment, error) {
	defer rows.Close()
	var out []Comment
	for rows.Next() {
		c, err := scanComment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// announceDeleted fires OnDeleted for each comment that was ever published
// (approved_at set: approved, dead, or moved on from there) with a batch
// context. Comments that were never public (held, bot spam) fire nothing.
func (s *Service) announceDeleted(ctx context.Context, cs []Comment) {
	s.announce(ctx, cs, func(c Comment) bool { return c.ApprovedAt != nil })
}

// announceErased fires OnDeleted for each erased comment that was ever announced
// through comment.created: every status except a draft and a comment the spam
// filter stored as spam (never approved, verdict spam). A pending comment counts,
// its name and body went out with comment.created. Then one OnBatch for the
// entities of the comments that had been public, because the observers skip the
// page drop inside a batch.
func (s *Service) announceErased(ctx context.Context, cs []Comment) {
	s.announce(ctx, cs, func(c Comment) bool { return c.ApprovedAt != nil })
	shown := map[uuid.UUID]bool{}
	for _, c := range cs {
		if c.ApprovedAt != nil {
			shown[c.EntityID] = true
		}
	}
	if len(shown) > 0 {
		s.fireBatch(ctx, sortedUUIDs(shown))
	}
}

func (s *Service) announce(ctx context.Context, cs []Comment, keep func(Comment) bool) {
	bctx := WithBatch(ctx)
	for _, c := range cs {
		if keep(c) {
			s.obs.each("deleted", func(o Observer) { o.OnDeleted(bctx, c) })
		}
	}
}
