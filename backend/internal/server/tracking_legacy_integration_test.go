//go:build integration

package server

import (
	"context"
	"testing"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/packages"
	"hatamoto.dev/core/internal/tags"
)

// legacyPlugin is one old tracking plugin row as an upgrading site has it.
type legacyPlugin struct {
	name, settings string
	state          string // "" is installed
	disabled       bool
	grants         string // postgres array literal of the granted permissions
}

func (r *legacyRig) insertTracking(t *testing.T, p legacyPlugin) {
	t.Helper()
	ctx := context.Background()
	pool := r.s.db.Pool()
	fp, err := packages.ParsePublicKey(config.FirstPartyKey)
	if err != nil {
		t.Fatal(err)
	}
	state := p.state
	if state == "" {
		state = packages.StateInstalled
	}
	if _, err := pool.Exec(ctx, `INSERT INTO hatamoto_packages (kind, name, version, manifest, data, sha256, signature, signer_key_id, source, enabled, state)
		VALUES ('plugin', $1, '0.1.0', jsonb_build_object('name', $1::text, 'version', '0.1.0'), '\x00', 'x', '\x00', $2, 'registry', $3, $4)`,
		p.name, packages.KeyID(fp), !p.disabled, state); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO hatamoto_plugin_settings (plugin, settings) VALUES ($1, $2::jsonb)`, p.name, p.settings); err != nil {
		t.Fatal(err)
	}
	if p.grants != "" {
		if _, err := pool.Exec(ctx, `INSERT INTO hatamoto_plugin_grants (plugin, permissions) VALUES ($1, $2::text[])`, p.name, p.grants); err != nil {
			t.Fatal(err)
		}
	}
}

func trackingRig(t *testing.T) *legacyRig {
	t.Helper()
	r := newLegacyRig(t)
	r.s.tagsStore = tags.NewStore(r.s.db.Pool())
	return r
}

func TestTrackingLegacyPluginsAreFoldedAndUninstalled(t *testing.T) {
	r := trackingRig(t)
	ctx := context.Background()
	r.insertTracking(t, legacyPlugin{name: "gtm", settings: `{"container_id":" gtm-abc1234 "}`, grants: "{site.scripts}"})
	r.insertTracking(t, legacyPlugin{name: "google-analytics", settings: `{"measurement_id":"G-ABC123XYZ9"}`, grants: "{site.scripts}"})

	r.s.retireTrackingPlugins(ctx)

	set, err := r.s.tagsStore.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if g := set.Vendors["gtm"]; !g.Enabled || g.ID != "GTM-ABC1234" {
		t.Errorf("gtm = %+v", g)
	}
	// google-analytics was never trusted with scripts, so it never emitted.
	if g := set.Vendors["ga4"]; g.Enabled || g.ID != "G-ABC123XYZ9" {
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

// A tracker that was not running before the upgrade is copied off: the owner
// never had it sending data and the upgrade must not start it.
func TestTrackingLegacyPluginThatWasNotRunningIsCopiedOff(t *testing.T) {
	cases := map[string]legacyPlugin{
		"disabled":         {disabled: true, grants: "{site.scripts}"},
		"uninstalling":     {state: packages.StateUninstalling, grants: "{site.scripts}"},
		"no scripts grant": {grants: "{kv}"},
		"no grants":        {},
	}
	for name, p := range cases {
		t.Run(name, func(t *testing.T) {
			r := trackingRig(t)
			ctx := context.Background()
			p.name, p.settings = "gtm", `{"container_id":"GTM-ABC1234"}`
			r.insertTracking(t, p)

			r.s.retireTrackingPlugins(ctx)

			set, err := r.s.tagsStore.Get(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if g := set.Vendors["gtm"]; g.Enabled || g.ID != "GTM-ABC1234" {
				t.Errorf("gtm = %+v, want the id copied and the tag off", g)
			}
		})
	}
}
