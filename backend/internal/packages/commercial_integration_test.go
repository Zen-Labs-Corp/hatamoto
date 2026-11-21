//go:build integration

package packages

import (
	"context"
	"errors"
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
