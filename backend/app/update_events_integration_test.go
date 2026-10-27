//go:build integration

package app

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Real table, real migration: importing the same file twice leaves one row per event.
func TestImportUpdateEvents_Integration(t *testing.T) {
	dsn := scratchDSN(t)
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	log, _ := warnLog()
	if err := migrateUp(ctx, pool, log); err != nil {
		t.Fatal(err)
	}

	dir := eventsDir(t, evLine("i-1")+evLine("i-2")+
		`{"id":"i-3","from":"v1.0.1","to":"v1.0.2","phase":"rolled_back","error":"gate: crash loop","started_at":"2026-10-22T10:01:00Z","finished_at":"2026-10-22T10:01:09Z"}`+"\n")
	count := func() (n int) {
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM hatamoto_updates`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	for i := 0; i < 2; i++ {
		if err := importUpdateEvents(ctx, pool, dir, log); err != nil {
			t.Fatal(err)
		}
		if n := count(); n != 3 {
			t.Fatalf("import %d: %d rows, want 3", i+1, n)
		}
	}
	var from, to, phase, msg string
	if err := pool.QueryRow(ctx, `SELECT from_version, to_version, phase, error FROM hatamoto_updates WHERE event_id='i-3'`).
		Scan(&from, &to, &phase, &msg); err != nil {
		t.Fatal(err)
	}
	if from != "v1.0.1" || to != "v1.0.2" || phase != "rolled_back" || msg != "gate: crash loop" {
		t.Fatalf("row: %s %s %s %s", from, to, phase, msg)
	}
	// A later boot with one more event adds exactly one row.
	dir2 := eventsDir(t, evLine("i-1")+evLine("i-4"))
	if err := importUpdateEvents(ctx, pool, dir2, log); err != nil {
		t.Fatal(err)
	}
	if n := count(); n != 4 {
		t.Fatalf("%d rows, want 4", n)
	}
}
