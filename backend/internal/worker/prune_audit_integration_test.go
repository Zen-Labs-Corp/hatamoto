//go:build integration

package worker

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
)

// DATABASE_URL=... go test -tags=integration ./internal/worker -run PruneAuditRetention
func TestPruneAuditRetention_Integration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set, skipping integration test")
	}
	ctx := context.Background()
	db, err := database.New(ctx, config.DatabaseConfig{URL: dsn, MaxConnections: 4, MinConnections: 1,
		MaxConnLifetime: time.Minute, MaxConnIdleTime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Pool().Exec(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	tag := "prune-audit-" + uuid.NewString()[:8]
	t.Cleanup(func() {
		_, _ = db.Pool().Exec(ctx, `DELETE FROM hatamoto_admin_audit WHERE action = $1`, tag)
		_, _ = db.Pool().Exec(ctx, `DELETE FROM hatamoto_mcp_audit WHERE tool = $1`, tag)
	})
	seed := func() {
		exec(`DELETE FROM hatamoto_admin_audit WHERE action = $1`, tag)
		exec(`DELETE FROM hatamoto_mcp_audit WHERE tool = $1`, tag)
		exec(`INSERT INTO hatamoto_admin_audit (actor, action, target, detail, at) VALUES
			('t', $1, '400', '{}'::jsonb, now() - interval '400 days'),
			('t', $1, '100', '{}'::jsonb, now() - interval '100 days')`, tag)
		exec(`INSERT INTO hatamoto_mcp_audit (api_key_name, tool, ok, at) VALUES
			('k', $1, TRUE, now() - interval '100 days'),
			('k', $1, TRUE, now() - interval '10 days')`, tag)
	}
	counts := func() (admin, agent int) {
		t.Helper()
		if err := db.Pool().QueryRow(ctx, `SELECT count(*) FROM hatamoto_admin_audit WHERE action = $1`, tag).Scan(&admin); err != nil {
			t.Fatal(err)
		}
		if err := db.Pool().QueryRow(ctx, `SELECT count(*) FROM hatamoto_mcp_audit WHERE tool = $1`, tag).Scan(&agent); err != nil {
			t.Fatal(err)
		}
		return admin, agent
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	seed()
	if err := NewPruneWorker(db, 2, logger).Run(ctx); err != nil {
		t.Fatal(err)
	}
	if a, m := counts(); a != 1 || m != 1 {
		t.Errorf("defaults: admin %d agent %d, want 1 1", a, m)
	}

	seed()
	w := NewPruneWorker(db, 2, logger)
	w.SetAuditRetention(func(context.Context) (int, int, error) { return 730, 180, nil })
	if err := w.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if a, m := counts(); a != 2 || m != 2 {
		t.Errorf("730/180: admin %d agent %d, want 2 2", a, m)
	}
}
