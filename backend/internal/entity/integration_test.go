//go:build integration

package entity

import (
	"context"
	"os"
	"testing"
	"time"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
)

// newIntegrationDB opens DATABASE_URL (a migrated database) or skips.
// Run with: DATABASE_URL=... go test -tags=integration ./internal/entity/...
func newIntegrationDB(t *testing.T) *database.DB {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set — skipping integration test")
	}
	db, err := database.New(context.Background(), config.DatabaseConfig{
		URL: dsn, MaxConnections: 6, MinConnections: 1,
		MaxConnLifetime: 30 * time.Minute, MaxConnIdleTime: 5 * time.Minute,
	})
	if err != nil {
		t.Fatalf("database.New: %v", err)
	}
	t.Cleanup(db.Close)
	return db
}
