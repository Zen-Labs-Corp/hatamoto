//go:build integration

package seo

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Run with: DATABASE_URL=... go test -tags=integration ./internal/seo/...
func newIntegrationPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set — skipping integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
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

			// Keep the test's own row state: restore whatever was there.
			var prev []byte
			hadRow := pool.QueryRow(ctx, `SELECT settings FROM hatamoto_seo_settings WHERE id`).Scan(&prev) == nil
			t.Cleanup(func() {
				if hadRow {
					pool.Exec(ctx, `UPDATE hatamoto_seo_settings SET settings = $1::jsonb WHERE id`, string(prev))
				} else {
					pool.Exec(ctx, `DELETE FROM hatamoto_seo_settings WHERE id`)
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
			time.Sleep(200 * time.Millisecond) // let both reach the lock; order is irrelevant to the assertion
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
