//go:build integration

package worker

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/testutil/pgbouncertest"
)

// With two pooled server connections, a bare SET lock_timeout would land on one
// of them and the DDL on the other, which then waits without a bound. Each DDL
// must carry its own lock_timeout and leave nothing behind.
func TestPooler_PartitionWorkerLeavesNoSessionState(t *testing.T) {
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

	// A DDL that has to wait for a lock held elsewhere gives up after the
	// worker's bound (5s), whichever server connection it is handed.
	holder, err := pgx.Connect(ctx, direct)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close(ctx)
	tx, err := holder.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `LOCK TABLE public.`+w.parent+` IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	wctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	start := time.Now()
	err = w.execLocal(wctx, `LOCK TABLE public.`+w.parent+` IN ACCESS EXCLUSIVE MODE`)
	var pe *pgconn.PgError
	if !errors.As(err, &pe) || pe.Code != "55P03" || time.Since(start) > 10*time.Second {
		t.Fatalf("DDL behind a held lock = %v after %v; want lock_not_available near 5s", err, time.Since(start))
	}
	_ = tx.Rollback(ctx)

	name := "hatamoto_agent_hits_" + time.Now().UTC().Format("2006_01")
	var ok bool
	if err := db.Pool().QueryRow(ctx, `SELECT to_regclass('public.'||$1) IS NOT NULL`, name).Scan(&ok); err != nil || !ok {
		t.Fatalf("partition %s missing (err=%v)", name, err)
	}
}
