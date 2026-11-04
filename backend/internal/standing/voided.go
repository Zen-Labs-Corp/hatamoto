package standing

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Voided says why and when the run's approval ended.
type Voided struct {
	At     time.Time
	Reason string
}

// LastVoided returns the most recent void of the run's approval, or nil when
// the run has an active approval or never had one.
func (s *Store) LastVoided(ctx context.Context, plugin, run string) (*Voided, error) {
	var v Voided
	err := s.DB.Pool().QueryRow(ctx, `
		SELECT voided_at, void_reason FROM hatamoto_standing_approvals a
		WHERE plugin = $1 AND run = $2 AND voided_at IS NOT NULL
		  AND NOT EXISTS (SELECT 1 FROM hatamoto_standing_approvals b WHERE b.plugin = $1 AND b.run = $2 AND b.voided_at IS NULL)
		ORDER BY voided_at DESC, id DESC LIMIT 1`, plugin, run).Scan(&v.At, &v.Reason)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read the last void: %w", err)
	}
	return &v, nil
}
