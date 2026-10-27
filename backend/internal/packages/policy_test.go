package packages

import (
	"context"
	"errors"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/pluginhost"
	"hatamoto.dev/sdk"
)

func TestCheckScriptsGrant(t *testing.T) {
	scripts := []string{sdk.PermKV, sdk.PermSiteScripts}
	for _, tt := range []struct {
		name  string
		o     Origin
		perms []string
		want  error
	}{
		{"community asks scripts", OriginCommunity, scripts, ErrCommunityScripts},
		{"community asks island", OriginCommunity, []string{sdk.PermBuilderIsland}, ErrCommunityScripts},
		{"first party island", OriginFirstParty, []string{sdk.PermBuilderIsland}, nil},
		{"self built island", OriginSelfBuilt, []string{sdk.PermBuilderIsland}, nil},
		{"community without scripts", OriginCommunity, []string{sdk.PermKV}, nil},
		{"community revokes all", OriginCommunity, []string{}, nil},
		{"first party scripts", OriginFirstParty, scripts, nil},
		{"self built scripts", OriginSelfBuilt, scripts, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := checkScriptsGrant(tt.o, tt.perms); !errors.Is(err, tt.want) {
				t.Fatalf("got %v, want %v", err, tt.want)
			}
		})
	}
}

func TestManagerCheckGrantUsesOrigins(t *testing.T) {
	m := &Manager{}
	m.refreshOrigins([]Row{
		{Kind: KindPlugin, Name: "fancy", SignerKeyID: "adea8e00f5ac74ca"},
		{Kind: KindPlugin, Name: "gtm", SignerKeyID: "adea8e00f5ac74ca"},
		{Kind: KindPlugin, Name: "mine"},
	})
	scripts := []string{sdk.PermSiteScripts}
	if err := m.CheckGrant("fancy", scripts); !errors.Is(err, ErrCommunityScripts) {
		t.Fatalf("community: %v", err)
	}
	if err := m.CheckGrant("fancy", []sdk.Permission{sdk.PermBuilderIsland}); !errors.Is(err, ErrCommunityScripts) || !strings.Contains(err.Error(), "builder.island") {
		t.Fatalf("community island: %v", err)
	}
	if err := m.CheckGrant("gtm", []sdk.Permission{sdk.PermBuilderIsland}); err != nil {
		t.Fatalf("first party island: %v", err)
	}
	if err := m.CheckGrant("fancy", []string{sdk.PermKV}); err != nil {
		t.Fatalf("community without scripts: %v", err)
	}
	for _, name := range []string{"gtm", "mine", "unknown"} {
		if err := m.CheckGrant(name, scripts); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if !m.ScriptsRefused("fancy") || m.ScriptsRefused("gtm") {
		t.Fatal("ScriptsRefused disagrees with the origin map")
	}
}

// scriptGrants grants site.scripts to every plugin it lists.
type scriptGrants map[string]bool

func (g scriptGrants) Has(p string, perm sdk.Permission) bool {
	return g[p] && perm == sdk.PermSiteScripts
}
func (g scriptGrants) Granted(string) []sdk.Permission { return nil }
func (g scriptGrants) Set(context.Context, string, []sdk.Permission) error {
	return nil
}

// statusPlugins is a PluginRuntime that only reports statuses.
type statusPlugins []pluginhost.Status

func (s statusPlugins) LoadWith(context.Context, sdk.Manifest, []byte, pluginhost.Assets) pluginhost.Status {
	return pluginhost.Status{}
}
func (s statusPlugins) Unload(context.Context, string) {}
func (s statusPlugins) Statuses() []pluginhost.Status  { return s }

func TestScriptGateAndCommunityLive(t *testing.T) {
	loaded := func(name string) pluginhost.Status {
		return pluginhost.Status{Name: name, Loaded: true, Manifest: sdk.Manifest{Name: name, Permissions: []string{sdk.PermSiteScripts}}}
	}
	m := &Manager{Options: Options{
		Plugins: statusPlugins{loaded("fancy"), loaded("gtm"), loaded("mine")},
		Grants:  scriptGrants{"fancy": true, "gtm": true, "mine": true},
	}}
	m.refreshOrigins([]Row{
		{Kind: KindPlugin, Name: "fancy", SignerKeyID: "adea8e00f5ac74ca"},
		{Kind: KindPlugin, Name: "gtm", SignerKeyID: "adea8e00f5ac74ca"},
		{Kind: KindPlugin, Name: "mine"},
	})

	// Production: a stored community grant links nothing and is not live.
	if m.scriptGate("fancy") || m.CommunityScriptsLive() {
		t.Fatal("a stored community grant must be neutralised")
	}
	if !m.scriptGate("gtm") || !m.scriptGate("mine") {
		t.Fatal("first-party and self-built scripts must be unaffected")
	}

	// Policy off (sandbox path): the community script is linked and live.
	refuseCommunityScripts = false
	t.Cleanup(func() { refuseCommunityScripts = true })
	if !m.scriptGate("fancy") || !m.CommunityScriptsLive() {
		t.Fatal("with the policy off a granted, loaded community script is live")
	}

	// Live needs the grant, the loaded manifest and a community plugin.
	m.Grants = scriptGrants{"gtm": true, "mine": true}
	if m.CommunityScriptsLive() {
		t.Fatal("without the grant nothing community is live")
	}
	m.Grants = scriptGrants{"fancy": true}
	m.Plugins = statusPlugins{{Name: "fancy", Loaded: true, Manifest: sdk.Manifest{Name: "fancy"}}}
	if m.CommunityScriptsLive() {
		t.Fatal("a stale grant (manifest no longer asks) must not count")
	}
}

func TestCommunityPluginUsesOriginsMap(t *testing.T) {
	m := &Manager{}
	m.refreshOrigins([]Row{
		{Kind: KindPlugin, Name: "fancy", SignerKeyID: "adea8e00f5ac74ca"},
		{Kind: KindPlugin, Name: "gtm", SignerKeyID: "adea8e00f5ac74ca"},
		{Kind: KindPlugin, Name: "mine"},
	})
	for name, want := range map[string]bool{"fancy": true, "gtm": false, "mine": false, "unknown": false} {
		if got := m.CommunityPlugin(name); got != want {
			t.Errorf("CommunityPlugin(%q) = %v, want %v", name, got, want)
		}
	}
}
