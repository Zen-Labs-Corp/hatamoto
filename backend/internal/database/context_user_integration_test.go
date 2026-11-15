//go:build integration

package database

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// GetConn must not leave the request user behind as session state: behind a
// transaction-mode pooler it would leak to whoever gets the server conn next.
func TestGetConnLeavesNoUserSessionState(t *testing.T) {
	dsn := os.Getenv("HATAMOTO_TEST_DB_URL")
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if dsn == "" {
		t.Skip("HATAMOTO_TEST_DB_URL not set")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns = 1
	ctx := context.Background()
	db, err := NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	conn, err := db.GetConn(WithUser(ctx, uuid.New()))
	if err != nil {
		t.Fatal(err)
	}
	conn.Release()

	conn, err = db.GetConn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	var v *string
	if err := conn.QueryRow(ctx, "SELECT current_setting('app.current_user_id', true)").Scan(&v); err != nil {
		t.Fatal(err)
	}
	if v != nil && *v != "" {
		t.Fatalf("app.current_user_id leaked: %q", *v)
	}
}
