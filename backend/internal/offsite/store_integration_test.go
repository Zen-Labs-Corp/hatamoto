//go:build integration

package offsite

import (
	"context"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Run with: DATABASE_URL=... go test -tags=integration ./internal/offsite/...
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
	ctx := context.Background()
	_, _ = pool.Exec(ctx, `DELETE FROM hatamoto_offsite_settings`)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM hatamoto_offsite_settings`)
		pool.Close()
	})
	return pool
}

func TestStoreRoundTripSealedAndResult(t *testing.T) {
	pool := newIntegrationPool(t)
	ctx := context.Background()
	st := NewStore(pool)
	if _, found, err := st.Get(ctx); found || err != nil {
		t.Fatalf("empty: found = %v, err = %v", found, err)
	}
	key := SecretKey("server-secret")
	sealed, err := SealSecret(key, "very-secret-value")
	if err != nil {
		t.Fatal(err)
	}
	set := valid()
	set.Prefix, set.Keep, set.Schedule = "hatamoto/", 4, Schedule{ScheduleWeekly, 5, 2}
	set.SecretSealed = sealed
	set.NextDueAt = time.Date(2026, 11, 8, 5, 0, 0, 0, time.UTC)
	if err := st.Save(ctx, set); err != nil {
		t.Fatal(err)
	}
	got, found, err := st.Get(ctx)
	if err != nil || !found {
		t.Fatalf("get: %v, %v", found, err)
	}
	if !got.Enabled || got.Bucket != "my-backups" || got.Prefix != "hatamoto/" || got.Keep != 4 ||
		got.Schedule != (Schedule{ScheduleWeekly, 5, 2}) || !got.NextDueAt.Equal(set.NextDueAt) || !got.Last.At.IsZero() {
		t.Fatalf("got %+v", got)
	}
	if s, ok := got.Secret(key); !ok || s != "very-secret-value" {
		t.Fatalf("secret = %q, %v", s, ok)
	}
	// The key is not stored in clear anywhere in the row.
	var clear bool
	if err := pool.QueryRow(ctx, `SELECT row(o.*)::text LIKE '%very-secret-value%' FROM hatamoto_offsite_settings o`).Scan(&clear); err != nil || clear {
		t.Fatalf("clear = %v, err = %v", clear, err)
	}

	res := Result{At: time.Date(2026, 11, 4, 3, 0, 5, 0, time.UTC), OK: false, Size: 123, SHA256: "ab", Error: "boom"}
	if err := st.SaveResult(ctx, res); err != nil {
		t.Fatal(err)
	}
	// A later Save keeps the last result.
	set.Keep = 5
	if err := st.Save(ctx, set); err != nil {
		t.Fatal(err)
	}
	got, _, _ = st.Get(ctx)
	if !got.Last.At.Equal(res.At) || got.Last.Error != "boom" || got.Last.Size != 123 || got.Keep != 5 {
		t.Fatalf("last = %+v keep = %d", got.Last, got.Keep)
	}
	if err := st.Clear(ctx); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := st.Get(ctx); found {
		t.Fatal("row survived Clear")
	}
}

func TestStoreClaimIsAtomic(t *testing.T) {
	pool := newIntegrationPool(t)
	ctx := context.Background()
	st := NewStore(pool)
	now := time.Date(2026, 11, 4, 3, 0, 30, 0, time.UTC)
	next := time.Date(2026, 11, 5, 3, 0, 0, 0, time.UTC)

	if ok, err := st.Claim(ctx, now, next); ok || err != nil {
		t.Fatalf("claim without a row = %v, %v", ok, err)
	}
	set := valid()
	set.Schedule = Schedule{ScheduleDaily, 3, 0}
	set.NextDueAt = now.Add(-time.Minute)
	if err := st.Save(ctx, set); err != nil {
		t.Fatal(err)
	}
	if ok, err := st.Claim(ctx, now.Add(-time.Hour), next); ok || err != nil {
		t.Fatalf("claim before due = %v, %v", ok, err)
	}

	// Two claimers racing for one slot: exactly one wins.
	var wins atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ok, err := st.Claim(ctx, now, next); err != nil {
				t.Error(err)
			} else if ok {
				wins.Add(1)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("%d claimers won one slot", wins.Load())
	}
	got, _, _ := st.Get(ctx)
	if !got.NextDueAt.Equal(next) {
		t.Fatalf("next_due_at = %v", got.NextDueAt)
	}
	if ok, _ := st.Claim(ctx, now, next); ok {
		t.Fatal("slot claimed twice")
	}

	// Defer moves the slot; a disabled or off schedule never claims.
	if err := st.Defer(ctx, now.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if ok, _ := st.Claim(ctx, now, next); ok {
		t.Fatal("claimed a deferred slot")
	}
	set.Enabled = false
	set.NextDueAt = now.Add(-time.Minute)
	if err := st.Save(ctx, set); err != nil {
		t.Fatal(err)
	}
	if ok, _ := st.Claim(ctx, now, next); ok {
		t.Fatal("claimed while disabled")
	}
}
