//go:build integration

package tags

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Run with: DATABASE_URL=... go test -tags=integration ./internal/tags/...
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

func TestStoreRoundTripAndInvalidate(t *testing.T) {
	pool := newIntegrationPool(t)
	ctx := context.Background()
	pool.Exec(ctx, `DELETE FROM hatamoto_tracking_settings`)
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM hatamoto_tracking_settings`) })

	st := NewStore(pool)
	got, err := st.Get(ctx)
	if err != nil || len(got.Enabled()) != 0 || !got.ExcludeSignedIn {
		t.Fatalf("default = %+v, %v", got, err)
	}
	set := Defaults()
	set.Vendors["ga4"] = VendorState{Enabled: true, ID: "g-abc123xyz9"}
	if err := st.Save(ctx, set); err != nil {
		t.Fatal(err)
	}
	got, err = st.Get(ctx)
	if err != nil || len(got.Enabled()) != 1 || got.Vendors["ga4"].ID != "G-ABC123XYZ9" {
		t.Fatalf("saved = %+v, %v", got, err)
	}

	// A write behind the store's back is seen only after Invalidate.
	if _, err := pool.Exec(ctx, `UPDATE hatamoto_tracking_settings SET settings = '{"vendors":{}}'`); err != nil {
		t.Fatal(err)
	}
	if got, _ = st.Get(ctx); len(got.Enabled()) != 1 {
		t.Error("cache not used")
	}
	st.Invalidate()
	if got, _ = st.Get(ctx); len(got.Enabled()) != 0 {
		t.Error("Invalidate did not reload")
	}

	bad := set
	bad.Vendors = map[string]VendorState{"ga4": {Enabled: true, ID: "nope"}}
	if err := st.Save(ctx, bad); err == nil {
		t.Error("invalid settings saved")
	}
}
