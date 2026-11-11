package packages

import (
	"context"
	"errors"
	"slices"
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
		{Kind: KindPlugin, Name: "searchconsole", SignerKeyID: "adea8e00f5ac74ca"},
		{Kind: KindPlugin, Name: "mine"},
	})
	scripts := []string{sdk.PermSiteScripts}
	if err := m.CheckGrant("fancy", scripts); !errors.Is(err, ErrCommunityScripts) {
		t.Fatalf("community: %v", err)
	}
	if err := m.CheckGrant("fancy", []sdk.Permission{sdk.PermBuilderIsland}); !errors.Is(err, ErrCommunityScripts) || !strings.Contains(err.Error(), "builder.island") {
		t.Fatalf("community island: %v", err)
	}
	if err := m.CheckGrant("searchconsole", []sdk.Permission{sdk.PermBuilderIsland}); err != nil {
		t.Fatalf("first party island: %v", err)
	}
	if err := m.CheckGrant("fancy", []string{sdk.PermKV}); err != nil {
		t.Fatalf("community without scripts: %v", err)
	}
	for _, name := range []string{"searchconsole", "mine", "unknown"} {
		if err := m.CheckGrant(name, scripts); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if !m.ScriptsRefused("fancy") || m.ScriptsRefused("searchconsole") {
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
		Plugins: statusPlugins{loaded("fancy"), loaded("searchconsole"), loaded("mine")},
		Grants:  scriptGrants{"fancy": true, "searchconsole": true, "mine": true},
	}}
	m.refreshOrigins([]Row{
		{Kind: KindPlugin, Name: "fancy", SignerKeyID: "adea8e00f5ac74ca"},
		{Kind: KindPlugin, Name: "searchconsole", SignerKeyID: "adea8e00f5ac74ca"},
		{Kind: KindPlugin, Name: "mine"},
	})

	// Production: a stored community grant links nothing and is not live.
	if m.scriptGate("fancy") || m.CommunityScriptsLive() {
		t.Fatal("a stored community grant must be neutralised")
	}
	if !m.scriptGate("searchconsole") || !m.scriptGate("mine") {
		t.Fatal("first-party and self-built scripts must be unaffected")
	}

	// Policy off (sandbox path): the community script is linked and live.
	refuseCommunityScripts = false
	t.Cleanup(func() { refuseCommunityScripts = true })
	if !m.scriptGate("fancy") || !m.CommunityScriptsLive() {
		t.Fatal("with the policy off a granted, loaded community script is live")
	}

	// Live needs the grant, the loaded manifest and a community plugin.
	m.Grants = scriptGrants{"searchconsole": true, "mine": true}
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
		{Kind: KindPlugin, Name: "searchconsole", SignerKeyID: "adea8e00f5ac74ca"},
		{Kind: KindPlugin, Name: "mine"},
	})
	for name, want := range map[string]bool{"fancy": true, "searchconsole": false, "mine": false, "unknown": false} {
		if got := m.CommunityPlugin(name); got != want {
			t.Errorf("CommunityPlugin(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestRefusedPermissionsForCommunityPackage(t *testing.T) {
	pkg := &Package{Kind: KindPlugin, Name: "blocks", Manifest: []byte(`{"permissions":["kv","builder.island","site.scripts"]}`),
		Signature: &Signature{Signer: FirstPartyKeyIDs[0]}}
	if got := RefusedPermissions(Classify(pkg.Name, signerOf(pkg)), pkg); !slices.Equal(got, []string{"builder.island", "site.scripts"}) {
		t.Fatalf("community refused = %v", got)
	}
	pkg.Signature = nil // self built: policy does not refuse
	if got := RefusedPermissions(Classify(pkg.Name, signerOf(pkg)), pkg); len(got) != 0 {
		t.Fatalf("self built refused = %v", got)
	}
}

func TestCheckCommunityCSP(t *testing.T) {
	img := &sdk.CSP{ImgSrc: []string{"https://img.example.com"}}
	for _, tt := range []struct {
		name string
		o    Origin
		csp  *sdk.CSP
		want bool
	}{
		{"community images", OriginCommunity, img, false},
		{"community none", OriginCommunity, nil, false},
		{"community script", OriginCommunity, &sdk.CSP{ScriptSrc: []string{"https://js.example.com"}}, true},
		{"community connect", OriginCommunity, &sdk.CSP{ConnectSrc: []string{"https://api.example.com"}}, true},
		{"community frame", OriginCommunity, &sdk.CSP{FrameSrc: []string{"https://f.example.com"}}, true},
		{"first party script", OriginFirstParty, &sdk.CSP{ScriptSrc: []string{"https://js.example.com"}}, false},
		{"self built script", OriginSelfBuilt, &sdk.CSP{ScriptSrc: []string{"https://js.example.com"}}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := checkCommunityCSP(tt.o, sdk.Manifest{CSP: tt.csp})
			if (err != nil) != tt.want || (err != nil && err.Error() != "Community plugins may only add image hosts.") {
				t.Fatalf("got %v, want refusal %v", err, tt.want)
			}
		})
	}
}

// An update that adds a page-security host is widened access; the same hosts are not.
func TestNewCSPHosts(t *testing.T) {
	files := promoPack()
	files[PluginManifest] = []byte(`{"name":"promo","version":"1.1.0","sdk":"^0.18","permissions":["builder.island"],"csp":{"script_src":["https://js.example.com"],"img_src":["https://img.example.com"]}}`)
	raw, err := PackFiles(files, nil)
	if err != nil {
		t.Fatal(err)
	}
	p := parse(t, raw)
	if got := newCSPHosts([]byte(`{"name":"promo","version":"1.0.0","sdk":"^0.18"}`), p); len(got) != 2 {
		t.Errorf("new hosts = %v, want both", got)
	}
	if got := newCSPHosts([]byte(`{"name":"promo","version":"1.0.0","sdk":"^0.18","csp":{"script_src":["https://js.example.com"]}}`), p); !slices.Equal(got, []string{"https://img.example.com"}) {
		t.Errorf("new hosts = %v, want only the image host", got)
	}
	if got := newCSPHosts([]byte(`{"name":"promo","csp":{"script_src":["https://js.example.com"],"img_src":["https://img.example.com"]}}`), p); len(got) != 0 {
		t.Errorf("unchanged hosts = %v", got)
	}
}

// An update that adds a request header is widened access; the same headers are not.
func TestNewRequestHeaders(t *testing.T) {
	files := promoPack()
	files[PluginManifest] = []byte(`{"name":"promo","version":"1.1.0","sdk":"^0.18","permissions":["builder.island"],"request_headers":["Stripe-Signature","X-Hook"]}`)
	raw, err := PackFiles(files, nil)
	if err != nil {
		t.Fatal(err)
	}
	p := parse(t, raw)
	if got := newRequestHeaders([]byte(`{"name":"promo","version":"1.0.0","sdk":"^0.18"}`), p); !slices.Equal(got, []string{"stripe-signature", "x-hook"}) {
		t.Errorf("new headers = %v, want both", got)
	}
	if got := newRequestHeaders([]byte(`{"name":"promo","request_headers":["stripe-signature"]}`), p); !slices.Equal(got, []string{"x-hook"}) {
		t.Errorf("new headers = %v, want only x-hook", got)
	}
	if got := newRequestHeaders([]byte(`{"name":"promo","request_headers":["x-hook","STRIPE-SIGNATURE"]}`), p); len(got) != 0 {
		t.Errorf("unchanged headers = %v", got)
	}
}

// An update that adds a redirect path is widened access; the same paths are not.
func TestNewRedirectPaths(t *testing.T) {
	p := parse(t, mustPackPaths(t, pathsPack("promo", "1.1.0", "/go", "/start")))
	if got := newRedirectPaths([]byte(`{"name":"promo","version":"1.0.0","sdk":"^0.22"}`), p); !slices.Equal(got, []string{"/go", "/start"}) {
		t.Errorf("new paths = %v, want both", got)
	}
	if got := newRedirectPaths([]byte(`{"name":"promo","redirect_paths":["/go"]}`), p); !slices.Equal(got, []string{"/start"}) {
		t.Errorf("new paths = %v, want only /start", got)
	}
	if got := newRedirectPaths([]byte(`{"name":"promo","redirect_paths":["/start","/go","/other"]}`), p); len(got) != 0 {
		t.Errorf("unchanged paths = %v", got)
	}
	// A trailing slash is a different path.
	if got := newRedirectPaths([]byte(`{"name":"promo","redirect_paths":["/go/","/start"]}`), p); !slices.Equal(got, []string{"/go"}) {
		t.Errorf("new paths = %v, want only /go", got)
	}
}

func mustPackPaths(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	raw, err := PackFiles(files, nil)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// A developer package gets the community policy at the prompt: Install would
// refuse the same permissions and CSP hosts.
func TestDeveloperPackageGetsCommunityPolicyAtThePrompt(t *testing.T) {
	pkg := &Package{Kind: KindPlugin, Name: "acme-seo",
		Manifest:  []byte(`{"permissions":["kv","builder.island"],"csp":{"script_src":["https://js.example.com"]}}`),
		Signature: &Signature{Signer: "0123456789abcdef"}}
	if got := RefusedPermissions(OriginDeveloper, pkg); !slices.Equal(got, []string{"builder.island"}) {
		t.Fatalf("developer refused = %v", got)
	}
	if err := CheckCommunityCSP(OriginDeveloper, pkg); err == nil {
		t.Fatal("a developer package with a script host passed the prompt")
	}
}

func TestCheckDeveloperPin(t *testing.T) {
	p := &Package{Kind: KindPlugin, Name: "acme-seo", Version: "2.0.0", Signature: &Signature{Signer: "aaaaaaaaaaaaaaaa"}}
	row := Row{Developer: true, SignerKeyID: "aaaaaaaaaaaaaaaa", DeveloperKey: "key-a"}
	if err := CheckDeveloperPin(row, p, "key-a"); err != nil {
		t.Fatalf("same key: %v", err)
	}
	// The same 64-bit id with another full key is another key.
	if err := CheckDeveloperPin(row, p, "key-b"); !errors.Is(err, ErrDeveloperSignerChanged) {
		t.Fatalf("same id, other key: %v", err)
	}
	if err := CheckDeveloperPin(row, p, ""); !errors.Is(err, ErrDeveloperSignerChanged) {
		t.Fatalf("not a developer package: %v", err)
	}
	if err := CheckDeveloperPin(Row{SignerKeyID: "aaaaaaaaaaaaaaaa"}, p, ""); err != nil {
		t.Fatalf("not a developer row: %v", err)
	}
}
