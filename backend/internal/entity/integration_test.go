//go:build integration

package entity

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

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

// searchToken is a unique word for a full-text search test. It is letters only:
// the text search parser reads a hex run such as 23e9b4dd as a number with an
// exponent and 12345678 as a signed number, so the query would not match the
// name it came from (about 7% of random uuid prefixes, on PostgreSQL 16 and 18).
func searchToken() string {
	b := []byte(uuid.NewString()[:8])
	for i, c := range b {
		if c >= '0' && c <= '9' {
			b[i] = 'g' + (c - '0')
		}
	}
	return "zq" + string(b)
}

func TestSearchTokenMatchesItsName(t *testing.T) {
	db := newIntegrationDB(t)
	for range 500 {
		tok := searchToken()
		var ok bool
		if err := db.Pool().QueryRow(context.Background(),
			`SELECT to_tsvector('simple', 'person-' || $1::text) @@ websearch_to_tsquery('simple', $1::text)`, tok).Scan(&ok); err != nil {
			t.Fatal(err)
		}
		if !ok {
			t.Fatalf("search token %q does not match a name holding it", tok)
		}
	}
}
