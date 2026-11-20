package comment

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// Resync repairs what a bulk load of comment rows leaves behind: it moves
// hatamoto_comments_seq past the highest seq and rebuilds every entity's
// approved and pending counts from the rows. It never touches hatamoto_entities
// (its UPDATE trigger purges the page cache) nor any comment mode. Call it in
// the transaction that loaded the rows.
func Resync(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, `SELECT setval('hatamoto_comments_seq',
		COALESCE((SELECT max(seq) FROM hatamoto_comments), 1),
		(SELECT count(*) > 0 FROM hatamoto_comments))`); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO hatamoto_comment_entities (entity_id)
		SELECT DISTINCT entity_id FROM hatamoto_comments ON CONFLICT DO NOTHING`); err != nil {
		return err
	}
	// Same definitions as recount: approved and pending rows of the entity.
	_, err := tx.Exec(ctx, `UPDATE hatamoto_comment_entities e SET
			comment_count = COALESCE(x.a, 0), pending_count = COALESCE(x.p, 0), updated_at = now()
		FROM hatamoto_comment_entities e2
		LEFT JOIN (SELECT entity_id,
				count(*) FILTER (WHERE status = 'approved') a,
				count(*) FILTER (WHERE status = 'pending') p
			FROM hatamoto_comments GROUP BY entity_id) x ON x.entity_id = e2.entity_id
		WHERE e.entity_id = e2.entity_id
			AND (e.comment_count, e.pending_count) IS DISTINCT FROM (COALESCE(x.a, 0), COALESCE(x.p, 0))`)
	return err
}
