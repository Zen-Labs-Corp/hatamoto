//go:build integration

package consent

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Run with: DATABASE_URL=... go test -tags=integration ./internal/consent/...
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
	t.Cleanup(pool.Close)
	return pool
}

func TestCounterOneRowPerDayAndChoice(t *testing.T) {
	pool := newIntegrationPool(t)
	ctx := context.Background()
	// A day far in the past keeps this test apart from real counts.
	day := time.Date(2001, 2, 3, 23, 59, 0, 0, time.UTC)
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM hatamoto_consent_daily WHERE day = '2001-02-03'`) })
	pool.Exec(ctx, `DELETE FROM hatamoto_consent_daily WHERE day = '2001-02-03'`)

	c := NewCounter(pool)
	for _, ch := range []string{"all", "all", "none"} {
		if err := c.Inc(ctx, day, ch); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.Inc(ctx, day, "bogus"); err == nil {
		t.Error("bogus choice accepted")
	}
	var rows, all int64
	if err := pool.QueryRow(ctx, `SELECT count(*), coalesce(sum(n) FILTER (WHERE choice='all'),0) FROM hatamoto_consent_daily WHERE day = '2001-02-03'`).Scan(&rows, &all); err != nil {
		t.Fatal(err)
	}
	if rows != 2 || all != 2 {
		t.Errorf("rows=%d all=%d, want 2 and 2", rows, all)
	}
}

func TestCounterTableHoldsNoVisitorColumns(t *testing.T) {
	pool := newIntegrationPool(t)
	rows, err := pool.Query(context.Background(), `SELECT column_name FROM information_schema.columns WHERE table_name = 'hatamoto_consent_daily' ORDER BY ordinal_position`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			t.Fatal(err)
		}
		cols = append(cols, c)
	}
	if len(cols) != 3 || cols[0] != "day" || cols[1] != "choice" || cols[2] != "n" {
		t.Errorf("columns = %v, want day, choice, n", cols)
	}
}

func TestStoreRoundTripAndDefault(t *testing.T) {
	pool := newIntegrationPool(t)
	ctx := context.Background()
	pool.Exec(ctx, `DELETE FROM hatamoto_privacy_settings`)
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM hatamoto_privacy_settings`) })

	st := NewStore(pool)
	got, err := st.Get(ctx)
	if err != nil || got.Enabled {
		t.Fatalf("default = %+v, %v", got, err)
	}
	set := Defaults()
	set.Enabled, set.Region, set.Categories = true, RegionEU, []string{"analytics"}
	if err := st.Save(ctx, set); err != nil {
		t.Fatal(err)
	}
	got, err = st.Get(ctx)
	if err != nil || !got.Enabled || got.Region != RegionEU || len(got.Categories) != 1 {
		t.Fatalf("saved = %+v, %v", got, err)
	}
	bad := set
	bad.Region = "x"
	if err := st.Save(ctx, bad); err == nil {
		t.Error("invalid settings saved")
	}
}
