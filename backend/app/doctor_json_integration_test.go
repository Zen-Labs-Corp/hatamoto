//go:build integration

package app

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func doctorPool(t *testing.T) (context.Context, *pgxpool.Pool, string) {
	t.Helper()
	ctx := context.Background()
	dsn := scratchDSN(t)
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrateUp(ctx, pool, quietLogger()); err != nil {
		t.Fatal(err)
	}
	return ctx, pool, dsn
}

// Every JSON column of a fully migrated database has a registry entry, so a
// new column cannot ship without the doctor knowing how to read it.
func TestDoctorRegistryCoversEveryJSONColumn_Integration(t *testing.T) {
	ctx, pool, _ := doctorPool(t)
	rows, err := pool.Query(ctx, `
		SELECT table_name || '.' || column_name
		  FROM information_schema.columns
		 WHERE table_schema = 'public' AND data_type IN ('json', 'jsonb')`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var found []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			t.Fatal(err)
		}
		found = append(found, k)
		if _, ok := doctorRegistry[k]; !ok {
			t.Errorf("JSON column %s is not in doctorRegistry (app/doctor_json.go): register it, opaque when it is free-form", k)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for k := range doctorRegistry {
		if !slices.Contains(found, k) {
			t.Errorf("doctorRegistry has %s but the migrated database has no such JSON column", k)
		}
	}
}

func TestDoctorJSON_Integration(t *testing.T) {
	ctx, pool, dsn := doctorPool(t)

	// A clean database passes.
	var out bytes.Buffer
	if err := runDoctorJSON(ctx, dsn, &out); err != nil {
		t.Fatalf("clean database: %v\n%s", err, out.String())
	}
	if !strings.HasSuffix(out.String(), "OK\n") || strings.Contains(out.String(), "unregistered") {
		t.Fatalf("clean report:\n%s", out.String())
	}

	if _, err := pool.Exec(ctx, `INSERT INTO hatamoto_site_config (company_name, theme_extra) VALUES ('Acme', '{"headercta":{"text":"a","url":"/"}}')`); err != nil {
		t.Fatal(err)
	}
	var formID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO hatamoto_forms (name, slug, fields)
		VALUES ('Contact', 'contact', '[{"id":"a","default_value":"x"}]') RETURNING id::text`).Scan(&formID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO hatamoto_forms (name, slug, fields) VALUES ('Bad', 'bad', '[{"id":"a","Default_Value":"x"}]')`); err != nil {
		t.Fatal(err)
	}
	var badID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM hatamoto_forms WHERE slug = 'bad'`).Scan(&badID); err != nil {
		t.Fatal(err)
	}

	snapshot := func() string {
		var s string
		err := pool.QueryRow(ctx, `
			SELECT (SELECT string_agg(theme_extra::text, '|') FROM hatamoto_site_config) || '#' ||
			       (SELECT string_agg(fields::text, '|' ORDER BY slug) FROM hatamoto_forms)`).Scan(&s)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	before := snapshot()

	out.Reset()
	err := runDoctorJSON(ctx, dsn, &out)
	var ee exitError
	if !errors.As(err, &ee) || ee.code != 1 {
		t.Fatalf("err = %v, want exitError{1}\n%s", err, out.String())
	}
	got := out.String()
	for _, want := range []string{
		"hatamoto_forms.fields id=" + badID + ": case-variant keys: /0/Default_Value -> default_value",
		"hatamoto_site_config.theme_extra id=true: case-variant keys: /headercta -> headerCta",
		"2 case-variant keys",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("report lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "id="+formID+":") {
		t.Errorf("clean row reported:\n%s", got)
	}
	if !strings.HasSuffix(got, "FAILED\n") {
		t.Errorf("report does not end in FAILED:\n%s", got)
	}

	if after := snapshot(); after != before {
		t.Errorf("doctor changed stored rows:\n before %s\n after  %s", before, after)
	}
}
