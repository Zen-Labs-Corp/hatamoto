package worker

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"hatamoto.dev/core/internal/database"
)

// ErrNotClaimed is returned by a job wrapped with Once when another server
// holds the run. The scheduler treats it as a silent skip.
var ErrNotClaimed = errors.New("worker: another server holds this run")

// Claimer takes job runs in the database so that one run happens across all
// servers sharing it.
type Claimer struct{ db *database.DB }

// NewClaimer returns a Claimer on db.
func NewClaimer(db *database.DB) *Claimer { return &Claimer{db: db} }

// Claim takes the run of id for window from the database clock; false when
// another process holds it.
func (c *Claimer) Claim(ctx context.Context, id string, window time.Duration) (bool, error) {
	var got string
	err := c.db.Pool().QueryRow(ctx, `
		INSERT INTO hatamoto_job_claims (job_id, claimed_until, claimed_at)
		VALUES ($1, now() + $2 * interval '1 microsecond', now())
		ON CONFLICT (job_id) DO UPDATE
		SET claimed_until = EXCLUDED.claimed_until, claimed_at = EXCLUDED.claimed_at
		WHERE hatamoto_job_claims.claimed_until <= now()
		RETURNING job_id`, id, window.Microseconds()).Scan(&got)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("failed to claim job %s: %w", id, err)
	}
	return true, nil
}

// Once runs fn only when claim wins. A lost claim returns ErrNotClaimed; a
// claim error is returned and fn does not run.
func Once(claim func(ctx context.Context, id string, window time.Duration) (bool, error), id string, window time.Duration, fn func(context.Context) error) func(context.Context) error {
	return func(ctx context.Context) error {
		ok, err := claim(ctx, id, window)
		if err != nil {
			return err
		}
		if !ok {
			return ErrNotClaimed
		}
		return fn(ctx)
	}
}
