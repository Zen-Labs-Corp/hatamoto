//go:build integration

package migrations_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"hatamoto.dev/core/internal/entity"
)

const entityLocaleVersion = 202610261200

// entitySnapshot is what the public URLs are built from: schema, slug, parent
// and the resolved path of a flat pattern.
func entitySnapshot(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), `
		SELECT e.schema_id::text || '|' || e.slug || '|' || COALESCE(e.parent_id::text, '') || '|' ||
		       replace(COALESCE(NULLIF(s.settings->>'url_pattern', ''), '/{slug}'), '{slug}', e.slug)
		  FROM hatamoto_entities e JOIN hatamoto_entity_schemas s ON s.id = e.schema_id
		 ORDER BY e.id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func sqlState(err error) (code, constraint string) {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		return pe.Code, pe.ConstraintName
	}
	return "", ""
}

// The prod-like snapshot keeps every slug, parent and URL, and every entity is
// tagged with the site language.
func TestEntityLocale_ProdSnapshot(t *testing.T) {
	ctx := context.Background()
	db, _ := scratchDB(t)
	p := provider(t, db)
	if _, err := p.UpTo(ctx, killLocalesVersion-1); err != nil {
		t.Fatal(err)
	}
	seed, err := os.ReadFile("testdata/prodlike_pre_v1_09.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, string(seed)); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := p.UpTo(ctx, entityLocaleVersion-1); err != nil {
		t.Fatal(err)
	}
	before := entitySnapshot(t, db)
	if len(before) == 0 {
		t.Fatal("the seed has no entities")
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	after := entitySnapshot(t, db)
	if strings.Join(before, "\n") != strings.Join(after, "\n") {
		t.Errorf("slugs, parents or URLs changed:\nbefore %v\nafter  %v", before, after)
	}
	var site string
	if err := db.QueryRowContext(ctx, `SELECT language FROM hatamoto_site_config`).Scan(&site); err != nil {
		t.Fatal(err)
	}
	if site != "en-AU" {
		t.Fatalf("site language = %q, want en-AU", site)
	}
	var other int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM hatamoto_entities WHERE locale IS DISTINCT FROM $1`, site).Scan(&other); err != nil || other != 0 {
		t.Errorf("entities not in the site language: %d (%v)", other, err)
	}
}

func entityLocaleDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	ctx := context.Background()
	db, _ := scratchDB(t)
	if _, err := provider(t, db).Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	var schema string
	if err := db.QueryRowContext(ctx, `INSERT INTO hatamoto_entity_schemas (name, slug, schema, settings)
		VALUES ('F', 'flat', '{"type":"object"}', '{}') RETURNING id::text`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	return db, schema
}

// The unique indexes keep their names and now scope by locale; the column is
// metadata-only (no rewrite).
func TestEntityLocale_IndexNamesAndScope(t *testing.T) {
	ctx := context.Background()
	db, flat := entityLocaleDB(t)
	var nestedSchema string
	if err := db.QueryRowContext(ctx, `INSERT INTO hatamoto_entity_schemas (name, slug, schema, settings)
		VALUES ('N', 'nested', '{"type":"object"}', '{"url_pattern":"/{path}"}') RETURNING id::text`).Scan(&nestedSchema); err != nil {
		t.Fatal(err)
	}
	ins := func(schema, slug, locale, parent string) error {
		var par any
		if parent != "" {
			par = parent
		}
		_, err := db.ExecContext(ctx, `INSERT INTO hatamoto_entities (schema_id, name, slug, locale, parent_id) VALUES ($1, $2, $2, $3, $4)`, schema, slug, locale, par)
		return err
	}
	if err := ins(flat, "about", "en", ""); err != nil {
		t.Fatal(err)
	}
	if err := ins(flat, "about", "fr", ""); err != nil {
		t.Errorf("same slug in another locale: %v", err)
	}
	code, name := sqlState(ins(flat, "about", "en", ""))
	if code != "23505" || name != "uq_entities_schema_slug" {
		t.Errorf("flat clash = %s %s, want 23505 uq_entities_schema_slug", code, name)
	}
	var root string
	if err := db.QueryRowContext(ctx, `INSERT INTO hatamoto_entities (schema_id, name, slug, locale) VALUES ($1, 'r', 'r', 'en') RETURNING id::text`, nestedSchema).Scan(&root); err != nil {
		t.Fatal(err)
	}
	if err := ins(nestedSchema, "kid", "en", root); err != nil {
		t.Fatal(err)
	}
	if err := ins(nestedSchema, "kid", "fr", root); err != nil {
		t.Errorf("nested same slug in another locale: %v", err)
	}
	code, name = sqlState(ins(nestedSchema, "kid", "en", root))
	if code != "23505" || name != "uq_entities_schema_parent_slug" {
		t.Errorf("nested clash = %s %s, want 23505 uq_entities_schema_parent_slug", code, name)
	}
	var missing bool
	if err := db.QueryRowContext(ctx, `SELECT atthasmissing FROM pg_attribute
		WHERE attrelid = 'public.hatamoto_entities'::regclass AND attname = 'locale'`).Scan(&missing); err != nil || !missing {
		t.Errorf("locale default is not metadata-only (atthasmissing=%v, %v)", missing, err)
	}
}

