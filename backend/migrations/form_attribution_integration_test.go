//go:build integration

package migrations_test

import (
	"context"
	"strings"
	"testing"
)

// The attribution migration adds exactly the ten touch columns to submissions
// and the two funnel counters to the traffic rollup; nothing identifying.
func TestFormAttributionColumns(t *testing.T) {
	ctx := context.Background()
	db, _ := scratchDB(t)
	if _, err := provider(t, db).Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	cols := func(table, like string) string {
		var s string
		if err := db.QueryRowContext(ctx, `SELECT coalesce(string_agg(column_name, ',' ORDER BY column_name), '')
			FROM information_schema.columns WHERE table_schema = 'public' AND table_name = $1 AND column_name LIKE $2`,
			table, like).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	want := "first_campaign,first_channel,first_landing,first_medium,first_source"
	if got := cols("hatamoto_form_submissions", "first\\_%"); got != want {
		t.Errorf("first_* = %q, want %q", got, want)
	}
	want = strings.ReplaceAll(want, "first_", "last_")
	if got := cols("hatamoto_form_submissions", "last\\_%"); got != want {
		t.Errorf("last_* = %q, want %q", got, want)
	}
	if got, want := cols("hatamoto_traffic_daily", "form\\_%"), "form_seen,form_started"; got != want {
		t.Errorf("funnel columns = %q, want %q", got, want)
	}

	var nullable int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.columns
		WHERE table_name = 'hatamoto_form_submissions' AND (column_name LIKE 'first\_%' OR column_name LIKE 'last\_%')
		AND is_nullable = 'YES' AND data_type = 'text'`).Scan(&nullable); err != nil || nullable != 10 {
		t.Errorf("nullable text touch columns = %d, err %v; want 10", nullable, err)
	}
	var bad int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.columns
		WHERE table_name IN ('hatamoto_form_submissions','hatamoto_traffic_daily')
		AND (column_name LIKE 'first\_%' OR column_name LIKE 'last\_%' OR column_name LIKE 'form\_%')
		AND column_name ~ '(ip|user|agent|referrer|visitor|session|cookie)'`).Scan(&bad); err != nil || bad != 0 {
		t.Errorf("identifying column names = %d, err %v; want 0", bad, err)
	}
}
