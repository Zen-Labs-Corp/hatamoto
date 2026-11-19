//go:build integration

package pluginhost

import (
	"context"
	"strings"
	"testing"
)

func TestPluginDBHugeRowIsBounded_Integration(t *testing.T) {
	r := newPGRig(t)
	ctx := context.Background()
	if err := r.db.Provision(ctx, "capa"); err != nil {
		t.Fatal(err)
	}
	migs, err := ReadMigrations(fsOf(map[string]string{
		"migrations/001_init.sql": "-- +goose Up\nCREATE TABLE blobs (id serial PRIMARY KEY, body text);\n-- +goose Down\nDROP TABLE blobs;\n",
	}), "migrations")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Migrate(ctx, "capa", migs); err != nil {
		t.Fatal(err)
	}
	stillUsable := func(step string) {
		t.Helper()
		if _, err := r.db.Query(ctx, "capa", `SELECT 1`); err != nil {
			t.Fatalf("%s: the plugin must keep working: %v", step, err)
		}
	}

	// Over the result cap but under the message cap: truncated, no rows.
	res, err := r.db.Query(ctx, "capa", `SELECT repeat('x', 2097152)`)
	if err != nil || !res.Truncated || len(res.Rows) != 0 {
		t.Fatalf("2 MiB row: %+v %v", res, err)
	}
	stillUsable("2 MiB row")

	// Over the message cap: the call fails.
	if _, err := r.db.Query(ctx, "capa", `SELECT repeat('x', 8388608)`); err == nil || !strings.Contains(err.Error(), "invalid body length") {
		t.Fatalf("8 MiB row: %v", err)
	}
	stillUsable("8 MiB row")

	if _, err := r.db.Exec(ctx, "capa", `INSERT INTO blobs (body) VALUES ('a') RETURNING repeat('x', 8388608)`); err == nil {
		t.Fatal("an 8 MiB RETURNING row must fail")
	}
	stillUsable("8 MiB RETURNING row")

	// Ordinary results are unchanged.
	res, err = r.db.Query(ctx, "capa", `SELECT g FROM generate_series(1, 1500) g`)
	if err != nil || !res.Truncated || len(res.Rows) != maxDBRows {
		t.Fatalf("row cap: %+v %v", res, err)
	}
	res, err = r.db.Query(ctx, "capa", `SELECT repeat('y', 100000)`)
	if err != nil || res.Truncated || len(res.Rows) != 1 || len(res.Rows[0][0].(string)) != 100000 {
		t.Fatalf("100 KB row: truncated=%v err=%v", res != nil && res.Truncated, err)
	}
}