// An insert that names no locale (an old binary) gets the site language.
func TestEntityLocale_DefaultForOldBinaryInsert(t *testing.T) {
	ctx := context.Background()
	db, flat := entityLocaleDB(t)
	get := func(slug string) string {
		var l string
		if err := db.QueryRowContext(ctx, `SELECT locale FROM hatamoto_entities WHERE slug = $1`, slug).Scan(&l); err != nil {
			t.Fatal(err)
		}
		return l
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO hatamoto_entities (schema_id, name, slug) VALUES ($1, 'a', 'a')`, flat)
	if got := get("a"); got != "en" {
		t.Errorf("no site config row: locale = %q, want en", got)
	}
	exec(`INSERT INTO hatamoto_site_config (company_name, language) VALUES ('Example', 'fr-CA')`)
	exec(`INSERT INTO hatamoto_entities (schema_id, name, slug) VALUES ($1, 'b', 'b')`, flat)
	if got := get("b"); got != "fr-CA" {
		t.Errorf("locale = %q, want fr-CA", got)
	}
}

// Changing the site language re-tags the old language, keeps updated_at, leaves
// a colliding row as stored and never fails the settings save.
func TestEntityLocale_SiteLanguageRetag(t *testing.T) {
	ctx := context.Background()
	db, flat := entityLocaleDB(t)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO hatamoto_site_config (company_name, language) VALUES ('Example', 'en')`)
	exec(`INSERT INTO hatamoto_entities (schema_id, name, slug, locale, updated_at) VALUES
		($1, 'plain', 'plain', 'en', '2020-01-01T00:00:00Z'),
		($1, 'dup', 'dup', 'en', '2020-01-01T00:00:00Z'),
		($1, 'dup', 'dup', 'fr', '2020-01-01T00:00:00Z'),
		($1, 'other', 'other', 'fr', '2020-01-01T00:00:00Z')`, flat)

	// An unrelated settings update does not fire the trigger.
	exec(`UPDATE hatamoto_site_config SET tagline = 'x'`)
	exec(`UPDATE hatamoto_site_config SET language = 'fr'`)

	row := func(slug, locale string) (string, string) {
		t.Helper()
		var l, u string
		if err := db.QueryRowContext(ctx, `SELECT locale, updated_at::text FROM hatamoto_entities WHERE slug = $1 AND locale = $2`, slug, locale).Scan(&l, &u); err != nil {
			t.Fatalf("%s/%s: %v", slug, locale, err)
		}
		return l, u
	}
	if _, u := row("plain", "fr"); !strings.HasPrefix(u, "2020-01-01") {
		t.Errorf("re-tag touched updated_at: %s", u)
	}
	if _, u := row("dup", "en"); !strings.HasPrefix(u, "2020-01-01") {
		t.Errorf("colliding row changed: %s", u)
	}
	row("dup", "fr")
	row("other", "fr")
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM hatamoto_entities WHERE slug = 'dup'`).Scan(&n); err != nil || n != 2 {
		t.Errorf("dup rows = %d (%v), want 2", n, err)
	}
	var imp sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT current_setting('hatamoto.import', true)`).Scan(&imp); err != nil || imp.String == "on" {
		t.Errorf("hatamoto.import left on: %q (%v)", imp.String, err)
	}
}

const canonicalSiteLanguageVersion = 202610261210

// A site language kill_locales stored in a non-canonical form is rewritten, the
// retag trigger follows with the entities (updated_at kept), and a later
// create with the same legacy spelling stays public.
func TestCanonicalSiteLanguage_LegacyValue(t *testing.T) {
	ctx := context.Background()
	db, _ := scratchDB(t)
	p := provider(t, db)
	if _, err := p.UpTo(ctx, canonicalSiteLanguageVersion-1); err != nil {
		t.Fatal(err)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`DELETE FROM hatamoto_site_config`)
	exec(`INSERT INTO hatamoto_site_config (company_name, language) VALUES ('Acme', 'en-UK')`)
	var schema string
	if err := db.QueryRowContext(ctx, `INSERT INTO hatamoto_entity_schemas (name, slug, schema, settings)
		VALUES ('F', 'flat', '{"type":"object"}', '{}') RETURNING id::text`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO hatamoto_entities (schema_id, name, slug, status, locale, updated_at)
		VALUES ($1, 'a', 'a', 'published', 'en-UK', '2020-01-02 03:04:05+00'), ($1, 'b', 'b', 'published', 'en-UK', '2020-01-02 03:04:05+00')`, schema)
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	var site string
	var n, bad int
	if err := db.QueryRowContext(ctx, `SELECT language FROM hatamoto_site_config`).Scan(&site); err != nil || site != "en-GB" {
		t.Fatalf("site language = %q (%v), want en-GB", site, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*), count(*) FILTER (WHERE updated_at <> '2020-01-02 03:04:05+00') FROM hatamoto_entities WHERE locale = 'en-GB' AND status = 'published'`).Scan(&n, &bad); err != nil || n != 2 || bad != 0 {
		t.Errorf("retagged public entities = %d, updated_at changed on %d (%v)", n, bad, err)
	}
	canonical, err := entity.NormalizeLocale("en-UK")
	if err != nil || canonical != site {
		t.Fatalf("NormalizeLocale(en-UK) = %q, %v; site language %q", canonical, err, site)
	}
	exec(`INSERT INTO hatamoto_entities (schema_id, name, slug, status, locale) VALUES ($1, 'c', 'c', 'published', $2)`, schema, canonical)
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM hatamoto_entities WHERE locale = public.hatamoto_site_language()`).Scan(&n); err != nil || n != 3 {
		t.Errorf("public entities = %d (%v), want 3", n, err)
	}
}
