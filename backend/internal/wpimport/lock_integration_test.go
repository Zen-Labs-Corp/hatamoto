//go:build integration

package wpimport

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestImportRunning_Integration(t *testing.T) {
	dsn := os.Getenv("HATAMOTO_TEST_DB_URL")
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if dsn == "" {
		t.Skip("HATAMOTO_TEST_DB_URL not set")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	name := "hm_lock_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(dsn)
	u.Path = "/" + name
	pool, err := pgxpool.New(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		_, _ = admin.Exec(ctx, "DROP DATABASE "+name+" WITH (FORCE)")
		admin.Close(ctx)
	})

	running := func() bool {
		t.Helper()
		r, err := ImportRunning(ctx, pool)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	if running() {
		t.Fatal("no import holds the lock, yet ImportRunning is true")
	}
	release, err := AcquireImportLock(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	if !running() {
		t.Fatal("an import holds the lock, yet ImportRunning is false")
	}
	release()
	if running() {
		t.Fatal("lock released, yet ImportRunning is true")
	}
	// The probe gives the lock back: a real import can still take it.
	rel2, err := AcquireImportLock(ctx, pool)
	if err != nil {
		t.Fatalf("probe leaked the lock: %v", err)
	}
	rel2()
}
