//go:build integration

package migrations_test

import (
	"context"
	"testing"
)

// The visitor_entry column is added with an empty-object default, so a row
// that existed before the migration reads as "off" and nothing else changes.
func TestVisitorEntry_ColumnDefaultsToEmptyObject(t *testing.T) {
	ctx := context.Background()
	db, _ := multiLocaleDB(t)
	var got, locales string
	if err := db.QueryRowContext(ctx, `SELECT visitor_entry::text, locales::text FROM hatamoto_site_config`).Scan(&got, &locales); err != nil {
		t.Fatal(err)
	}
	if got != "{}" {
		t.Errorf("visitor_entry = %s, want {}", got)
	}
	if locales != "[]" {
		t.Errorf("locales = %s, want it untouched", locales)
	}
	if _, err := db.ExecContext(ctx, `UPDATE hatamoto_site_config SET visitor_entry = NULL`); err == nil {
		t.Error("visitor_entry accepted NULL, want NOT NULL")
	}
}
