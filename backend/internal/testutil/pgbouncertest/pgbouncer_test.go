//go:build integration

package pgbouncertest

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestStartPoolsTransactions(t *testing.T) {
	dsn := os.Getenv("HATAMOTO_TEST_DB_URL")
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if dsn == "" {
		t.Skip("HATAMOTO_TEST_DB_URL not set")
	}
	pooled := Start(t, dsn, 1)
	ctx := context.Background()

	// Default pgx mode prepares statements: only works with max_prepared_statements > 0.
	c1, err := pgx.Connect(ctx, pooled)
	if err != nil {
		t.Fatal(err)
	}
	defer c1.Close(ctx)
	c2, err := pgx.Connect(ctx, pooled)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close(ctx)

	pid := func(c *pgx.Conn) (p int) {
		t.Helper()
		if err := c.QueryRow(ctx, "SELECT pg_backend_pid() WHERE $1::int = 1", 1).Scan(&p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	// Two clients, one server connection: transaction pooling is in effect.
	if a, b := pid(c1), pid(c2); a != b {
		t.Fatalf("pool size 1 but backend pids differ: %d vs %d", a, b)
	}
}
