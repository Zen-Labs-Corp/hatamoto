//go:build integration

package packages

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/sdk"
)

// commercialRig is a developer rig whose manager has a site plugin key and an
// entitlement the test flips.
type commercialRig struct {
	*devRig
	site     Key
	entitled atomic.Bool
}

func newCommercialRig(t *testing.T) *commercialRig {
	t.Helper()
	c := &commercialRig{devRig: newDevRig(t), site: newKey(t)}
	pub, err := c.site.PublicKey()
	if err != nil {
		t.Fatal(err)
	}
	c.entitled.Store(true)
	c.mgr.PluginKey = pub
	c.mgr.Entitled = func(context.Context, string) bool { return c.entitled.Load() }
	return c
}

func (c *commercialRig) pack(version string, key Key) []byte {
	return c.devRig.pack(version, key, func(m *sdk.Manifest) {
		m.License, m.SDK = sdk.LicenseCommercial, "^0.26"
	})
}

func TestCommercialLoadFollowsTheEntitlement(t *testing.T) {
	c := newCommercialRig(t)
	ctx := context.Background()
	got, err := c.mgr.Install(ctx, c.pack("1.0.0", c.site), InstallRequest{Source: SourceUpload, Grant: capaGrant})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status == nil || !got.Status.Loaded {
		t.Fatalf("status: %+v", got.Status)
	}

	c.entitled.Store(false)
	c.mgr.Reconcile(ctx)
	if c.loaded("capa") || !strings.Contains(c.mgr.Blocked(KindPlugin, "capa"), "not entitled") {
		t.Fatalf("not entitled: loaded=%v blocked=%q", c.loaded("capa"), c.mgr.Blocked(KindPlugin, "capa"))
	}
	if err := c.mgr.SetEnabled(ctx, "capa", true); !errors.Is(err, ErrCommercialNotEntitled) {
		t.Fatalf("enable while not entitled: %v", err)
	}

	c.entitled.Store(true)
	c.mgr.Reconcile(ctx)
	if !c.loaded("capa") || c.mgr.Blocked(KindPlugin, "capa") != "" {
		t.Fatalf("entitled again: loaded=%v blocked=%q", c.loaded("capa"), c.mgr.Blocked(KindPlugin, "capa"))
	}
}

func TestCommercialWithoutPluginKeyDoesNotLoad(t *testing.T) {
	c := newCommercialRig(t)
	ctx := context.Background()
	if _, err := c.mgr.Install(ctx, c.pack("1.0.0", c.site), InstallRequest{Source: SourceUpload, Grant: capaGrant}); err != nil {
		t.Fatal(err)
	}
	c.mgr.PluginKey = nil
	c.mgr.Reconcile(ctx)
	if c.loaded("capa") || !strings.Contains(c.mgr.Blocked(KindPlugin, "capa"), "not entitled") {
		t.Fatalf("no key: loaded=%v blocked=%q", c.loaded("capa"), c.mgr.Blocked(KindPlugin, "capa"))
	}
	if err := c.mgr.SetEnabled(ctx, "capa", true); !errors.Is(err, ErrCommercialNotEntitled) {
		t.Fatalf("enable without a key: %v", err)
	}
}

func TestCommercialIgnoresUnsignedAndDeveloperRoutes(t *testing.T) {
	c := newCommercialRig(t)
	ctx := context.Background()
	if err := c.mgr.Store().SetAllowUnsigned(ctx, true); err != nil {
		t.Fatal(err)
	}
	if err := c.mgr.SetAllowDeveloper(ctx, true); err != nil {
		t.Fatal(err)
	}
	// Unsigned commercial, with the owner's unsigned opt-in on.
	files := c.devRig.plugin("1.0.0")
	var man sdk.Manifest
	if err := jsonx.Unmarshal(files[PluginManifest], &man); err != nil {
		t.Fatal(err)
	}
	man.License, man.SDK = sdk.LicenseCommercial, "^0.26"
	files[PluginManifest] = mustJSON(man)
	if _, err := c.mgr.Install(ctx, mustPack(t, files), InstallRequest{Source: SourceUpload, Grant: capaGrant}); !errors.Is(err, ErrCommercialSigner) {
		t.Fatalf("unsigned with opt-in: %v", err)
	}
	// Signed by the developer-index key: refused, not granted as a developer package.
	raw := c.pack("1.0.0", c.dev)
	c.list(raw, c.dev)
	if _, err := c.mgr.Install(ctx, raw, InstallRequest{Source: SourceUpload, Grant: capaGrant}); !errors.Is(err, ErrCommercialSigner) {
		t.Fatalf("developer-listed key: %v", err)
	}
}

// A plugin installed before the license member existed may carry any value in
// plugin.json; it must still load, as a normal plugin.
func TestStoredPluginWithOtherLicenseValueStillLoads(t *testing.T) {
	for name, license := range map[string]string{"spdx id": `"MIT"`, "npm object": `{"type":"MIT"}`} {
		t.Run(name, func(t *testing.T) {
			c := newCommercialRig(t)
			ctx := context.Background()
			files := c.devRig.plugin("1.0.0")
			man := string(files[PluginManifest])
			files[PluginManifest] = []byte(man[:len(man)-1] + `,"license":` + license + `}`)
			raw, err := PackFiles(files, &c.devRig.rig.key)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := c.mgr.Install(ctx, raw, InstallRequest{Source: SourceUpload, Grant: capaGrant}); err == nil {
				t.Fatal("a new install with this license value must be refused")
			}
			if err := c.mgr.Store().Put(ctx, parse(t, raw), SourceUpload, true); err != nil {
				t.Fatal(err)
			}
			if err := c.mgr.Grants.Set(ctx, "capa", []sdk.Permission{sdk.PermKV, sdk.PermDB}); err != nil {
				t.Fatal(err)
			}
			c.mgr.Reconcile(ctx)
			if !c.loaded("capa") || c.mgr.Blocked(KindPlugin, "capa") != "" {
				t.Fatalf("loaded=%v blocked=%q err=%v", c.loaded("capa"), c.mgr.Blocked(KindPlugin, "capa"), c.mgr.LoadError("capa"))
			}
		})
	}
}

// HATAMOTO_PLUGIN_DIR plugins never reach usable, so LoadDevDir is the only
// place that keeps a commercial plugin out of it.
func TestCommercialPluginInPluginDirIsSkipped(t *testing.T) {
	for _, tc := range []struct {
		name       string
		commercial bool
	}{{"normal plugin loads", false}, {"commercial plugin is skipped", true}} {
		t.Run(tc.name, func(t *testing.T) {
			c := newCommercialRig(t)
			files := withManifest(t, c.devRig.plugin("1.0.0"), func(m *sdk.Manifest) {
				if tc.commercial {
					m.License, m.SDK = sdk.LicenseCommercial, "^0.26"
				}
			})
			dir := filepath.Join(t.TempDir(), "capa")
			for name, b := range files {
				if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			c.mgr.LoadDevDir(context.Background(), filepath.Dir(dir))
			if got := c.loaded("capa"); got == tc.commercial {
				t.Fatalf("loaded = %v with commercial=%v", got, tc.commercial)
			}
		})
	}
}
