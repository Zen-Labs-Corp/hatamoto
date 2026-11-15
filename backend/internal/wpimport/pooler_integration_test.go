//go:build integration

package wpimport_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/testutil/pgbouncertest"
	"hatamoto.dev/core/internal/wpimport"
)

func poolerDB(t *testing.T, poolSize int) *database.DB {
	t.Helper()
	dsn := os.Getenv("HATAMOTO_TEST_DB_URL")
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if dsn == "" {
		t.Skip("HATAMOTO_TEST_DB_URL not set")
	}
	direct := pgbouncertest.ScratchDB(t, dsn)
	pooled := pgbouncertest.Start(t, direct, poolSize)
	db, err := database.New(context.Background(), config.DatabaseConfig{
		URL: pooled, DirectURL: direct, MaxConnections: 4,
		MaxConnLifetime: time.Minute, MaxConnIdleTime: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	return db
}

// Behind a transaction pooler the import lock must still exclude a second
// import, and release must really free it.
func TestPooler_ImportLockExcludes(t *testing.T) {
	ctx := context.Background()
	db := poolerDB(t, 2)
	src := db.Direct()

	release, err := wpimport.AcquireImportLock(ctx, src)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wpimport.AcquireImportLock(ctx, src); !errors.Is(err, wpimport.ErrImportRunning) {
		t.Fatalf("second import while the first holds the lock: err=%v, want ErrImportRunning", err)
	}
	if running, err := wpimport.ImportRunning(ctx, src); err != nil || !running {
		t.Fatalf("ImportRunning = %v, %v; want true", running, err)
	}
	release()
	if running, err := wpimport.ImportRunning(ctx, src); err != nil || running {
		t.Fatalf("ImportRunning after release = %v, %v; want false", running, err)
	}
	rel2, err := wpimport.AcquireImportLock(ctx, src)
	if err != nil {
		t.Fatalf("lock after release: %v", err)
	}
	rel2()
}

// The harness has teeth: the old way (a session lock on a pooled connection)
// does not exclude. The lock lands on a server session that the next client
// is handed, where trying again is reentrant and succeeds.
func TestPooler_SessionLockOnPooledConnDoesNotExclude(t *testing.T) {
	ctx := context.Background()
	db := poolerDB(t, 2)
	const try = `SELECT pg_try_advisory_lock(42)`

	c1, err := db.Pool().Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c1.Release()
	c2, err := db.Pool().Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Release()

	var first, second bool
	if err := c1.QueryRow(ctx, try).Scan(&first); err != nil {
		t.Fatal(err)
	}
	if err := c2.QueryRow(ctx, try).Scan(&second); err != nil {
		t.Fatal(err)
	}
	if !first || !second {
		t.Fatalf("pooled session locks = %v, %v; expected both to be granted (the leak this change avoids)", first, second)
	}
}
