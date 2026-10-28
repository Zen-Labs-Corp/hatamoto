//go:build integration

package installstats

import (
	"context"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// scratchPool creates a throwaway database with only the install stats
// migration applied and drops it afterwards.
func scratchPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
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
	name := "hm_stats_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
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
	sqlText, err := os.ReadFile("../../migrations/202610280900_install_stats.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, _, _ := strings.Cut(strings.TrimPrefix(string(sqlText), "-- +goose Up"), "-- +goose Down")
	if _, err := pool.Exec(ctx, up); err != nil {
		t.Fatal(err)
	}
	return pool
}

func TestStoreMissingRowIsUnset_Integration(t *testing.T) {
	st := NewStore(scratchPool(t))
	got, err := st.Load(context.Background())
	if err != nil || got.Consent != ConsentUnset || got.InstallID != "" {
		t.Fatalf("missing row must be unset without an id: %+v %v", got, err)
	}
	if _, ok, err := st.Claim(context.Background()); err != nil || ok {
		t.Fatalf("claim with consent unset: ok=%v err=%v", ok, err)
	}
}

func TestStoreEnableDisableReset_Integration(t *testing.T) {
	ctx := context.Background()
	st := NewStore(scratchPool(t))

	// Reset before consent creates nothing.
	if got, err := st.Reset(ctx); err != nil || got.InstallID != "" {
		t.Fatalf("reset without consent: %+v %v", got, err)
	}
	first, err := st.Enable(ctx)
	if err != nil || first.Consent != ConsentYes || len(first.InstallID) != 32 {
		t.Fatalf("enable: %+v %v", first, err)
	}
	again, err := st.Enable(ctx)
	if err != nil || again.InstallID != first.InstallID {
		t.Fatalf("a second enable must keep the id: %+v %v", again, err)
	}
	reset, err := st.Reset(ctx)
	if err != nil || reset.InstallID == "" || reset.InstallID == first.InstallID || reset.Consent != ConsentYes {
		t.Fatalf("reset must give a different id: %+v %v", reset, err)
	}
	if err := st.Disable(ctx); err != nil {
		t.Fatal(err)
	}
	off, err := st.Load(ctx)
	if err != nil || off.Consent != ConsentNo || off.InstallID != "" {
		t.Fatalf("disable must clear the id: %+v %v", off, err)
	}
	if got, err := st.Reset(ctx); err != nil || got.InstallID != "" {
		t.Fatalf("reset with consent no: %+v %v", got, err)
	}
	// A new yes after no is a new id.
	back, err := st.Enable(ctx)
	if err != nil || back.InstallID == "" || back.InstallID == first.InstallID {
		t.Fatalf("enable after disable: %+v %v", back, err)
	}
}

func TestStoreClaim_Integration(t *testing.T) {
	ctx := context.Background()
	pool := scratchPool(t)
	st := NewStore(pool)
	enabled, err := st.Enable(ctx)
	if err != nil {
		t.Fatal(err)
	}

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		wins int
	)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, ok, err := st.Claim(ctx)
			if err != nil {
				t.Error(err)
				return
			}
			if ok {
				if id != enabled.InstallID {
					t.Errorf("claim returned %q", id)
				}
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("concurrent claims: %d winners, want 1", wins)
	}

	if _, err := pool.Exec(ctx, `UPDATE hatamoto_install_stats SET last_sent_at = now() - interval '21 hours'`); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := st.Claim(ctx); err != nil || !ok {
		t.Fatalf("claim after the interval: ok=%v err=%v", ok, err)
	}

	if err := st.RecordResult(ctx, "ok"); err != nil {
		t.Fatal(err)
	}
	got, _ := st.Load(ctx)
	if got.LastStatus != "ok" || got.LastSentAt == nil {
		t.Fatalf("result not stored: %+v", got)
	}

	if err := st.Disable(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := st.Claim(ctx); err != nil || ok {
		t.Fatalf("claim with consent no: ok=%v err=%v", ok, err)
	}
}
