//go:build integration

package worker

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/testutil/pgbouncertest"
)

// With one pooled server connection, a bare SET lock_timeout in the partition
// worker would stick to it and show up in the next transaction.
func TestPooler_PartitionWorkerLeavesNoSessionState(t *testing.T) {
	dsn := os.Getenv("HATAMOTO_TEST_DB_URL")
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if dsn == "" {
		t.Skip("HATAMOTO_TEST_DB_URL not set")
	}
	direct := pgbouncertest.ScratchDB(t, dsn)
	pooled := pgbouncertest.Start(t, direct, 1)

	ctx := context.Background()
	db, err := database.New(ctx, config.DatabaseConfig{
		URL: pooled, DirectURL: direct, MaxConnections: 4,
		MaxConnLifetime: time.Minute, MaxConnIdleTime: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	w := NewAgentHitsPartitionWorker(db, 2, 3, slog.New(slog.NewTextHandler(io.Discard, nil)))

	if err := w.Run(ctx); err != nil {
		t.Fatal(err)
	}
	w.EnsureCurrentAndNext(ctx)

	var lt string
	if err := db.Pool().QueryRow(ctx, `SELECT current_setting('lock_timeout')`).Scan(&lt); err != nil {
		t.Fatal(err)
	}
	if lt == "5s" {
		t.Fatalf("lock_timeout leaked through the pooler: %s", lt)
	}
	name := "hatamoto_agent_hits_" + time.Now().UTC().Format("2006_01")
	var ok bool
	if err := db.Pool().QueryRow(ctx, `SELECT to_regclass('public.'||$1) IS NOT NULL`, name).Scan(&ok); err != nil || !ok {
		t.Fatalf("partition %s missing (err=%v)", name, err)
	}
}
