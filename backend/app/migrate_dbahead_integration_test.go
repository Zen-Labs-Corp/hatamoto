//go:build integration

package app

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"hatamoto.dev/core/migrations"
)

// A newer release may have migrated the database further than this binary
// knows: goose_db_version holds versions with no embedded source, both past
// the newest one and between two embedded ones. Rolling back to an older
// binary (the n-1 guarantee, docs/UPGRADES.md) relies on migrate being a
// no-op and serve booting anyway.
func TestMigrateDBAhead_Integration(t *testing.T) {
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
	var before int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM goose_db_version`).Scan(&before); err != nil {
		t.Fatal(err)
	}

	// 202610171207 sits between 202610171205 and 202610171210 and has no file.
	const future, missingMiddle int64 = 209901010000, 202610171207
	entries, err := migrations.FS.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "202610171207_") || strings.HasPrefix(e.Name(), "209901010000_") {
			t.Fatalf("version already has a source file: %s", e.Name())
		}
	}
	for _, v := range []int64{future, missingMiddle} {
		if _, err := pool.Exec(ctx, `INSERT INTO goose_db_version (version_id, is_applied) VALUES ($1, true)`, v); err != nil {
			t.Fatal(err)
		}
	}

	var buf bytes.Buffer
	if err := migrateUp(ctx, pool, slog.New(slog.NewTextHandler(&buf, nil))); err != nil {
		t.Fatalf("migrate on a database ahead of the binary: %v", err)
	}
	if !strings.Contains(buf.String(), "database schema is up to date") {
		t.Fatalf("migrate did not report an up-to-date schema:\n%s", buf.String())
	}
	if strings.Contains(buf.String(), "migration applied") {
		t.Fatalf("migrate applied something on a database ahead:\n%s", buf.String())
	}
	var after int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM goose_db_version`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before+2 {
		t.Fatalf("goose_db_version rows = %d, want %d", after, before+2)
	}

	var out bytes.Buffer
	if err := migrateStatus(ctx, pool, &out); err != nil {
		t.Fatalf("migrate status with extra versions: %v", err)
	}

	base := startServe(t, dsn)
	if code, body, _ := get(t, base+"/ready"); code != 200 {
		t.Fatalf("GET /ready = %d\n%s", code, body)
	}
	if code, body, _ := get(t, base+"/"); code != 200 {
		t.Fatalf("GET / = %d\n%s", code, body)
	}
}
