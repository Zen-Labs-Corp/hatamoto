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
