//go:build integration

package migrations_test

import (
	"context"
	"strings"
	"testing"
)

const pluginRunsVersion = 202611031100

// Rows the previous release left running are closed so the one-active index
// can be built; a second running row for one plugin is then refused, while
// finished rows and other plugins are not.
func TestPluginRunsMigration_OneActiveRun(t *testing.T) {
	ctx := context.Background()
	db, _ := scratchDB(t)
	p := provider(t, db)
	if _, err := p.UpTo(ctx, pluginRunsVersion-1); err != nil {
		t.Fatalf("up to previous: %v", err)
	}
	// Two legacy running rows for one plugin would break the index.
	for i := 0; i < 2; i++ {
		if _, err := db.ExecContext(ctx, `INSERT INTO hatamoto_runs (plugin) VALUES ('legacy')`); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	var running int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM hatamoto_runs WHERE state = 'running'`).Scan(&running); err != nil || running != 0 {
		t.Fatalf("legacy running rows = %d (%v), want 0", running, err)
	}
	var phase, name string
	if err := db.QueryRowContext(ctx, `INSERT INTO hatamoto_runs (plugin) VALUES ('a') RETURNING phase, name`).Scan(&phase, &name); err != nil {
		t.Fatal(err)
	}
	if phase != "fetch" || name != "" {
		t.Errorf("defaults phase=%q name=%q", phase, name)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO hatamoto_runs (plugin) VALUES ('a')`); err == nil || !strings.Contains(err.Error(), "hatamoto_runs_one_active") {
		t.Errorf("second running run for one plugin must fail, got %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO hatamoto_runs (plugin) VALUES ('b')`); err != nil {
		t.Errorf("another plugin must be allowed: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE hatamoto_runs SET state = 'finished' WHERE plugin = 'a'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO hatamoto_runs (plugin) VALUES ('a')`); err != nil {
		t.Errorf("a new run after the old one finished must be allowed: %v", err)
	}
}

// Proposals cascade with their run; uploads get an id.
func TestPluginRunsMigration_ProposalsAndUploads(t *testing.T) {
	ctx := context.Background()
	db, _ := scratchDB(t)
	if _, err := provider(t, db).Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	var runID string
	if err := db.QueryRowContext(ctx, `INSERT INTO hatamoto_runs (plugin) VALUES ('a') RETURNING id`).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO hatamoto_run_proposals (run_id, kind, payload) VALUES ($1, 'home_page', '{}')`, runID); err != nil {
		t.Fatal(err)
	}
	var id string
	if err := db.QueryRowContext(ctx, `INSERT INTO hatamoto_run_uploads (plugin, filename, size, blob_key) VALUES ('a', 'x.csv', 3, 'k') RETURNING id`).Scan(&id); err != nil || id == "" {
		t.Fatalf("upload insert: %q %v", id, err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM hatamoto_runs WHERE id = $1`, runID); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM hatamoto_run_proposals`).Scan(&n); err != nil || n != 0 {
		t.Errorf("proposals left after run delete: %d (%v)", n, err)
	}
}

// Down removes what this migration added and keeps the changeset tables; Up again works.
func TestPluginRunsMigration_Down(t *testing.T) {
	ctx := context.Background()
	db, _ := scratchDB(t)
	p := provider(t, db)
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	if _, err := p.DownTo(ctx, pluginRunsVersion-1); err != nil {
		t.Fatalf("down: %v", err)
	}
	for _, c := range []string{"item_count", "payload_bytes"} {
		if columnExists(t, db, "hatamoto_changesets", c) {
			t.Errorf("hatamoto_changesets.%s still exists after down", c)
		}
	}
	for _, c := range []string{"name", "phase", "cursor", "params", "hosts", "upload_id", "progress", "summary", "reason", "changeset_id", "steps"} {
		if columnExists(t, db, "hatamoto_runs", c) {
			t.Errorf("hatamoto_runs.%s still exists after down", c)
		}
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM pg_tables WHERE tablename IN ('hatamoto_run_proposals','hatamoto_run_uploads')`).Scan(&n); err != nil || n != 0 {
		t.Errorf("tables left: %d (%v)", n, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM pg_indexes WHERE indexname = 'hatamoto_runs_one_active'`).Scan(&n); err != nil || n != 0 {
		t.Errorf("index left: %d (%v)", n, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM pg_tables WHERE tablename IN ('hatamoto_runs','hatamoto_changesets','hatamoto_run_buffer')`).Scan(&n); err != nil || n != 3 {
		t.Errorf("changeset tables must remain: %d (%v)", n, err)
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("up again: %v", err)
	}
}

// A changeset the previous release left staged gets its running totals from
// its items, so the staging quota does not start from zero.
func TestPluginRunsMigration_ChangesetCountersBackfill(t *testing.T) {
	ctx := context.Background()
	db, _ := scratchDB(t)
	p := provider(t, db)
	if _, err := p.UpTo(ctx, pluginRunsVersion-1); err != nil {
		t.Fatalf("up to previous: %v", err)
	}
	var runID string
	if err := db.QueryRowContext(ctx, `INSERT INTO hatamoto_runs (plugin) VALUES ('a') RETURNING id`).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	var csID int64
	if err := db.QueryRowContext(ctx, `INSERT INTO hatamoto_changesets (run_id, plugin, expires_at) VALUES ($1, 'a', now() + interval '1 day') RETURNING id`, runID).Scan(&csID); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 2; i++ {
		if _, err := db.ExecContext(ctx, `INSERT INTO hatamoto_changeset_items (changeset_id, seq, kind, op, payload) VALUES ($1, $2, 'entity', 'upsert', '{"a":1}')`, csID, i); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	var n, b int64
	if err := db.QueryRowContext(ctx, `SELECT item_count, payload_bytes FROM hatamoto_changesets WHERE id = $1`, csID).Scan(&n, &b); err != nil || n != 2 || b <= 0 {
		t.Fatalf("counters = %d items, %d bytes (%v)", n, b, err)
	}
}
