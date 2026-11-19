//go:build integration

package packages

import (
	"context"
	"errors"
	"slices"
	"testing"

	"hatamoto.dev/core/internal/pluginhost"
	"hatamoto.dev/sdk"
)

// filterRuntime is a PluginRuntime with fixed statuses that records which
// plugins were asked to rebuild their hooks.
type filterRuntime struct {
	PluginRuntime
	statuses []pluginhost.Status
	exports  map[string]bool
	rebuilt  []string
}

func (f *filterRuntime) Statuses() []pluginhost.Status { return f.statuses }
func (f *filterRuntime) ExportsEntityFilter(name string) bool {
	return f.exports[name]
}
func (f *filterRuntime) Reregister(name string) { f.rebuilt = append(f.rebuilt, name) }

func loadedStatus(name string, perms ...string) pluginhost.Status {
	return pluginhost.Status{Name: name, Loaded: true, Manifest: sdk.Manifest{Name: name, Permissions: perms}}
}

func TestGrandfatherEntityFilterOnce(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	rt := &filterRuntime{
		PluginRuntime: r.rt,
		statuses: []pluginhost.Status{
			loadedStatus("old", sdk.PermKV),
			loadedStatus("asks", sdk.PermEntitiesFilter),
			loadedStatus("quiet"),
			{Name: "off", Manifest: sdk.Manifest{Name: "off"}},
		},
		exports: map[string]bool{"old": true, "asks": true, "off": true},
	}
	r.mgr.Plugins = rt
	if err := r.mgr.Grants.Set(ctx, "old", []sdk.Permission{sdk.PermKV}); err != nil {
		t.Fatal(err)
	}

	r.mgr.grandfatherEntityFilter(ctx)

	if got := slices.Sorted(slices.Values(r.mgr.Grants.Granted("old"))); !slices.Equal(got, []sdk.Permission{sdk.PermEntitiesFilter, sdk.PermKV}) {
		t.Errorf("old grants = %v, want kv kept and entities.filter added", got)
	}
	for _, name := range []string{"asks", "quiet", "off"} {
		if r.mgr.Grants.Has(name, sdk.PermEntitiesFilter) {
			t.Errorf("%s must not be granted", name)
		}
	}
	if !slices.Equal(rt.rebuilt, []string{"old"}) {
		t.Errorf("rebuilt = %v, want [old]", rt.rebuilt)
	}
	var audits int
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM hatamoto_admin_audit WHERE action = 'package.grants' AND target = 'plugin:old'`).Scan(&audits); err != nil || audits != 1 {
		t.Errorf("audit rows = %d, %v", audits, err)
	}

	// The marker row is harmless to the readers of the grants table.
	var marker int
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM hatamoto_plugin_grants WHERE plugin = $1`, grandfatherEntityFilterKey).Scan(&marker); err != nil || marker != 1 {
		t.Fatalf("marker rows = %d, %v", marker, err)
	}
	if err := pluginhost.NewGrantStore(r.pool).Load(ctx); err != nil {
		t.Errorf("grant store load: %v", err)
	}
	if err := pluginhost.NewMailQuotaStore(r.pool).Load(ctx); err != nil {
		t.Errorf("mail quota load: %v", err)
	}

	// A plugin that shows up after the sweep is never granted, and a second
	// pass changes nothing.
	rt.statuses = append(rt.statuses, loadedStatus("late"))
	rt.exports["late"] = true
	rt.rebuilt = nil
	r.mgr.grandfatherEntityFilter(ctx)
	if r.mgr.Grants.Has("late", sdk.PermEntitiesFilter) || len(rt.rebuilt) != 0 {
		t.Errorf("a later plugin was granted: rebuilt %v", rt.rebuilt)
	}
}

func TestGrandfatherEntityFilterFailsClosed(t *testing.T) {
	r := newRig(t)
	rt := &filterRuntime{PluginRuntime: r.rt, statuses: []pluginhost.Status{loadedStatus("old")}, exports: map[string]bool{"old": true}}
	r.mgr.Plugins = rt

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	r.mgr.grandfatherEntityFilter(cancelled) // the marker cannot be claimed
	if r.mgr.Grants.Has("old", sdk.PermEntitiesFilter) {
		t.Fatal("nothing may be granted when the marker is not written")
	}

	r.mgr.grandfatherEntityFilter(context.Background()) // the next pass retries
	if !r.mgr.Grants.Has("old", sdk.PermEntitiesFilter) {
		t.Fatal("the retry did not keep the filter")
	}
}

func TestUpdateAskingEntitiesFilterNeedsConfirmation(t *testing.T) {
	r := newRig(t)
	if err := r.installPlugin(r.plugin("1.0.0"), []string{sdk.PermKV}, InstallRequest{}); err != nil {
		t.Fatal(err)
	}

	// An update that asks for nothing new goes through.
	if err := r.installPlugin(r.plugin("1.1.0"), []string{sdk.PermKV}, InstallRequest{}); err != nil {
		t.Fatalf("update asking nothing new: %v", err)
	}

	err := r.installPlugin(r.plugin("1.2.0"), []string{sdk.PermKV, sdk.PermEntitiesFilter}, InstallRequest{})
	var need *ConfirmationRequired
	if !errors.As(err, &need) || !need.WidenedAccess || !need.NewEntitiesFilter {
		t.Fatalf("update asking entities.filter: %v", err)
	}

	// With the owner's confirmation it installs; a later update that keeps asking is not widened.
	if err := r.installPlugin(r.plugin("1.2.0"), []string{sdk.PermKV, sdk.PermEntitiesFilter}, InstallRequest{Confirm: true}); err != nil {
		t.Fatalf("confirmed update: %v", err)
	}
	if err := r.installPlugin(r.plugin("1.3.0"), []string{sdk.PermKV, sdk.PermEntitiesFilter}, InstallRequest{}); err != nil {
		t.Fatalf("update keeping the permission: %v", err)
	}
}
