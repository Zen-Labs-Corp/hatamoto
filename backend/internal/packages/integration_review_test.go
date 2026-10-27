//go:build integration

package packages

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/sdk"
)

func (r *rig) install(raw func() ([]byte, error), req InstallRequest) (*Installed, error) {
	r.t.Helper()
	b, err := raw()
	if err != nil {
		r.t.Fatal(err)
	}
	return r.mgr.Install(context.Background(), b, req)
}

func (r *rig) pack(files map[string][]byte, key *Key) func() ([]byte, error) {
	return func() ([]byte, error) { return PackFiles(files, key) }
}

func TestDowngradeAndSignerChangeNeedTheOwnersConfirmation(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	second := newKey(t)
	r.mgr.Trust = trust(t, r.key, second)

	if _, err := r.install(r.pack(r.theme("2.0.0"), &r.key), InstallRequest{Source: SourceUpload}); err != nil {
		t.Fatal(err)
	}
	var need *ConfirmationRequired
	if _, err := r.install(r.pack(r.theme("1.0.0"), &r.key), InstallRequest{Source: SourceUpload}); !errors.As(err, &need) || !need.Downgrade {
		t.Fatalf("v1.0.0 over v2.0.0 must need confirmation, got %v", err)
	}
	if _, err := r.install(r.pack(r.theme("2.0.0"), &r.key), InstallRequest{Source: SourceUpload}); !errors.As(err, &need) || !need.Downgrade {
		t.Fatalf("the same version again must need confirmation, got %v", err)
	}
	if _, err := r.install(r.pack(r.theme("3.0.0"), &second), InstallRequest{Source: SourceUpload}); !errors.As(err, &need) || !need.SignerChange {
		t.Fatalf("another signer must need confirmation, got %v", err)
	}
	if row, _ := r.mgr.Store().Get(ctx, KindTheme, "sunrise"); row.Version != "2.0.0" {
		t.Fatalf("a refused replacement changed the stored version to %s", row.Version)
	}
	if _, err := r.install(r.pack(r.theme("1.0.0"), &r.key), InstallRequest{Source: SourceUpload, Confirm: true}); err != nil {
		t.Fatalf("confirmed downgrade: %v", err)
	}
	if row, _ := r.mgr.Store().Get(ctx, KindTheme, "sunrise"); row.Version != "1.0.0" {
		t.Fatalf("version = %s", row.Version)
	}
	var audited int
	_ = r.pool.QueryRow(ctx, `SELECT count(*) FROM hatamoto_admin_audit WHERE action = 'package.update' AND detail->>'confirmed_downgrade' = 'true'`).Scan(&audited)
	if audited != 1 {
		t.Fatalf("the confirmed downgrade was audited %d times", audited)
	}
}

