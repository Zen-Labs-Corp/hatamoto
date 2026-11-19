//go:build integration

package packages

import (
	"context"
	"errors"
	"slices"
	"testing"

	"hatamoto.dev/core/internal/jsonx"
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

// Reregister records the rebuild and, like the runtime, lists the hook in the
// status afterwards.
func (f *filterRuntime) Reregister(name string) {
	f.rebuilt = append(f.rebuilt, name)
	for i := range f.statuses {
		if f.statuses[i].Name == name {
			f.statuses[i].Hooks = []string{sdk.EntityBeforeSave.Name}
		}
	}
}

func loadedStatus(name string, perms ...string) pluginhost.Status {
	return pluginhost.Status{Name: name, Loaded: true, Manifest: sdk.Manifest{Name: name, Permissions: perms}}
}

// legacyRow stores a plugin row the way an earlier release left it: no marker
// exists yet and the package is not run, only listed.
func legacyRow(t *testing.T, r *rig, name string, enabled bool) {
	t.Helper()
	if _, err := r.pool.Exec(context.Background(), `INSERT INTO hatamoto_packages (kind, name, version, manifest, data, sha256, source, enabled)
		VALUES ('plugin', $1, '1.0.0', '{}', '\x00', $1, 'upload', $2)`, name, enabled); err != nil {
		t.Fatal(err)
	}
}

func storedGrants(t *testing.T, r *rig, plugin string) []string {
	t.Helper()
	var got []string
	if err := r.pool.QueryRow(context.Background(), `SELECT permissions FROM hatamoto_plugin_grants WHERE plugin = $1`, plugin).Scan(&got); err != nil {
		return nil
	}
	slices.Sort(got)
	return got
}

func TestGrandfatherEntityFilterOnce(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	for name, enabled := range map[string]bool{"old": true, "asks": true, "quiet": true, "off": false} {
		legacyRow(t, r, name, enabled)
	}
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
	// A fresh manager stands for the first start of this release.
	r.mgr = NewManager(r.mgr.Options)

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

	// A plugin that shows up after the claim is never granted, and a second
	// pass changes nothing.
	legacyRow(t, r, "late", true)
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
	legacyRow(t, r, "old", true)
	rt := &filterRuntime{PluginRuntime: r.rt, statuses: []pluginhost.Status{loadedStatus("old")}, exports: map[string]bool{"old": true}}
	r.mgr.Plugins = rt
	r.mgr = NewManager(r.mgr.Options)

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

// A plugin that is installed but not running at the first start (disabled,
// blocked, failed) keeps the filter once it loads.
func TestEntityFilterKeptForDisabledPluginEnabledLater(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	legacyRow(t, r, "off", false)
	rt := &filterRuntime{PluginRuntime: r.rt, exports: map[string]bool{"off": true}}
	r.mgr.Plugins = rt
	r.mgr = NewManager(r.mgr.Options)

	r.mgr.grandfatherEntityFilter(ctx) // not loaded yet
	if r.mgr.Grants.Has("off", sdk.PermEntitiesFilter) {
		t.Fatal("granted before the plugin was loaded")
	}
	rt.statuses = []pluginhost.Status{loadedStatus("off")}
	r.mgr.grandfatherEntityFilter(ctx)
	if !r.mgr.Grants.Has("off", sdk.PermEntitiesFilter) {
		t.Fatal("the plugin enabled later lost the filter")
	}
}

// Grants stored for a plugin survive the keep even when this process's copy of
// them is empty (a failed load, another process's write).
func TestEntityFilterKeptUnionsStoredGrants(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	legacyRow(t, r, "old", true)
	if _, err := r.pool.Exec(ctx, `INSERT INTO hatamoto_plugin_grants (plugin, permissions) VALUES ('old', '{kv,mail.send}')`); err != nil {
		t.Fatal(err)
	}
	rt := &filterRuntime{PluginRuntime: r.rt, statuses: []pluginhost.Status{loadedStatus("old", sdk.PermKV)}, exports: map[string]bool{"old": true}}
	r.mgr.Plugins = rt
	r.mgr = NewManager(r.mgr.Options)

	r.mgr.grandfatherEntityFilter(ctx)

	want := []string{"entities.filter", "kv", "mail.send"}
	if got := storedGrants(t, r, "old"); !slices.Equal(got, want) {
		t.Fatalf("stored grants = %v, want %v", got, want)
	}
}

// A plugin installed after the snapshot, by the CLI before the server's first
// start of this release, is never granted the filter.
func TestEntityFilterNotKeptForInstallAfterSnapshot(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	cli := NewManager(Options{Pool: r.pool, Trust: trust(t, r.key), AllowDev: true})
	raw, err := PackFiles(r.plugin("1.0.0"), &r.key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cli.Install(ctx, raw, InstallRequest{Source: SourceUpload}); err != nil {
		t.Fatal(err)
	}
	rt := &filterRuntime{PluginRuntime: r.rt, statuses: []pluginhost.Status{loadedStatus("capa")}, exports: map[string]bool{"capa": true}}
	r.mgr.Plugins = rt
	r.mgr = NewManager(r.mgr.Options)

	r.mgr.grandfatherEntityFilter(ctx)
	if r.mgr.Grants.Has("capa", sdk.PermEntitiesFilter) {
		t.Fatal("a plugin installed after the snapshot was granted the filter")
	}
}

// An update has to ask: a plugin in the snapshot that is updated before it is
// loaded does not keep the filter.
func TestEntityFilterNotKeptForUpdateAfterSnapshot(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	if err := r.installPlugin(r.plugin("1.0.0"), []string{sdk.PermKV}, InstallRequest{}); err != nil {
		t.Fatal(err)
	}
	// Back to how an earlier release left it: no marker yet.
	if _, err := r.pool.Exec(ctx, `DELETE FROM hatamoto_plugin_grants WHERE plugin = $1`, grandfatherEntityFilterKey); err != nil {
		t.Fatal(err)
	}
	rt := &filterRuntime{PluginRuntime: r.rt, exports: map[string]bool{"capa": true}}
	opts := r.mgr.Options
	opts.Plugins = rt
	srv := NewManager(opts)
	srv.grandfatherEntityFilter(ctx) // first start: snapshot holds capa, which is not loaded

	cli := NewManager(Options{Pool: r.pool, Trust: trust(t, r.key), AllowDev: true})
	files := r.plugin("1.1.0")
	var man sdk.Manifest
	_ = jsonx.Unmarshal(files[PluginManifest], &man)
	man.Permissions = []string{sdk.PermKV}
	files[PluginManifest] = mustJSON(man)
	raw, err := PackFiles(files, &r.key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cli.Install(ctx, raw, InstallRequest{Source: SourceUpload}); err != nil {
		t.Fatal(err)
	}

	rt.statuses = []pluginhost.Status{loadedStatus("capa", sdk.PermKV)}
	srv.grandfatherEntityFilter(ctx)
	if srv.Grants.Has("capa", sdk.PermEntitiesFilter) {
		t.Fatal("an updated plugin kept the filter")
	}
}

// A plugin that holds the filter without asking may ship the recommended
// update that asks for it without a reset.
func TestEntityFilterKeptPluginMayStartAsking(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	if err := r.installPlugin(r.plugin("1.0.0"), []string{sdk.PermKV}, InstallRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.pool.Exec(ctx, `INSERT INTO hatamoto_plugin_grants (plugin, permissions) VALUES ('capa', '{kv,entities.filter}')
		ON CONFLICT (plugin) DO UPDATE SET permissions = EXCLUDED.permissions`); err != nil {
		t.Fatal(err)
	}
	if err := r.installPlugin(r.plugin("1.1.0"), []string{sdk.PermKV, sdk.PermEntitiesFilter}, InstallRequest{}); err != nil {
		t.Fatalf("update that asks for a permission it already holds: %v", err)
	}
	if got := storedGrants(t, r, "capa"); !slices.Equal(got, []string{"entities.filter", "kv"}) {
		t.Fatalf("grants after the update = %v", got)
	}
}

// Another replica may have made the grant: this one reads it and rebuilds the
// hooks whose registration no longer matches.
func TestEntityFilterHooksFollowStoredGrant(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	legacyRow(t, r, "old", true)
	rt := &filterRuntime{PluginRuntime: r.rt, exports: map[string]bool{"old": true}}
	r.mgr.Plugins = rt
	r.mgr = NewManager(r.mgr.Options)
	r.mgr.grandfatherEntityFilter(ctx) // claims a snapshot holding old; old is not loaded here

	// The other replica keeps the filter for old.
	if _, err := r.pool.Exec(ctx, `UPDATE hatamoto_plugin_grants SET permissions = '{}' WHERE plugin = $1`, grandfatherEntityFilterKey); err != nil {
		t.Fatal(err)
	}
	if _, err := r.pool.Exec(ctx, `INSERT INTO hatamoto_plugin_grants (plugin, permissions) VALUES ('old', '{entities.filter}')`); err != nil {
		t.Fatal(err)
	}
	rt.statuses = []pluginhost.Status{loadedStatus("old")}

	r.mgr.grandfatherEntityFilter(ctx)
	if !r.mgr.Grants.Has("old", sdk.PermEntitiesFilter) {
		t.Fatal("the stored grant was not read")
	}
	if !slices.Equal(rt.rebuilt, []string{"old"}) {
		t.Fatalf("rebuilt = %v, want [old]", rt.rebuilt)
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
