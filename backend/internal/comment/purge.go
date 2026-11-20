package comment

import "context"

// purgeRounds bounds the leaf-first delete loop: a thread deeper than this is
// finished by the next daily run.
const purgeRounds = 20

// PurgeSpamTrash deletes spam and trash comments whose updated_at (the time of
// the last status change) is older than Settings.PurgeDays. A delete cascades to
// replies, so leaves go first, in rounds; an old one that still has replies is
// blanked instead, like an erase. Fires no observer and needs no recount: spam and
// trash are not counted. Returns the number of rows deleted plus blanked.
func (s *Service) PurgeSpamTrash(ctx context.Context) (int64, error) {
	set, err := s.Settings(ctx)
	if err != nil {
		return 0, err
	}
	cutoff := s.utcNow().AddDate(0, 0, -set.PurgeDays)
	var n int64
	for round := 0; round < purgeRounds; round++ {
		tag, err := s.db.Pool().Exec(ctx, `DELETE FROM hatamoto_comments WHERE id IN (
			SELECT x.id FROM hatamoto_comments x
			WHERE x.status IN ('spam','trash') AND x.updated_at < $1
			  AND NOT EXISTS (SELECT 1 FROM hatamoto_comments ch WHERE ch.parent_id = x.id)
			LIMIT 1000)
		  AND status IN ('spam','trash') AND updated_at < $1`, cutoff)
		if err != nil {
			return n, err
		}
		n += tag.RowsAffected()
		if tag.RowsAffected() == 0 {
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
