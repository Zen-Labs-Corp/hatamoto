//go:build integration

package agents

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/testutil/pgbouncertest"
)

// Behind a transaction pooler a running backfill must still turn a second run
// into a no-op, and give the lock back when it ends.
func TestPooler_AttributionBackfillSingleRun(t *testing.T) {
	dsn := os.Getenv("HATAMOTO_TEST_DB_URL")
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if dsn == "" {
		t.Skip("HATAMOTO_TEST_DB_URL not set")
	}
	direct := pgbouncertest.ScratchDB(t, dsn)
	pooled := pgbouncertest.Start(t, direct, 2)
	ctx := context.Background()
	db, err := database.New(ctx, config.DatabaseConfig{
		URL: pooled, DirectURL: direct, MaxConnections: 4,
		MaxConnLifetime: time.Minute, MaxConnIdleTime: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)

	// A first run holds the job while its resolver runs; a second run started
	// from inside it must return at once without resolving anything.
	if _, err := db.Pool().Exec(ctx, `INSERT INTO hatamoto_agent_daily (day, agent_id, path, status, hits)
		VALUES (current_date, 'pooler-probe', '/pooler-probe', 200, 1)`); err != nil {
		t.Fatalf("seed a candidate path: %v", err)
	}
	var innerRan, innerResolved bool
	resolve := func(ctx context.Context, p string) (*uuid.UUID, int) {
		if innerRan {
			return nil, 404
		}
		innerRan = true
		n, err := NewAttributionBackfill(db, func(context.Context, string) (*uuid.UUID, int) {
			innerResolved = true
			return nil, 404
		}, 0).Run(ctx)
		if err != nil || n != 0 {
			t.Errorf("overlapping run = %d, %v; want a no-op", n, err)
		}
		return nil, 404
	}
	if _, err := NewAttributionBackfill(db, resolve, 0).Run(ctx); err != nil {
		t.Fatal(err)
	}
	if !innerRan {
		t.Fatal("the resolver never ran, so the overlap was not exercised")
	}
	if innerResolved {
		t.Fatal("the overlapping run resolved paths: the job lock did not exclude it")
	}
	// The lock is free again.
	if _, err := NewAttributionBackfill(db, func(context.Context, string) (*uuid.UUID, int) { return nil, 404 }, 0).Run(ctx); err != nil {
		t.Fatal(err)
	}
	var held bool
	if err := db.Pool().QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_locks WHERE locktype='advisory' AND granted
		AND database = (SELECT oid FROM pg_database WHERE datname = current_database()))`).Scan(&held); err != nil {
		t.Fatal(err)
	}
	if held {
		t.Fatal("an advisory lock is still held after both runs")
	}
}
