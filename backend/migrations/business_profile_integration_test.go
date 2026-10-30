//go:build integration

package migrations_test

import (
	"context"
	"testing"
)

// New columns default to an empty object so the existing config row and an
// older binary's inserts stay valid.
func TestBusinessProfile_Defaults(t *testing.T) {
	db, _ := multiLocaleDB(t)
	var profile, locales string
	if err := db.QueryRowContext(context.Background(),
		`SELECT business_profile::text, locale_profiles::text FROM hatamoto_site_config`).Scan(&profile, &locales); err != nil {
		t.Fatal(err)
	}
	if profile != "{}" || locales != "{}" {
		t.Errorf("defaults = %q %q, want {} {}", profile, locales)
	}
}

// businessProfileVersion is 202610301616_business_profile; Down rolls back to
// just below it.
const businessProfileVersion = 202610301616

// Down drops both columns.
func TestBusinessProfile_Down(t *testing.T) {
	ctx := context.Background()
	db, _ := multiLocaleDB(t)
	if _, err := provider(t, db).DownTo(ctx, businessProfileVersion-1); err != nil {
		t.Fatalf("down: %v", err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.columns
		WHERE table_name = 'hatamoto_site_config' AND column_name IN ('business_profile', 'locale_profiles')`).Scan(&n); err != nil || n != 0 {
		t.Errorf("columns left = %d (%v)", n, err)
	}
}
