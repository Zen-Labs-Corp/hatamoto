//go:build integration

package database_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/testutil/pgbouncertest"
)

// Through a one-connection transaction pooler every request shares the same
// server session, so any session state set for one request is visible to the
// next. None must be left behind.
func TestPooler_NoUserStateLeaks(t *testing.T) {
	dsn := os.Getenv("HATAMOTO_TEST_DB_URL")
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if dsn == "" {
		t.Skip("HATAMOTO_TEST_DB_URL not set")
	}
	direct := pgbouncertest.ScratchDB(t, dsn)
	pooled := pgbouncertest.Start(t, direct, 1)

	ctx := context.Background()
	db, err := database.New(ctx, config.DatabaseConfig{
		URL: pooled, DirectURL: direct, MaxConnections: 4,
		MaxConnLifetime: time.Minute, MaxConnIdleTime: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)

	userCtx := database.WithUser(ctx, uuid.New())
	conn, err := db.GetConn(userCtx)
	if err != nil {
		t.Fatal(err)
	}
	conn.Release()

	err = db.WithTx(userCtx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "CREATE TEMP TABLE hm_pooler_probe (id int) ON COMMIT DROP")
		return err
	})
	if err != nil {
		t.Fatalf("WithTx through the pooler: %v", err)
	}

	var v *string
	err = db.WithTx(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, "SELECT current_setting('app.current_user_id', true)").Scan(&v)
	})
	if err != nil {
		t.Fatal(err)
	}
	if v != nil && *v != "" {
		t.Fatalf("app.current_user_id leaked through the pooler: %q", *v)
	}

	// The direct source is a different endpoint: a session there is not shared.
	sess, rel, err := db.Direct().Session(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rel()
	if sess.Config().Port == db.Pool().Config().ConnConfig.Port {
		t.Fatal("direct session went through the pooler port")
	}
}
