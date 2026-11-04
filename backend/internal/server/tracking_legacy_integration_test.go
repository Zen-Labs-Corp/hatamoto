//go:build integration

package server

import (
	"context"
	"testing"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/packages"
	"hatamoto.dev/core/internal/tags"
)

func TestTrackingLegacyPluginsAreFoldedAndUninstalled(t *testing.T) {
	r := newLegacyRig(t)
	ctx := context.Background()
	pool := r.s.db.Pool()
	r.s.tagsStore = tags.NewStore(pool)
	fp, err := packages.ParsePublicKey(config.FirstPartyKey)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"gtm", "google-analytics"} {
		if _, err := pool.Exec(ctx, `INSERT INTO hatamoto_packages (kind, name, version, manifest, data, sha256, signature, signer_key_id, source)
			VALUES ('plugin', $1, '0.1.0', jsonb_build_object('name', $1::text, 'version', '0.1.0'), '\x00', 'x', '\x00', $2, 'registry')`, name, packages.KeyID(fp)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO hatamoto_plugin_settings (plugin, settings) VALUES
		('gtm', '{"container_id":" gtm-abc1234 "}'), ('google-analytics', '{"measurement_id":"G-ABC123XYZ9"}')`); err != nil {
		t.Fatal(err)
	}

	r.s.retireTrackingPlugins(ctx)

	set, err := r.s.tagsStore.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if g := set.Vendors["gtm"]; !g.Enabled || g.ID != "GTM-ABC1234" {
		t.Errorf("gtm = %+v", g)
	}
	if g := set.Vendors["ga4"]; !g.Enabled || g.ID != "G-ABC123XYZ9" {
		t.Errorf("ga4 = %+v", g)
	}
	if len(set.MovedFrom) != 2 || set.NoticeDismissed {
		t.Errorf("notice state = %+v", set)
	}
	if n := r.count(t, `SELECT count(*) FROM hatamoto_packages WHERE name IN ('gtm','google-analytics')`); n != 0 {
		t.Errorf("%d plugin rows left", n)
	}
	if n := r.count(t, `SELECT count(*) FROM hatamoto_admin_audit WHERE action = 'tracking.import_plugin'`); n != 2 {
		t.Errorf("audit rows = %d, want 2", n)
	}
	// A second boot changes nothing.
	r.s.retireTrackingPlugins(ctx)
	if n := r.count(t, `SELECT count(*) FROM hatamoto_admin_audit WHERE action = 'tracking.import_plugin'`); n != 2 {
		t.Errorf("second boot audited again: %d", n)
	}
}
