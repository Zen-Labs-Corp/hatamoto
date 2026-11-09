//go:build integration

package app

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"hatamoto.dev/core/internal/packages"
)

func TestStatsSources_Integration(t *testing.T) {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, scratchDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	log, _ := warnLog()
	if err := migrateUp(ctx, pool, log); err != nil {
		t.Fatal(err)
	}
	src := statsSources{pool: pool, pkgs: packages.NewStore(pool), worker: true}

	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	pkg := func(kind, name, version string, signer *string, state string) {
		sig := []byte(nil)
		if signer != nil {
			sig = []byte("sig")
		}
		exec(`INSERT INTO hatamoto_packages (kind, name, version, manifest, data, sha256, signature, signer_key_id, source, state)
			VALUES ($1, $2, $3, '{}', '\x00', 'x', $4, $5, 'upload', $6)`, kind, name, version, sig, signer, state)
	}
	first, other := packages.FirstPartyKeyIDs[len(packages.FirstPartyKeyIDs)-1], "0000000000000000"
	pkg("plugin", "searchconsole", "1.0.0", &first, packages.StateInstalled)
	pkg("plugin", "unsigned-one", "0.1.0", nil, packages.StateInstalled)
	pkg("plugin", "other-key", "0.2.0", &other, packages.StateInstalled)
	pkg("plugin", "going-away", "0.3.0", &first, packages.StateUninstalling)
	pkg("theme", "my-theme", "2.0.0", nil, packages.StateInstalled)

	plugins, err := src.Plugins(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, p := range plugins {
		got[p.Name] = p.FirstParty
	}
	if len(got) != 3 || !got["searchconsole"] || got["unsigned-one"] || got["other-key"] {
		t.Fatalf("plugins = %+v", plugins)
	}

	if th, err := src.Theme(ctx); err != nil || th.Name != "default" || th.Version == "" {
		t.Fatalf("default theme = %+v, %v", th, err)
	}
	exec(`UPDATE hatamoto_package_config SET active_theme = 'my-theme'`)
	if th, err := src.Theme(ctx); err != nil || th.Name != "my-theme" || th.Version != "2.0.0" {
		t.Fatalf("stored theme = %+v, %v", th, err)
	}

	if pg, err := src.PostgresMajor(ctx); err != nil || pg < 10 {
		t.Fatalf("postgres major = %d, %v", pg, err)
	}
	if start, err := src.InstallStart(ctx); err != nil || start.IsZero() || start.After(time.Now().Add(time.Minute)) {
		t.Fatalf("install start = %v, %v", start, err)
	}

	now := time.Now()
	ev := func(id, phase string, at time.Time) {
		exec(`INSERT INTO hatamoto_updates (event_id, phase, error, started_at) VALUES ($1, $2, 'secret text', $3)`, id, phase, at)
	}
	ev("a", "failed", now.Add(-2*time.Hour))
	ev("b", "rolled_back", now.Add(-time.Hour))
	ev("c", "rolled_back", now.Add(-48*time.Hour))
	ev("d", "success", now.Add(-time.Hour))
	ev("e", "manual_rollback", now.Add(-time.Hour))
	failed, rolled, err := src.UpdateCounts(ctx, now.Add(-24*time.Hour))
	if err != nil || failed != 1 || rolled != 1 {
		t.Fatalf("update counts = %d, %d, %v", failed, rolled, err)
	}
	if failed, rolled, _ = src.UpdateCounts(ctx, now.Add(-72*time.Hour)); failed != 1 || rolled != 2 {
		t.Fatalf("wider window = %d, %d", failed, rolled)
	}
	if !src.WorkerOn() {
		t.Fatal("worker flag lost")
	}
}
