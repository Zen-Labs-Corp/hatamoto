//go:build integration

package migrations_test

import (
	"context"
	"testing"
)

// The traffic tables exist with exactly the aggregate columns and the settings
// singleton row is seeded enabled.
func TestTrafficDailyTables(t *testing.T) {
	ctx := context.Background()
	db, _ := scratchDB(t)
	if _, err := provider(t, db).Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	cols := func(table string) string {
		var s string
		if err := db.QueryRowContext(ctx, `SELECT string_agg(column_name, ',' ORDER BY column_name)
			FROM information_schema.columns WHERE table_schema = 'public' AND table_name = $1`, table).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	if got, want := cols("hatamoto_traffic_daily"), "day,dim,engaged,form_seen,form_started,k1,k2,multi,pageviews,scrolled,visits"; got != want {
		t.Errorf("traffic columns = %q, want %q", got, want)
	}
	if got, want := cols("hatamoto_analytics_settings"), "engagement_enabled,id"; got != want {
		t.Errorf("settings columns = %q, want %q", got, want)
	}
	var on bool
	if err := db.QueryRowContext(ctx, `SELECT engagement_enabled FROM hatamoto_analytics_settings`).Scan(&on); err != nil || !on {
		t.Errorf("engagement_enabled = %v, err %v; want true", on, err)
	}
}
