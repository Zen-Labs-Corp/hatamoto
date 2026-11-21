package worker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"hatamoto.dev/core/internal/database"
)

// Claimer decides whether this server may run a job now.
type Claimer interface {
	Claim(ctx context.Context, jobID string, lease time.Duration) (bool, error)
}

// ClaimStore claims job runs in hatamoto_job_claims. It is the shared helper
// for anything that must run once per interval across servers.
type ClaimStore struct {
	db     *database.DB
	holder string
}

// NewClaimStore returns a store on db that claims as holder.
func NewClaimStore(db *database.DB, holder string) *ClaimStore {
	return &ClaimStore{db: db, holder: holder}
}

// Claim reports whether this holder won jobID for lease. The holder that holds
// the row may always claim again (one server never skips its own tick). It is
// one statement on the database clock, so it is safe behind a transaction pooler.
func (c *ClaimStore) Claim(ctx context.Context, jobID string, lease time.Duration) (bool, error) {
	var got string
	err := c.db.Pool().QueryRow(ctx, `
		INSERT INTO hatamoto_job_claims (job_id, holder, claimed_at, claimed_until)
		VALUES ($1, $2, now(), now() + $3 * interval '1 millisecond')
		ON CONFLICT (job_id) DO UPDATE SET
			holder = EXCLUDED.holder,
			claimed_at = EXCLUDED.claimed_at,
			claimed_until = EXCLUDED.claimed_until
		WHERE hatamoto_job_claims.claimed_until <= now()
		   OR hatamoto_job_claims.holder = EXCLUDED.holder
		RETURNING job_id`,
		jobID, c.holder, lease.Milliseconds()).Scan(&got)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("worker: claim %s: %w", jobID, err)
	}
	return true, nil
}

// DefaultHolder names this server: HATAMOTO_INSTANCE_ID, else the host name,
// else "local". Read on each call (like DrainMode reads WORKER_DRAIN).
func DefaultHolder() string {
	if v := strings.TrimSpace(os.Getenv("HATAMOTO_INSTANCE_ID")); v != "" {
		return v
	}
	if h, err := os.Hostname(); err == nil && h != "" {
		return h
	}
	return "local"
}

// claimLease is how long one claimed run keeps the job: just under the
// shortest wait between two ticks, so the next interval is claimable again.
func claimLease(every, jitter time.Duration) time.Duration {
	ms := (every - jitter).Milliseconds() * 9 / 10
	return max(time.Second, time.Duration(ms)*time.Millisecond)
}
