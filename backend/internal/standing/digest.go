package standing

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// DigestName is the key of the daily summary in hatamoto_digest_state.
const DigestName = "standing_digest"

// DigestRow is one automatic run in the daily summary.
type DigestRow struct {
	Plugin      string
	Run         string
	RunID       uuid.UUID
	ChangesetID int64
	Changes     int
	At          time.Time
}

// Digest lists the changesets published automatically since the given time,
// newest first, with how many changes each applied.
func (s *Store) Digest(ctx context.Context, since time.Time) ([]DigestRow, error) {
	rows, err := s.DB.Pool().Query(ctx, `
		SELECT c.plugin, r.name, r.id, c.id, c.published_at,
		       (SELECT count(*) FROM hatamoto_changeset_items i WHERE i.changeset_id = c.id AND i.result = 'applied')
		FROM hatamoto_changesets c JOIN hatamoto_runs r ON r.id = c.run_id
		WHERE c.auto_published AND c.state = 'published' AND c.published_at >= $1
		ORDER BY c.published_at DESC LIMIT 200`, since)
	if err != nil {
		return nil, fmt.Errorf("failed to read the daily summary: %w", err)
	}
	defer rows.Close()
	var out []DigestRow
	for rows.Next() {
		var d DigestRow
		if err := rows.Scan(&d.Plugin, &d.Run, &d.RunID, &d.ChangesetID, &d.At, &d.Changes); err != nil {
			return nil, fmt.Errorf("failed to read a summary row: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ClaimDigest records that the summary for the period ending at through is
// being sent, and reports whether this caller won the claim. Only one caller
// per period wins, across replicas and restarts.
func (s *Store) ClaimDigest(ctx context.Context, through time.Time) (bool, error) {
	var got time.Time
	err := s.DB.Pool().QueryRow(ctx, `
		INSERT INTO hatamoto_digest_state (name, sent_through) VALUES ($1, $2)
		ON CONFLICT (name) DO UPDATE SET sent_through = EXCLUDED.sent_through
		WHERE hatamoto_digest_state.sent_through < EXCLUDED.sent_through
		RETURNING sent_through`, DigestName, through).Scan(&got)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("failed to claim the daily summary: %w", err)
	}
	return true, nil
}
