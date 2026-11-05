//go:build integration

package migrations_test

import (
	"context"
	"database/sql"
	"testing"
)

// normaliseStoredJSONKeysVersion is 202611051000_normalise_stored_json_keys.
const normaliseStoredJSONKeysVersion = 202611051000

func execAll(t *testing.T, db *sql.DB, stmts ...string) {
	t.Helper()
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
}

// jsonIs fails unless the jsonb the query returns equals want.
func jsonIs(t *testing.T, db *sql.DB, query, want string) {
	t.Helper()
	var ok bool
	if err := db.QueryRow(`SELECT (`+query+`) = $1::jsonb`, want).Scan(&ok); err != nil {
		t.Fatal(err)
	}
	if !ok {
		var got string
		_ = db.QueryRow(`SELECT (` + query + `)::text`).Scan(&got)
		t.Fatalf("%s = %s, want %s", query, got, want)
	}
}

func xminOf(t *testing.T, db *sql.DB, table, where string) string {
	t.Helper()
	var x string
	if err := db.QueryRow(`SELECT xmin::text FROM ` + table + ` WHERE ` + where).Scan(&x); err != nil {
		t.Fatal(err)
	}
	return x
}

func TestNormaliseStoredJSONKeys(t *testing.T) {
	ctx := context.Background()
	db, _ := scratchDB(t)
	if _, err := provider(t, db).UpTo(ctx, normaliseStoredJSONKeysVersion-1); err != nil {
		t.Fatalf("up to previous: %v", err)
	}
	execAll(t, db,
		`INSERT INTO hatamoto_site_config (company_name, theme_extra) VALUES ('Example',
			'{"Colors":{"PRIMARY":"#fff","accent":"#000"},"typography":{"fontfamily":"Inter"},"headercta":{"Text":"Buy","url":"/x"},
			  "builder":{"AnyKey":{"Nested":1}},"socialLinks":{"Twitter":"x","twitter":"y"},"Custom":{"Keep":1},"num":1e400}')`,

		`INSERT INTO hatamoto_entity_schemas (name, slug, schema, ui_schema, settings) VALUES
			('Variant', 'variant', '{"additionalproperties":true}', '{"Variant":1}',
			 '{"Url_Pattern":"/x/{slug}","sitemap":{"Changefreq":"daily"},"overrides":[{"URL_PATTERN":"/y","attribute":"a"}],"aliases":["Keep"],"custom":{"Keep":1}}'),
			('Collide', 'collide', '{}', '{}',
			 '{"url_pattern":"/a","URL_PATTERN":"/b","Archive_Path":"/p","ARCHIVE_PATH":"/q"}'),
			('Clean', 'clean', '{}', '{}', '{"url_pattern":"/c/{slug}"}')`,

		`INSERT INTO hatamoto_forms (name, slug, fields, settings) VALUES
			('Variant', 'variant', '[{"id":"a","Default_Value":"x","Options":[{"LABEL":"L","value":"v"}],"attributes":{"Data-X":"1"}}]', '{"Submit_Button_Text":"Go"}'),
			('Clean', 'clean', '[{"id":"a","default_value":"x"}]', '{"submit_button_text":"Go"}'),
			('NoSettings', 'nosettings', '[]', NULL)`,
	)
	cleanSchemaXmin := xminOf(t, db, "hatamoto_entity_schemas", "slug = 'clean'")
	cleanFormXmin := xminOf(t, db, "hatamoto_forms", "slug = 'clean'")

	if _, err := provider(t, db).Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}

	jsonIs(t, db, `SELECT theme_extra FROM hatamoto_site_config`,
		`{"colors":{"primary":"#fff","accent":"#000"},"typography":{"fontFamily":"Inter"},"headerCta":{"text":"Buy","url":"/x"},
		  "builder":{"AnyKey":{"Nested":1}},"socialLinks":{"Twitter":"x","twitter":"y"},"Custom":{"Keep":1},"num":1e400}`)

	jsonIs(t, db, `SELECT settings FROM hatamoto_entity_schemas WHERE slug = 'variant'`,
		`{"url_pattern":"/x/{slug}","sitemap":{"changefreq":"daily"},"overrides":[{"url_pattern":"/y","attribute":"a"}],"aliases":["Keep"],"custom":{"Keep":1}}`)
	// The canonical spelling wins a collision, else the smallest variant.
	jsonIs(t, db, `SELECT settings FROM hatamoto_entity_schemas WHERE slug = 'collide'`,
		`{"url_pattern":"/a","archive_path":"/q"}`)

	jsonIs(t, db, `SELECT fields FROM hatamoto_forms WHERE slug = 'variant'`,
		`[{"id":"a","default_value":"x","options":[{"label":"L","value":"v"}],"attributes":{"Data-X":"1"}}]`)
	jsonIs(t, db, `SELECT settings FROM hatamoto_forms WHERE slug = 'variant'`, `{"submit_button_text":"Go"}`)

	// Rows that were already canonical, and NULL, are not written.
	if got := xminOf(t, db, "hatamoto_entity_schemas", "slug = 'clean'"); got != cleanSchemaXmin {
		t.Errorf("clean schema row was rewritten (xmin %s -> %s)", cleanSchemaXmin, got)
	}
	if got := xminOf(t, db, "hatamoto_forms", "slug = 'clean'"); got != cleanFormXmin {
		t.Errorf("clean form row was rewritten (xmin %s -> %s)", cleanFormXmin, got)
	}
	var null bool
	if err := db.QueryRow(`SELECT settings IS NULL FROM hatamoto_forms WHERE slug = 'nosettings'`).Scan(&null); err != nil || !null {
		t.Errorf("NULL settings changed: %v %v", null, err)
	}

	// No other column is touched.
	jsonIs(t, db, `SELECT schema FROM hatamoto_entity_schemas WHERE slug = 'variant'`, `{"additionalproperties":true}`)
	jsonIs(t, db, `SELECT ui_schema FROM hatamoto_entity_schemas WHERE slug = 'variant'`, `{"Variant":1}`)

	// Idempotent: running Up again is a no-op, and so is running the
	// migration itself a second time (roll its record back, then Up).
	if _, err := provider(t, db).Up(ctx); err != nil {
		t.Fatalf("second up: %v", err)
	}
	rows := func() map[string]string {
		m := map[string]string{}
		for _, q := range []struct{ name, table, where string }{
			{"site", "hatamoto_site_config", "true"},
			{"schema variant", "hatamoto_entity_schemas", "slug = 'variant'"},
			{"schema collide", "hatamoto_entity_schemas", "slug = 'collide'"},
			{"form variant", "hatamoto_forms", "slug = 'variant'"},
		} {
			m[q.name] = xminOf(t, db, q.table, q.where)
		}
		return m
	}
	before := rows()
	if _, err := provider(t, db).DownTo(ctx, normaliseStoredJSONKeysVersion-1); err != nil {
		t.Fatalf("down: %v", err)
	}
	var applied int
	if err := db.QueryRow(`SELECT count(*) FROM goose_db_version WHERE version_id = $1 AND is_applied`, normaliseStoredJSONKeysVersion).Scan(&applied); err != nil || applied != 0 {
		t.Fatalf("migration still recorded as applied (%d, %v)", applied, err)
	}
	if _, err := provider(t, db).Up(ctx); err != nil {
		t.Fatalf("re-run: %v", err)
	}
	after := rows()
	for k, v := range before {
		if after[k] != v {
			t.Errorf("%s rewritten by the second run (xmin %s -> %s)", k, v, after[k])
		}
	}
}

// A database without any of the typed rows migrates.
func TestNormaliseStoredJSONKeysEmpty(t *testing.T) {
	db, _ := scratchDB(t)
	if _, err := provider(t, db).Up(context.Background()); err != nil {
		t.Fatalf("up: %v", err)
	}
}
