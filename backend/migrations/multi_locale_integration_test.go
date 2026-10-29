//go:build integration

package migrations_test

import (
	"context"
	"database/sql"
	"testing"
)

// multiLocaleDB is a migrated scratch database with one flat schema and a site
// config in language "en", holding entities of en, fr and the shared "und".
func multiLocaleDB(t *testing.T) (*sql.DB, func(q string, args ...any)) {
	t.Helper()
	ctx := context.Background()
	db, flat := entityLocaleDB(t)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO hatamoto_site_config (company_name, language) VALUES ('Example', 'en')`)
	exec(`INSERT INTO hatamoto_entities (schema_id, name, slug, locale) VALUES
		($1, 'a', 'a', 'en'), ($1, 'b', 'b', 'fr'), ($1, 's', 's', 'und')`, flat)
	return db, exec
}

func localesOf(t *testing.T, db *sql.DB) map[string]string {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), `SELECT slug, locale FROM hatamoto_entities`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var s, l string
		if err := rows.Scan(&s, &l); err != nil {
			t.Fatal(err)
		}
		out[s] = l
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// With an empty locale list a language change re-tags as before, and shared
// ("und") rows are never touched.
func TestMultiLocale_RetagWhenListEmpty(t *testing.T) {
	db, exec := multiLocaleDB(t)
	exec(`UPDATE hatamoto_site_config SET language = 'fr'`)
	got := localesOf(t, db)
	if got["a"] != "fr" || got["b"] != "fr" || got["s"] != "und" {
		t.Errorf("locales = %v, want a=fr b=fr s=und", got)
	}
}

// A one-entry list is still a single-locale install (Multi() needs two): the
// language change re-tags.
func TestMultiLocale_RetagWhenListHasOneEntry(t *testing.T) {
	db, exec := multiLocaleDB(t)
	exec(`UPDATE hatamoto_site_config SET locales = '[{"tag":"en","segment":""}]'::jsonb`)
	exec(`UPDATE hatamoto_site_config SET language = 'fr'`)
	got := localesOf(t, db)
	if got["a"] != "fr" || got["b"] != "fr" || got["s"] != "und" {
		t.Errorf("locales = %v, want a=fr b=fr s=und", got)
	}
}

// With two or more entries a language change leaves every entity as stored.
func TestMultiLocale_NoRetagWhenListSet(t *testing.T) {
	db, exec := multiLocaleDB(t)
	exec(`UPDATE hatamoto_site_config SET locales = '[{"tag":"en","segment":"en"},{"tag":"fr","segment":"fr"}]'::jsonb`)
	exec(`UPDATE hatamoto_site_config SET language = 'fr'`)
	got := localesOf(t, db)
	if got["a"] != "en" || got["b"] != "fr" || got["s"] != "und" {
		t.Errorf("locales = %v, want a=en b=fr s=und", got)
	}
}

// New columns default so an older binary's inserts and the existing config row
// are valid, and a legacy insert with an explicit column list still works.
func TestMultiLocale_Defaults(t *testing.T) {
	ctx := context.Background()
	db, exec := multiLocaleDB(t)
	var locales, xdef string
	var prefix bool
	if err := db.QueryRowContext(ctx, `SELECT locales::text, prefix_all, x_default_locale FROM hatamoto_site_config`).Scan(&locales, &prefix, &xdef); err != nil {
		t.Fatal(err)
	}
	if locales != "[]" || prefix || xdef != "" {
		t.Errorf("defaults = %q %v %q", locales, prefix, xdef)
	}
	var schema string
	if err := db.QueryRowContext(ctx, `SELECT id::text FROM hatamoto_entity_schemas LIMIT 1`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO hatamoto_entities (schema_id, name, slug) VALUES ($1, 'old', 'old')`, schema)
	var group sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT translation_group::text FROM hatamoto_entities WHERE slug = 'old'`).Scan(&group); err != nil || group.Valid {
		t.Errorf("translation_group = %v (%v), want NULL", group, err)
	}
	var idx int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM pg_indexes WHERE indexname = 'idx_entities_translation_group' AND indexdef NOT LIKE '%UNIQUE%'`).Scan(&idx); err != nil || idx != 1 {
		t.Errorf("translation_group index count = %d (%v), want 1 non-unique", idx, err)
	}
}

// multiLocaleVersion is 202610291600_multi_locale; later migrations exist, so
// the Down test rolls back to just below it instead of using Down.
const multiLocaleVersion = 202610291600

// Down restores the old trigger body and drops the columns.
func TestMultiLocale_Down(t *testing.T) {
	ctx := context.Background()
	db, exec := multiLocaleDB(t)
	if _, err := provider(t, db).DownTo(ctx, multiLocaleVersion-1); err != nil {
		t.Fatalf("down: %v", err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.columns
		WHERE (table_name = 'hatamoto_site_config' AND column_name IN ('locales', 'prefix_all', 'x_default_locale'))
		   OR (table_name = 'hatamoto_entities' AND column_name = 'translation_group')`).Scan(&n); err != nil || n != 0 {
		t.Errorf("columns left = %d (%v)", n, err)
	}
	exec(`UPDATE hatamoto_site_config SET language = 'fr'`)
	if got := localesOf(t, db); got["a"] != "fr" {
		t.Errorf("old trigger body not restored: %v", got)
	}
}
