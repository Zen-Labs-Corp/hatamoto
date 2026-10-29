//go:build integration

package seo

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Run with: DATABASE_URL=... go test -tags=integration ./internal/seo/...
func newIntegrationPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set — skipping integration test")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("pgxpool.ParseConfig: %v", err)
	}
	// The advisory lock, the barrier, two waiters and the poll need a connection each.
	cfg.MaxConns = 8
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// seoRowAdvisoryKey serialises the integration tests that rewrite the shared
// singleton SEO settings row, so parallel package runs cannot interfere.
const seoRowAdvisoryKey = 7301012

// lockSEORow holds the session advisory lock for the rest of the test.
func lockSEORow(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, seoRowAdvisoryKey); err != nil {
		conn.Release()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := conn.Exec(ctx, `SELECT pg_advisory_unlock($1)`, seoRowAdvisoryKey); err != nil {
			t.Errorf("advisory unlock: %v", err)
			conn.Conn().Close(ctx) // closing the session drops the lock
		}
		conn.Release()
	})
}

// waitForSEOLockWaiters polls (10s deadline) until n sessions wait on a lock in
// a statement that touches the SEO settings table.
func waitForSEOLockWaiters(t *testing.T, pool *pgxpool.Pool, n int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var got int
		err := pool.QueryRow(context.Background(), `
			SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock'
			  AND query LIKE '%hatamoto_seo_settings%' AND pid <> pg_backend_pid()`).Scan(&got)
		if err != nil {
			t.Fatal(err)
		}
		if got >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d session(s) waiting on the SEO settings lock, want %d", got, n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestStoreMergeConcurrentSavesKeepBothTabs holds the row lock in a
// transaction so both merges are queued behind it, then releases it: whatever
// order they run in, each one's keys must survive.
func TestStoreMergeConcurrentSavesKeepBothTabs(t *testing.T) {
	for _, tc := range []struct {
		name    string
		missing bool
	}{{"existing row", false}, {"missing row", true}} {
		t.Run(tc.name, func(t *testing.T) {
			pool := newIntegrationPool(t)
			ctx := context.Background()
			st := NewStore(pool)
			lockSEORow(t, pool)

			// Keep the test's own row state: restore whatever was there.
			var prev []byte
			prevErr := pool.QueryRow(ctx, `SELECT settings FROM hatamoto_seo_settings WHERE id`).Scan(&prev)
			if prevErr != nil && !errors.Is(prevErr, pgx.ErrNoRows) {
				t.Fatal(prevErr)
			}
			hadRow := prevErr == nil
			t.Cleanup(func() {
				if hadRow {
					// Upsert: the row may be missing after the "missing row" case.
					_, err := pool.Exec(ctx, `
						INSERT INTO hatamoto_seo_settings (id, settings, updated_at) VALUES (TRUE, $1::jsonb, now())
						ON CONFLICT (id) DO UPDATE SET settings = EXCLUDED.settings`, string(prev))
					if err != nil {
						t.Errorf("restore seo settings row: %v", err)
					}
				} else if _, err := pool.Exec(ctx, `DELETE FROM hatamoto_seo_settings WHERE id`); err != nil {
					t.Errorf("remove seo settings row: %v", err)
				}
			})
			if tc.missing {
				if _, err := pool.Exec(ctx, `DELETE FROM hatamoto_seo_settings WHERE id`); err != nil {
					t.Fatal(err)
				}
			} else if err := st.Save(ctx, Defaults()); err != nil {
				t.Fatal(err)
			}

			// Barrier: a transaction holding the lock (or, with no row, a
			// transaction that has just created it) until both merges started.
			barrier, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err := st.MergeTx(ctx, barrier, func(s *Settings) { s.Home = "%title%" }); err != nil {
				barrier.Rollback(ctx)
				t.Fatal(err)
			}

			var wg sync.WaitGroup
			errs := make(chan error, 2)
			for _, key := range []string{"a", "b"} {
				wg.Add(1)
				go func() {
					defer wg.Done()
					errs <- st.Merge(ctx, func(s *Settings) { s.Variables[key] = "v-" + key })
				}()
			}
			waitForSEOLockWaiters(t, pool, 2) // both queue behind the barrier; their order is irrelevant to the assertion
			if err := barrier.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			wg.Wait()
			close(errs)
			for err := range errs {
				if err != nil {
					t.Fatal(err)
				}
			}

			got, err := st.Get(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if got.Home != "%title%" || got.Variables["a"] != "v-a" || got.Variables["b"] != "v-b" {
				t.Fatalf("lost update: home=%q variables=%v", got.Home, got.Variables)
			}
		})
	}
}
