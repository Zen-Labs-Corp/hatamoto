package standing

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// WaitingRun is a cron started run whose changes are staged and wait for the
// owner because they stepped outside the approved rule.
type WaitingRun struct {
	RunID       uuid.UUID
	Run         string
	ChangesetID int64
	Items       int
	At          time.Time
}

// Waiting lists the plugin's finished cron started runs whose changeset is
// still staged, newest first. A run that staged nothing is not waiting: there
// is nothing for the owner to review.
func (s *Store) Waiting(ctx context.Context, plugin string) ([]WaitingRun, error) {
	rows, err := s.DB.Pool().Query(ctx, `
		SELECT r.id, r.name, c.id, c.item_count, r.created_at
		FROM hatamoto_runs r JOIN hatamoto_changesets c ON c.run_id = r.id
		WHERE r.plugin = $1 AND r.trigger = 'cron' AND r.state = 'finished'
		  AND c.state = 'staged' AND c.item_count > 0 AND c.expires_at > $2
		ORDER BY r.created_at DESC LIMIT 50`, plugin, s.now())
	if err != nil {
		return nil, fmt.Errorf("failed to list waiting runs: %w", err)
	}
	defer rows.Close()
	var out []WaitingRun
	for rows.Next() {
		var w WaitingRun
		if err := rows.Scan(&w.RunID, &w.Run, &w.ChangesetID, &w.Items, &w.At); err != nil {
			return nil, fmt.Errorf("failed to read waiting run: %w", err)
		}
		out = append(out, w)
	}
	return out, rows.Err()
}
