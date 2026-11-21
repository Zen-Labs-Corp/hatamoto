//go:build integration

package modmail

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"hatamoto.dev/core/internal/emaillog"
	"hatamoto.dev/core/internal/modhost"
)

// Module mail leaves one email log row in a migrated Postgres:
//
//	HATAMOTO_TEST_DB_URL=... go test -tags integration ./internal/modmail
func TestToUserWritesEmailLogRowIntegration(t *testing.T) {
	dsn := os.Getenv("HATAMOTO_TEST_DB_URL")
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if dsn == "" {
		t.Skip("HATAMOTO_TEST_DB_URL not set: skipping (needs Postgres)")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	const module = "example_it"
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM hatamoto_email_log WHERE plugin = $1`, module) })

	svc, _, srv := newSink(t)
	svc.SetRecorder(emaillog.NewStore(pool))
	a := adapter(svc)
	if err := a.ToUser(ctx, module, uuid.NewString(), modhost.Message{Subject: "Hello", Text: "body text"}); err != nil {
		t.Fatal(err)
	}
	if len(srv.Messages()) != 1 {
		t.Fatalf("messages = %d, want 1", len(srv.Messages()))
	}

	var n int
	deadline := time.Now().Add(3 * time.Second)
	for {
		err := pool.QueryRow(ctx, `SELECT count(*) FROM hatamoto_email_log WHERE kind = 'module' AND plugin = $1 AND status = 'sent'`, module).Scan(&n)
		if err != nil {
			t.Fatal(err)
		}
		if n == 1 || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if n != 1 {
		t.Fatalf("email log rows = %d, want 1", n)
	}
}
