//go:build integration

package server

import (
	"context"
	"errors"
	"slices"
	"testing"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/hooks"
	"hatamoto.dev/core/internal/pluginhost/plugintest"
	"hatamoto.dev/sdk"
)

// A community-signed package is refused site.scripts and builder.island through the
// real plugin adapter, both as a loaded plugin and as a package with no wasm, and
// the plugins page is told which permissions are refused.
func TestPluginAdminCommunityGrant(t *testing.T) {
	r := newLegacyRig(t)
	s := r.s
	s.hooks = hooks.New(s.logger)
	ctx := context.Background()
	rt := capaRuntime(t, s)
	rt.SetGrantPolicy(s.packages.CheckGrant)
	s.packages.Grants = rt.Grants()

	const communityKey = "adea8e00f5ac74ca" // signed by a first-party key, not on the first-party list
	insert := func(name, manifest string) {
		t.Helper()
		if _, err := s.db.Pool().Exec(ctx, `INSERT INTO hatamoto_packages (kind, name, version, manifest, data, sha256, signature, signer_key_id, source)
			VALUES ('plugin', $1, '0.1.0', $2, '\x00', 'x', '\x00', $3, 'registry')`, name, manifest, communityKey); err != nil {
			t.Fatal(err)
		}
	}
	insert("capa", `{"name":"capa","version":"0.1.0","permissions":["kv","site.scripts"]}`)
	insert("islands", `{"name":"islands","version":"0.1.0","permissions":["kv","builder.island"]}`)
	s.packages.Reconcile(ctx)

	// The runtime plugin asks for site.scripts too; a stale grant is stored.
	m := capaManifest()
	m.Permissions = append(m.Permissions, string(sdk.PermSiteScripts))
	if st := rt.Load(ctx, m, plugintest.Build(t, "capa")); !st.Loaded {
		t.Fatalf("capa: %s", st.Error)
	}
	if err := rt.Grants().Set(ctx, "capa", []sdk.Permission{sdk.PermKV, sdk.PermSiteScripts}); err != nil {
		t.Fatal(err)
	}

	pa := pluginAdmin{s: s, pkgs: &packageAdmin{s: s}}
	refused := map[string][]string{}
	for _, p := range pa.Plugins() {
		refused[p.Name] = p.Refused
	}
	if !slices.Equal(refused["capa"], []string{"site.scripts"}) {
		t.Errorf("runtime plugin refused = %v", refused["capa"])
	}
	if !slices.Equal(refused["islands"], []string{"builder.island"}) {
		t.Errorf("package-only refused = %v", refused["islands"])
	}

	if err := pa.Grant(ctx, "capa", []string{"kv", "site.scripts"}); !errors.Is(err, admin.ErrCommunityGrant) {
		t.Errorf("runtime grant = %v, want ErrCommunityGrant", err)
	}
	if err := pa.Grant(ctx, "islands", []string{"builder.island"}); !errors.Is(err, admin.ErrCommunityGrant) {
		t.Errorf("package-only grant = %v, want ErrCommunityGrant", err)
	}
	if err := pa.Grant(ctx, "capa", []string{"kv"}); err != nil {
		t.Fatalf("grant without the refused permission: %v", err)
	}
	if g := rt.Grants().Granted("capa"); !slices.Equal(g, []sdk.Permission{sdk.PermKV}) {
		t.Errorf("granted after save = %v", g)
	}
}
