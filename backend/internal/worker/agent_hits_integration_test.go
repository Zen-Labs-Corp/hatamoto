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

// DATABASE_URL=... go test -tags=integration ./internal/worker -run AgentHits
func TestAgentHitsWorkers_Integration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set — skipping integration test")
	}
	ctx := context.Background()
	db, err := database.New(ctx, config.DatabaseConfig{URL: dsn, MaxConnections: 4, MinConnections: 1,
		MaxConnLifetime: time.Minute, MaxConnIdleTime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Pool().Exec(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}

	t.Run("ip truncation", func(t *testing.T) {
		marker := "it-" + uuid.NewString()[:6]
		t.Cleanup(func() { _, _ = db.Pool().Exec(ctx, `DELETE FROM hatamoto_agent_hits WHERE agent_id = $1`, marker) })
		oldID, newID, v6ID := uuid.New(), uuid.New(), uuid.New()
		exec(`INSERT INTO hatamoto_agent_hits (id, occurred_at, agent_id, ip, source) VALUES
			($1, now() - interval '31 days', $4, '203.0.113.77', 'backend'),
			($2, now() - interval '1 day',   $4, '203.0.113.88', 'backend'),
			($3, now() - interval '40 days', $4, '2001:db8:abcd:1234::1', 'backend')`, oldID, newID, v6ID, marker)

		if err := NewAgentHitsMaintenanceWorker(db, 2, logger).Run(ctx); err != nil {
			t.Fatal(err)
		}
		check := func(id uuid.UUID, wantIP string, wantTrunc bool) {
			t.Helper()
			var ip string
			var trunc bool
			if err := db.Pool().QueryRow(ctx, `SELECT ip::text, ip_truncated FROM hatamoto_agent_hits WHERE id = $1`, id).Scan(&ip, &trunc); err != nil {
				t.Fatal(err)
			}
			if ip != wantIP || trunc != wantTrunc {
				t.Errorf("%s: ip=%s truncated=%v, want %s/%v", id, ip, trunc, wantIP, wantTrunc)
			}
		}
		check(oldID, "203.0.113.0/24", true)
		check(v6ID, "2001:db8:abcd::/48", true)
		check(newID, "203.0.113.88/32", false) // inside the 30 day window: untouched
	})

	t.Run("partition create and drop", func(t *testing.T) {
		w := NewAgentHitsPartitionWorker(db, 2, 3, logger)
		name, ddl := w.ddl(time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC))
		exec(ddl)
		t.Cleanup(func() { _, _ = db.Pool().Exec(ctx, `DROP TABLE IF EXISTS public.`+name) })
		if err := w.Run(ctx); err != nil {
			t.Fatal(err)
		}
		var exists bool
		if err := db.Pool().QueryRow(ctx, `SELECT to_regclass('public.'||$1) IS NOT NULL`, name).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists {
			t.Errorf("aged partition %s was not dropped", name)
		}
		var def bool
		if err := db.Pool().QueryRow(ctx, `SELECT to_regclass('public.hatamoto_agent_hits_default') IS NOT NULL`).Scan(&def); err != nil || !def {
			t.Errorf("default partition must survive (err=%v)", err)
		}
	})
}
