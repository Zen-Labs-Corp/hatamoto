//go:build integration

package app

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/jackc/pgx/v5/pgxpool"

	"hatamoto.dev/core/internal/modhost"
)

func TestModuleMigrations_Integration(t *testing.T) {
	t.Cleanup(modhost.Reset)
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, scratchDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrateUp(ctx, pool, quietLogger()); err != nil {
		t.Fatal(err)
	}
	count := func(table string) int {
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		return n
	}
	coreBefore := count("goose_db_version")

	modhost.RegisterMigrations("x", fstest.MapFS{
		"202611210000_init.sql": {Data: []byte("-- +goose Up\nCREATE TABLE module_x_things (id int);\n-- +goose Down\nDROP TABLE module_x_things;\n")},
	})
	modhost.RegisterMigrations("bad", fstest.MapFS{
		"202611210000_bad.sql": {Data: []byte("-- +goose Up\nTHIS IS NOT SQL;\n-- +goose Down\nSELECT 1;\n")},
	})
	for i := 0; i < 2; i++ { // the second run is a no-op
		migrateModules(ctx, pool, quietLogger())
	}
	if n := count(moduleTable("x")); n != 2 { // goose's bootstrap row + the migration
		t.Fatalf("module_x version rows = %d, want 2", n)
	}
	count("module_x_things")
	if got := count("goose_db_version"); got != coreBefore {
		t.Fatalf("core goose_db_version rows %d -> %d", coreBefore, got)
	}
	if modhost.MigrationErr("x") != nil {
		t.Fatalf("x err: %v", modhost.MigrationErr("x"))
	}
	if modhost.MigrationErr("bad") == nil {
		t.Fatal("broken module migration recorded no error")
	}

	var buf bytes.Buffer
	if err := migrateModulesStatusPool(ctx, pool, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "module x\n") || !strings.Contains(buf.String(), "202611210000_init.sql") {
		t.Fatalf("status output:\n%s", buf.String())
	}
}
