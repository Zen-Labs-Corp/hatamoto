package comment

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// surgeStateName is the comment surge row in hatamoto_digest_state; sent_through
// holds the last time any replica saw the site in surge mode.
const surgeStateName = "comment_surge"

// NoteSurge records that this replica sees the site in surge mode now and
// reports whether the caller starts a new episode: there was no earlier
// heartbeat, or the last one is at least quiet old. Callers serialise on the
// row, so across replicas exactly one wins per episode.
func (s *Service) NoteSurge(ctx context.Context, quiet time.Duration) (bool, error) {
	now := s.utcNow()
	var won bool
	err := s.db.WithTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO hatamoto_digest_state (name, sent_through)
			VALUES ($1, 'epoch') ON CONFLICT (name) DO NOTHING`, surgeStateName); err != nil {
			return err
		}
		var prev time.Time
		if err := tx.QueryRow(ctx, `SELECT sent_through FROM hatamoto_digest_state
			WHERE name = $1 FOR UPDATE`, surgeStateName).Scan(&prev); err != nil {
			return err
		}
		won = !prev.After(now.Add(-quiet))
		_, err := tx.Exec(ctx, `UPDATE hatamoto_digest_state SET sent_through = GREATEST(sent_through, $2)
			WHERE name = $1`, surgeStateName, now)
		return err
	})
	if err != nil {
		return false, fmt.Errorf("failed to note the comment surge: %w", err)
	}
	return won, nil
}
