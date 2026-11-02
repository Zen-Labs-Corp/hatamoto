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

// DATABASE_URL=... go test -tags=integration ./internal/worker -run PruneRecovery
func TestPruneRecoveryCopies_Integration(t *testing.T) {
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

	suffix := uuid.NewString()[:8]
	var schemaID uuid.UUID
	if err := db.Pool().QueryRow(ctx, `SELECT id FROM hatamoto_entity_schemas WHERE slug = 'person'`).Scan(&schemaID); err != nil {
		t.Fatalf("person schema (is the database migrated?): %v", err)
	}
	var entityID uuid.UUID
	if err := db.Pool().QueryRow(ctx, `INSERT INTO hatamoto_entities (schema_id, name, slug, status, attributes)
		VALUES ($1, 'Prune', $2, 'published', '{}'::jsonb) RETURNING id`, schemaID, "prune-"+suffix).Scan(&entityID); err != nil {
		t.Fatal(err)
	}
	oldUser, newUser := uuid.New(), uuid.New()
	t.Cleanup(func() {
		_, _ = db.Pool().Exec(ctx, `DELETE FROM hatamoto_entities WHERE id = $1`, entityID)
		_, _ = db.Pool().Exec(ctx, `DELETE FROM hatamoto_users WHERE id = ANY($1)`, []uuid.UUID{oldUser, newUser})
	})
	for i, u := range []uuid.UUID{oldUser, newUser} {
		exec(`INSERT INTO hatamoto_users (id, email, name) VALUES ($1, $2, 'u')`, u, []string{"old-", "new-"}[i]+suffix+"@example.com")
	}
	exec(`INSERT INTO hatamoto_admin_recovery_copies (user_id, entity_id, content, saved_at) VALUES
		($1, $3, 'old', now() - interval '31 days'),
		($2, $3, 'new', now() - interval '29 days')`, oldUser, newUser, entityID)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := NewPruneWorker(db, 2, logger).Run(ctx); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.Pool().QueryRow(ctx, `SELECT count(*) FROM hatamoto_admin_recovery_copies WHERE user_id = $1`, oldUser).Scan(&n); err != nil || n != 0 {
		t.Errorf("31 day old copy: %d rows left (err=%v)", n, err)
	}
	if err := db.Pool().QueryRow(ctx, `SELECT count(*) FROM hatamoto_admin_recovery_copies WHERE user_id = $1`, newUser).Scan(&n); err != nil || n != 1 {
		t.Errorf("29 day old copy: %d rows left (err=%v), want 1", n, err)
	}
}

// DATABASE_URL=... go test -tags=integration ./internal/worker -run Prune404
func TestPrune404Retention_Integration(t *testing.T) {
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
	prefix := "/prune404-" + uuid.NewString()[:8]
	t.Cleanup(func() { _, _ = db.Pool().Exec(ctx, `DELETE FROM hatamoto_404_log WHERE path LIKE $1`, prefix+"%") })
	count := func(path string) int {
		t.Helper()
		var n int
		if err := db.Pool().QueryRow(ctx, `SELECT count(*) FROM hatamoto_404_log WHERE path = $1`, prefix+path).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	seed := func() {
		exec(`DELETE FROM hatamoto_404_log WHERE path LIKE $1`, prefix+"%")
		exec(`INSERT INTO hatamoto_404_log (path, last_seen_at) VALUES
			($1 || '/20', now() - interval '20 days'),
			($1 || '/40', now() - interval '40 days'),
			($1 || '/100', now() - interval '100 days')`, prefix)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	seed()
	w := NewPruneWorker(db, 2, logger)
	w.SetRetention404(func(context.Context) int { return 30 })
	if err := w.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if count("/20") != 1 || count("/40") != 0 || count("/100") != 0 {
		t.Errorf("30 days: 20d=%d 40d=%d 100d=%d", count("/20"), count("/40"), count("/100"))
	}

	seed()
	w.SetRetention404(func(context.Context) int { return 7 }) // unknown: 90 days
	if err := w.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if count("/20") != 1 || count("/40") != 1 || count("/100") != 0 {
		t.Errorf("unknown: 20d=%d 40d=%d 100d=%d", count("/20"), count("/40"), count("/100"))
	}
}
