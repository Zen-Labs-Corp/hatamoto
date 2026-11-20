//go:build integration

package siteid

import (
	"context"
	"os"
	"regexp"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

var uuidShape = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// Run with: DATABASE_URL=... go test -tags=integration ./internal/siteid/...
func newIntegrationPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set, skipping integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestGetReadsStableID(t *testing.T) {
	pool := newIntegrationPool(t)
	ctx := context.Background()

	s := New(pool)
	a, err := s.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if a == "" || a != b {
		t.Fatalf("site id unstable or empty: %q vs %q", a, b)
	}
	if !uuidShape.MatchString(a) {
		t.Errorf("site id %q is not a UUID", a)
	}
	// A fresh Store has no cache, so this value comes from the database.
	c, err := New(pool).Get(ctx)
	if err != nil || c != a {
		t.Errorf("second store = %q (%v), want %q", c, err, a)
	}
}
