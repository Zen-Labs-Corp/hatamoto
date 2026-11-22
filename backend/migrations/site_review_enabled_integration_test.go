//go:build integration

package migrations_test

import (
	"context"
	"database/sql"
	"testing"
)

const siteReviewEnabledVersion = 202611241000

func reviewEnabled(t *testing.T, db *sql.DB) bool {
	t.Helper()
	var on bool
	if err := db.QueryRowContext(context.Background(),
		`SELECT review_enabled FROM hatamoto_site_config WHERE id = TRUE`).Scan(&on); err != nil {
		t.Fatalf("read review_enabled: %v", err)
	}
	return on
}

// The column appears false on the site row, Down drops it and Up again
// is clean.
func TestSiteReviewEnabled_UpDown(t *testing.T) {
	ctx := context.Background()
	db, _ := scratchDB(t)
	p := provider(t, db)
	if _, err := p.UpTo(ctx, siteReviewEnabledVersion-1); err != nil {
		t.Fatalf("up to previous: %v", err)
	}
	if _, err := p.UpTo(ctx, siteReviewEnabledVersion); err != nil {
		t.Fatalf("up: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO hatamoto_site_config (id, company_name) VALUES (TRUE, 'Test') ON CONFLICT DO NOTHING`); err != nil {
		t.Fatalf("seed site config: %v", err)
	}
	if reviewEnabled(t, db) {
		t.Fatal("review_enabled is true by default")
	}
	if _, err := p.DownTo(ctx, siteReviewEnabledVersion-1); err != nil {
		t.Fatalf("down: %v", err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.columns
		WHERE table_name = 'hatamoto_site_config' AND column_name = 'review_enabled'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("column after down: n=%d err=%v", n, err)
	}
	if _, err := p.UpTo(ctx, siteReviewEnabledVersion); err != nil {
		t.Fatalf("up again: %v", err)
	}
	if reviewEnabled(t, db) {
		t.Fatal("review_enabled is true after down and up")
	}
}
