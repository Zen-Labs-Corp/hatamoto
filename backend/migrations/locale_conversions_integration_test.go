//go:build integration

package migrations_test

import (
	"context"
	"testing"
)

// localeConversionsVersion is 202610311000_locale_conversions; Down rolls back
// to just below it.
const localeConversionsVersion = 202610311000

func TestLocaleConversions_TablesAndCascade(t *testing.T) {
	ctx := context.Background()
	db, _ := scratchDB(t)
	if _, err := provider(t, db).Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	var id int64
	if err := db.QueryRowContext(ctx, `INSERT INTO hatamoto_locale_conversions
		(plan, report, settings_before, settings_after) VALUES ('{}', '{}', '{}', '{}') RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	var status, schemasBefore string
	if err := db.QueryRowContext(ctx, `SELECT status, schemas_before::text FROM hatamoto_locale_conversions WHERE id = $1`, id).
		Scan(&status, &schemasBefore); err != nil || status != "applied" || schemasBefore != "[]" {
		t.Errorf("defaults = %q %q (%v), want applied []", status, schemasBefore, err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE hatamoto_locale_conversions SET status = 'other' WHERE id = $1`, id); err == nil {
		t.Error("status outside applied/reverted accepted")
	}
	// No entity with this id exists: the row table has no foreign key to entities.
	if _, err := db.ExecContext(ctx, `INSERT INTO hatamoto_locale_conversion_rows
		(conversion_id, entity_id, before_locale, before_slug, after_locale, after_slug, after_updated_at)
		VALUES ($1, gen_random_uuid(), 'und', 'a', 'en', 'a', now())`, id); err != nil {
		t.Fatalf("insert row: %v", err)
	}
	var fks int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM pg_constraint
		WHERE contype = 'f' AND conrelid = 'hatamoto_locale_conversion_rows'::regclass
		  AND confrelid = 'hatamoto_entities'::regclass`).Scan(&fks); err != nil || fks != 0 {
		t.Errorf("foreign keys to entities = %d (%v), want 0", fks, err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM hatamoto_locale_conversions WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM hatamoto_locale_conversion_rows`).Scan(&n); err != nil || n != 0 {
		t.Errorf("rows after cascade = %d (%v), want 0", n, err)
	}
}

func TestLocaleConversions_Down(t *testing.T) {
	ctx := context.Background()
	db, _ := scratchDB(t)
	p := provider(t, db)
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	if _, err := p.DownTo(ctx, localeConversionsVersion-1); err != nil {
		t.Fatalf("down: %v", err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.tables
		WHERE table_name IN ('hatamoto_locale_conversions', 'hatamoto_locale_conversion_rows')`).Scan(&n); err != nil || n != 0 {
		t.Errorf("tables left = %d (%v)", n, err)
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("up again: %v", err)
	}
}
