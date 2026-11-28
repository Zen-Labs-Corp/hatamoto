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
	if h, err := src.Host(ctx); err != nil || h != "" {
		t.Fatalf("host without a site row = %q, %v", h, err)
	}
	exec(`INSERT INTO hatamoto_site_config (company_name, public_base_url) VALUES ('Example', 'https://stored.example.com')`)
	if h, err := src.Host(ctx); err != nil || h != "https://stored.example.com" {
		t.Fatalf("stored host = %q, %v", h, err)
	}
	src.baseURL = " https://env.example.com "
	if h, err := src.Host(ctx); err != nil || h != "https://env.example.com" {
		t.Fatalf("env host = %q, %v", h, err)
	}
	if !src.WorkerOn() {
		t.Fatal("worker flag lost")
	}
}

func TestStatsSourcesFlags_Integration(t *testing.T) {
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
	src := statsSources{pool: pool, pkgs: packages.NewStore(pool), csp: true}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	type flagSet struct {
		comments, static, sso, forms, offsite, csp, wp bool
		locales                                        int
	}
	read := func() flagSet {
		t.Helper()
		f := src.Flags(ctx)
		for name, p := range map[string]*bool{"comments": f.CommentsOn, "static": f.StaticMode, "sso": f.SSOOn,
			"forms": f.FormsInUse, "offsite": f.OffsiteBackupsOn, "csp": f.CSPEnforced, "wp": f.WPImported} {
			if p == nil {
				t.Fatalf("flag %s is nil", name)
			}
		}
		if f.Locales == nil {
			t.Fatal("locales is nil")
		}
		return flagSet{*f.CommentsOn, *f.StaticMode, *f.SSOOn, *f.FormsInUse, *f.OffsiteBackupsOn, *f.CSPEnforced, *f.WPImported, *f.Locales}
	}

	if got, want := read(), (flagSet{csp: true}); got != want {
		t.Fatalf("fresh install flags = %+v, want %+v", got, want)
	}

	// Negative cases: an inactive form and an unfinished import do not count.
	exec(`INSERT INTO hatamoto_forms (name, slug, status) VALUES ('Old', 'old', 'inactive')`)
	exec(`INSERT INTO hatamoto_import_runs (source, source_ref, status) VALUES ('wordpress', 'https://example.com', 'scanned')`)
	if got := read(); got.forms || got.wp {
		t.Fatalf("inactive form / scanned import counted: %+v", got)
	}

	exec(`INSERT INTO hatamoto_comment_settings (id, enabled) VALUES (TRUE, TRUE) ON CONFLICT (id) DO UPDATE SET enabled = TRUE`)
	exec(`INSERT INTO hatamoto_static_hosting_settings (id, mode) VALUES (TRUE, 'static') ON CONFLICT (id) DO UPDATE SET mode = 'static'`)
	exec(`INSERT INTO hatamoto_signin_oidc (id, enabled) VALUES (TRUE, TRUE) ON CONFLICT (id) DO UPDATE SET enabled = TRUE`)
	exec(`INSERT INTO hatamoto_offsite_settings (id, enabled) VALUES (TRUE, TRUE) ON CONFLICT (id) DO UPDATE SET enabled = TRUE`)
	exec(`INSERT INTO hatamoto_forms (name, slug, status) VALUES ('Contact', 'contact', 'active')`)
	exec(`INSERT INTO hatamoto_import_runs (source, source_ref, status) VALUES ('wordpress', 'https://example.com', 'completed')`)
	exec(`INSERT INTO hatamoto_site_config (company_name, locales) SELECT 'Example', '[{},{},{},{}]'::jsonb WHERE NOT EXISTS (SELECT 1 FROM hatamoto_site_config)`)
	exec(`UPDATE hatamoto_site_config SET locales = '[{},{},{},{}]'::jsonb`)
	if got, want := read(), (flagSet{true, true, true, true, true, true, true, 4}); got != want {
		t.Fatalf("all-on flags = %+v, want %+v", got, want)
	}
}
