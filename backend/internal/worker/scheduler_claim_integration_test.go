//go:build integration

package worker

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Two schedulers on one database tick the same job id: only the first
// one to claim runs it, and the other never does within the lease.
func TestSchedulerClaimsOnePerInterval_Integration(t *testing.T) {
	ctx := context.Background()
	dbA, dbB := openStatusDB(t), openStatusDB(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	a, b := NewScheduler(dbA, log), NewScheduler(dbB, log)
	a.SetClaimer(NewClaimer(dbA))
	b.SetClaimer(NewClaimer(dbB))

	pluginID := "plugin:claimtest-" + uuid.NewString() + ":tick"
	t.Cleanup(func() {
		_, _ = dbA.Pool().Exec(context.Background(), `DELETE FROM hatamoto_job_claims WHERE job_id = $1 OR job_id = 'prune'`, pluginID)
	})
	// The catalogue id is shared state: start from a free row.
	_, _ = dbA.Pool().Exec(ctx, `DELETE FROM hatamoto_job_claims WHERE job_id = 'prune'`)

	for _, id := range []string{pluginID, "prune"} {
		var runs int
		fn := func(context.Context) error { runs++; return nil }
		a.register(id, time.Minute, 0, fn)
		b.register(id, time.Minute, 0, fn)
		for _, s := range []*Scheduler{a, b, b} {
			s.runJob(ctx, id, fn)
		}
		if runs != 1 {
			t.Fatalf("%s: runs = %d, want 1", id, runs)
		}
	}
}
