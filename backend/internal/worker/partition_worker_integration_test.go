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
)

// The worker must leave no session state behind (SET LOCAL only) and one
// failing statement must not stop the ones after it.
func TestPartitionWorker_NoSessionLeakAndContinuesAfterFailure(t *testing.T) {
	dsn := os.Getenv("HATAMOTO_TEST_DB_URL")
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if dsn == "" {
		t.Skip("DATABASE_URL not set, skipping integration test")
	}
	ctx := context.Background()
	db, err := database.New(ctx, config.DatabaseConfig{URL: dsn, MaxConnections: 1, MinConnections: 1,
		MaxConnLifetime: time.Minute, MaxConnIdleTime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	w := NewAgentHitsPartitionWorker(db, 2, 3, slog.New(slog.NewTextHandler(io.Discard, nil)))

	if err := w.execLocal(ctx, `SELECT * FROM hm_no_such_table`); err == nil {
		t.Fatal("bad statement succeeded")
	}
	var applied string
	if err := db.Pool().QueryRow(ctx, `SELECT 'ok'`).Scan(&applied); err != nil {
		t.Fatalf("connection unusable after a failed statement: %v", err)
	}
	if err := w.execLocal(ctx, `SELECT 1`); err != nil {
		t.Fatalf("statement after a failure: %v", err)
	}

	// MaxConns is 1: Run and the check below share one server connection.
	if err := w.Run(ctx); err != nil {
		t.Fatal(err)
	}
	w.EnsureCurrentAndNext(ctx)
	var lt string
	if err := db.Pool().QueryRow(ctx, `SELECT current_setting('lock_timeout')`).Scan(&lt); err != nil {
		t.Fatal(err)
	}
	if lt == "5s" {
		t.Fatalf("lock_timeout leaked onto the pooled connection: %s", lt)
	}
	name := "hatamoto_agent_hits_" + time.Now().UTC().Format("2006_01")
	{
		var ok bool
		if err := db.Pool().QueryRow(ctx, `SELECT to_regclass('public.'||$1) IS NOT NULL`, name).Scan(&ok); err != nil || !ok {
			t.Fatalf("partition %s missing (err=%v)", name, err)
		}
	}
}