func TestSignerChangeResetsThePluginsGrants(t *testing.T) {
	r := newRig(t)
	second := newKey(t)
	r.mgr.Trust = trust(t, r.key, second)
	if _, err := r.install(r.pack(r.plugin("1.0.0"), &r.key), InstallRequest{Source: SourceUpload, Grant: []string{sdk.PermKV, sdk.PermDB}}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.install(r.pack(r.plugin("2.0.0"), &second), InstallRequest{Source: SourceUpload, Confirm: true}); err != nil {
		t.Fatal(err)
	}
	if g := r.rt.Grants().Granted("capa"); len(g) != 0 {
		t.Fatalf("a new signer inherited grants: %v", g)
	}
}

func TestProductionIgnoresDevPackages(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	if _, err := r.install(r.pack(r.theme("1.0.0"), nil), InstallRequest{Source: SourceDev}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.install(r.pack(r.plugin("1.0.0"), nil), InstallRequest{Source: SourceDev, Grant: []string{sdk.PermKV}}); err != nil {
		t.Fatal(err)
	}
	if err := r.mgr.Activate(ctx, "sunrise", true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.page(), `id="sunrise"`) {
		t.Fatal("a dev theme must load in development")
	}

	// The same database, now served in production.
	prod := r.mgr
	prod.AllowDev = false
	prod.Reconcile(ctx)
	if prod.ActiveTheme() == "sunrise" {
		t.Fatal("production applied a dev theme found in the database")
	}
	if prod.ThemeError() == "" || prod.Blocked(KindTheme, "sunrise") == "" || prod.Blocked(KindPlugin, "capa") == "" {
		t.Fatalf("a blocked dev package must say why: %q", prod.ThemeError())
	}
	if len(r.rt.Statuses()) != 0 {
		t.Fatal("production loaded a dev plugin")
	}
	if err := prod.Activate(ctx, "sunrise", true); err == nil {
		t.Fatal("production activated a dev theme")
	}
	if _, err := prod.Install(ctx, mustPack(t, r.theme("1.0.1")), InstallRequest{Source: SourceDev}); !errors.Is(err, ErrDevDisabled) {
		t.Fatalf("production installed a dev package: %v", err)
	}
}

func TestARemovedTrustedKeyDisablesItsPackages(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	if _, err := r.install(r.pack(r.theme("1.0.0"), &r.key), InstallRequest{Source: SourceUpload}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.install(r.pack(r.plugin("1.0.0"), &r.key), InstallRequest{Source: SourceUpload, Grant: []string{sdk.PermKV}}); err != nil {
		t.Fatal(err)
	}
	if err := r.mgr.Activate(ctx, "sunrise", true); err != nil {
		t.Fatal(err)
	}
	r.mgr.Trust = TrustSet{} // the key was removed from HATAMOTO_TRUSTED_KEYS
	r.mgr.Reconcile(ctx)
	if strings.Contains(r.page(), `id="sunrise"`) || len(r.rt.Statuses()) != 0 {
		t.Fatal("packages signed by a removed key still load")
	}
	if !strings.Contains(r.mgr.Blocked(KindPlugin, "capa"), "no longer in HATAMOTO_TRUSTED_KEYS") || r.mgr.ThemeError() == "" {
		t.Fatalf("the reason must be visible: %q / %q", r.mgr.Blocked(KindPlugin, "capa"), r.mgr.ThemeError())
	}
}

func TestUpdatingTheActiveThemeRunsTheMissingSectionCheck(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	var schemaID uuid.UUID
	if err := r.pool.QueryRow(ctx, `SELECT id FROM hatamoto_entity_schemas LIMIT 1`).Scan(&schemaID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.pool.Exec(ctx, `INSERT INTO hatamoto_entities (schema_id, name, slug, status, attributes)
		VALUES ($1, 'p', 'p', 'published', '{"page_sections":[{"type":"promo"}]}')`, schemaID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.install(r.pack(r.theme("1.0.0"), &r.key), InstallRequest{Source: SourceUpload}); err != nil {
		t.Fatal(err)
	}
	if err := r.mgr.Activate(ctx, "sunrise", false); err != nil {
		t.Fatal(err)
	}
	v2 := r.theme("2.0.0")
	delete(v2, "sections/promo.html")
	delete(v2, "schemas/promo.json")
	var need *ConfirmationRequired
	if _, err := r.install(r.pack(v2, &r.key), InstallRequest{Source: SourceUpload}); !errors.As(err, &need) || len(need.Missing) != 1 || need.Missing[0].Type != "promo" {
		t.Fatalf("err = %v, want promo missing", err)
	}
	if row, _ := r.mgr.Store().Get(ctx, KindTheme, "sunrise"); row.Version != "1.0.0" {
		t.Fatal("a blocked update was stored")
	}
	if _, err := r.install(r.pack(v2, &r.key), InstallRequest{Source: SourceUpload, Confirm: true}); err != nil {
		t.Fatalf("confirmed: %v", err)
	}
}

// flakyDropper fails its first Drop.
type flakyDropper struct {
	DataDropper
	failed bool
}

func (d *flakyDropper) Drop(ctx context.Context, plugin string) error {
	if !d.failed {
		d.failed = true
		return fmt.Errorf("connection lost")
	}
	return d.DataDropper.Drop(ctx, plugin)
}

func TestAFailedUninstallCanBeRetried(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	if _, err := r.install(r.pack(r.plugin("1.0.0"), &r.key), InstallRequest{Source: SourceUpload, Grant: []string{sdk.PermKV, sdk.PermDB}}); err != nil {
		t.Fatal(err)
	}
	r.mgr.Dropper = &flakyDropper{DataDropper: r.mgr.Dropper}
	if err := r.mgr.Uninstall(ctx, KindPlugin, "capa", true); err == nil {
		t.Fatal("the first uninstall should fail")
	}
	row, err := r.mgr.Store().Get(ctx, KindPlugin, "capa")
	if err != nil || row.State != StateUninstalling {
		t.Fatalf("a failed uninstall must stay recoverable: %+v %v", row, err)
	}
	if len(r.rt.Statuses()) != 0 || len(r.rt.Grants().Granted("capa")) != 0 {
		t.Fatal("an uninstalling plugin must be unloaded and have no grants")
	}
	if _, err := r.install(r.pack(r.plugin("1.1.0"), &r.key), InstallRequest{Source: SourceUpload}); !errors.Is(err, ErrUninstalling) {
		t.Fatalf("installing over an unfinished uninstall: %v", err)
	}
	if err := r.mgr.Uninstall(ctx, KindPlugin, "capa", true); err != nil {
		t.Fatalf("retry: %v", err)
	}
	var gone bool
	_ = r.pool.QueryRow(ctx, `SELECT NOT EXISTS (SELECT 1 FROM hatamoto_packages) AND NOT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = $1)`, "plugin_capa").Scan(&gone)
	if !gone {
		t.Fatal("the retry left the row or the schema")
	}
}

func TestPackageChangesAreAudited(t *testing.T) {
	r := newRig(t)
	ctx := WithActor(context.Background(), Actor{Email: "owner@example.test"})
	if err := r.mgr.SetAllowUnsigned(ctx, true); err != nil {
		t.Fatal(err)
	}
	b := mustPack(t, r.plugin("1.0.0"))
	if _, err := r.mgr.Install(ctx, b, InstallRequest{Source: SourceUpload, Grant: []string{sdk.PermKV}}); err != nil {
		t.Fatal(err)
	}
	if err := r.mgr.Uninstall(ctx, KindPlugin, "capa", false); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"package.allow_unsigned", "package.install", "package.uninstall"} {
		var n int
		_ = r.pool.QueryRow(ctx, `SELECT count(*) FROM hatamoto_admin_audit WHERE action = $1 AND actor = 'owner@example.test'`, action).Scan(&n)
		if n != 1 {
			t.Errorf("%s audited %d times", action, n)
		}
	}
}

func TestThemeSettingsChangesReachOtherReplicas(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	if _, err := r.install(r.pack(r.theme("1.0.0"), &r.key), InstallRequest{Source: SourceUpload}); err != nil {
		t.Fatal(err)
	}
	if err := r.mgr.Activate(ctx, "sunrise", false); err != nil {
		t.Fatal(err)
	}
	cli := NewManager(Options{Pool: r.pool, Trust: r.mgr.Trust, AllowDev: true})
	if errs, err := cli.SaveThemeSettings(ctx, "sunrise", map[string]any{"accent": "violet"}); err != nil || len(errs) != 0 {
		t.Fatal(errs, err)
	}
	r.mgr.Reconcile(ctx)
	if !strings.Contains(r.page(), "accent=violet") {
		t.Fatalf("another replica did not pick up the settings:\n%s", r.page())
	}
}

func mustPack(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	b, err := PackFiles(files, nil)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// withManifest returns files with the plugin manifest changed by edit.
func withManifest(t *testing.T, files map[string][]byte, edit func(*sdk.Manifest)) map[string][]byte {
	t.Helper()
	var m sdk.Manifest
	if err := json.Unmarshal(files[PluginManifest], &m); err != nil {
		t.Fatal(err)
	}
	edit(&m)
	out := maps.Clone(files)
	out[PluginManifest] = mustJSON(m)
	return out
}

func TestWidenedHostsOrFetchAnyNeedConfirmationAndResetGrants(t *testing.T) {
	r := newRig(t)
	base := func(m *sdk.Manifest) {
		m.Permissions = append(m.Permissions, sdk.PermHTTPFetch)
		m.AllowedHosts = []string{"api.example.com"}
	}
	if _, err := r.install(r.pack(withManifest(t, r.plugin("1.0.0"), base), &r.key), InstallRequest{Source: SourceUpload, Grant: []string{sdk.PermKV, sdk.PermHTTPFetch}}); err != nil {
		t.Fatal(err)
	}
	// Same hosts, a newer version: no prompt, grants kept.
	if _, err := r.install(r.pack(withManifest(t, r.plugin("1.1.0"), base), &r.key), InstallRequest{Source: SourceUpload}); err != nil {
		t.Fatalf("a plain update must not prompt: %v", err)
	}
	if g := r.rt.Grants().Granted("capa"); len(g) != 2 {
		t.Fatalf("grants after a plain update: %v", g)
	}
	// A new host: confirmation, then the grants are gone.
	wider := withManifest(t, r.plugin("1.2.0"), func(m *sdk.Manifest) {
		base(m)
		m.AllowedHosts = append(m.AllowedHosts, "evil.example.net")
	})
	var need *ConfirmationRequired
	if _, err := r.install(r.pack(wider, &r.key), InstallRequest{Source: SourceUpload}); !errors.As(err, &need) || !need.WidenedAccess || len(need.NewHosts) != 1 {
		t.Fatalf("a new host needs confirmation, got %v", err)
	}
	if _, err := r.install(r.pack(wider, &r.key), InstallRequest{Source: SourceUpload, Confirm: true}); err != nil {
		t.Fatal(err)
	}
	if g := r.rt.Grants().Granted("capa"); len(g) != 0 {
		t.Fatalf("widened access kept grants: %v", g)
	}
	// Newly asking for http.fetch.any is reviewed the same way.
	any := withManifest(t, r.plugin("1.3.0"), func(m *sdk.Manifest) {
		base(m)
		m.AllowedHosts = []string{"api.example.com", "evil.example.net"}
		m.Permissions = append(m.Permissions, sdk.PermHTTPFetchAny)
	})
	if _, err := r.install(r.pack(any, &r.key), InstallRequest{Source: SourceUpload}); !errors.As(err, &need) || !need.NewFetchAny {
		t.Fatalf("http.fetch.any needs confirmation, got %v", err)
	}
}

// A theme that no longer compiles (it calls a template func that does not
// exist) is not applied at boot; that must be a boot fault, since nothing
// fails afterwards and the site silently serves the built-in theme.
func TestThemeThatFailsToApplyIsABootFault(t *testing.T) {
	r := newRig(t)
	// The install-time check would refuse this theme; skip it to reach the
	// boot path, where a new release's engine rejects an already stored theme.
	r.mgr.Engine = uncheckedEngine{r.eng}
	ctx := context.Background()
	files := r.theme("1.0.0")
	files["partials/footer.html"] = []byte(`{{define "footer"}}{{noSuchFunc .}}{{end}}`)
	if _, err := r.install(r.pack(files, &r.key), InstallRequest{Source: SourceUpload}); err != nil {
		t.Fatal(err)
	}
	if err := r.mgr.Store().SetActiveTheme(ctx, "sunrise"); err != nil {
		t.Fatal(err)
	}
	r.mgr.Reconcile(ctx)
	if r.mgr.ThemeError() == "" {
		t.Fatal("the theme was applied; the test needs one that fails")
	}
	if !slices.Contains(render.BootFaults(), "theme-not-applied:sunrise") {
		t.Fatalf("boot faults = %v", render.BootFaults())
	}
}

// uncheckedEngine accepts any theme in CheckTheme; SetTheme stays real.
type uncheckedEngine struct{ ThemeEngine }

func (uncheckedEngine) CheckTheme(...fs.FS) error { return nil }

func TestPluginNamedWebhooksIsReserved(t *testing.T) {
	r := newRig(t)
	m := sdk.Manifest{Name: ReservedPluginName, Version: "1.0.0", SDK: "^0.7", Permissions: []string{sdk.PermLog}}
	files := r.plugin("1.0.0")
	files[PluginManifest] = mustJSON(m)
	raw, err := PackFiles(files, &r.key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.mgr.Install(context.Background(), raw, InstallRequest{Source: SourceUpload}); !errors.Is(err, ErrReservedName) {
		t.Fatalf("err = %v, want ErrReservedName", err)
	}
	var n int
	if err := r.pool.QueryRow(context.Background(), `SELECT count(*) FROM hatamoto_packages`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("a refused package was stored: %d (%v)", n, err)
	}
}

// The reserved name only blocks installing: an old plugin of that name that is
// still installed (not first-party, so not moved into core) can be uninstalled.
func TestPluginNamedWebhooksCanStillBeUninstalled(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	if _, err := r.pool.Exec(ctx, `INSERT INTO hatamoto_packages (kind, name, version, manifest, data, sha256, source)
		VALUES ('plugin', 'webhooks', '0.1.0', '{"name":"webhooks","version":"0.1.0"}', '\x00', 'x', 'upload')`); err != nil {
		t.Fatal(err)
	}
	if err := r.mgr.Uninstall(ctx, KindPlugin, ReservedPluginName, true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	var n int
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM hatamoto_packages WHERE name = 'webhooks'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("package rows left: %d (%v)", n, err)
	}
}
