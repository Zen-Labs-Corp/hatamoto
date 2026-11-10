package packages

import (
	"errors"
	"testing"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/sdk"
)

func TestOriginValuesAreStable(t *testing.T) {
	if OriginSelfBuilt != 0 || OriginFirstParty != 1 || OriginCommunity != 2 || OriginDeveloper != 3 {
		t.Fatalf("origin values changed: %d %d %d %d", OriginSelfBuilt, OriginFirstParty, OriginCommunity, OriginDeveloper)
	}
}

func TestClassifyRowDeveloper(t *testing.T) {
	pub, err := ParsePublicKey(config.FirstPartyKey)
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name string
		row  Row
		want Origin
	}{
		{"developer row", Row{Name: "acme", SignerKeyID: "0123456789abcdef", Developer: true}, OriginDeveloper},
		{"developer row under a list name", Row{Name: "searchconsole", SignerKeyID: "0123456789abcdef", Developer: true}, OriginDeveloper},
		{"first-party key is never developer", Row{Name: "acme", SignerKeyID: KeyID(pub), Developer: true}, OriginCommunity},
		{"old first-party key is never developer", Row{Name: "searchconsole", SignerKeyID: FirstPartyKeyIDs[0], Developer: true}, OriginFirstParty},
		{"not a developer row", Row{Name: "acme", SignerKeyID: "0123456789abcdef"}, OriginSelfBuilt},
	} {
		if got := ClassifyRow(tt.row); got != tt.want {
			t.Errorf("%s: ClassifyRow = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestDeveloperGetsCommunityPolicy(t *testing.T) {
	scripts := []string{sdk.PermKV, sdk.PermSiteScripts}
	for _, perms := range [][]string{scripts, {sdk.PermBuilderIsland}} {
		if err := checkScriptsGrant(OriginDeveloper, perms); !errors.Is(err, ErrCommunityScripts) {
			t.Errorf("developer %v: %v, want ErrCommunityScripts", perms, err)
		}
	}
	if err := checkScriptsGrant(OriginDeveloper, []string{sdk.PermKV}); err != nil {
		t.Errorf("developer without scripts: %v", err)
	}

	for _, csp := range []*sdk.CSP{{ScriptSrc: []string{"https://js.example.com"}}, {ConnectSrc: []string{"https://a.example.com"}}, {FrameSrc: []string{"https://f.example.com"}}} {
		err := checkCommunityCSP(OriginDeveloper, sdk.Manifest{CSP: csp})
		if !errors.Is(err, ErrCommunityCSP) || err.Error() != "Developer plugins may only add image hosts." {
			t.Errorf("developer csp %+v: %v", csp, err)
		}
	}
	if err := checkCommunityCSP(OriginDeveloper, sdk.Manifest{CSP: &sdk.CSP{ImgSrc: []string{"https://img.example.com"}}}); err != nil {
		t.Errorf("developer image hosts: %v", err)
	}
}

// After a reboot the origins map is rebuilt from the rows alone (no index).
func TestDeveloperOriginsMapAfterBoot(t *testing.T) {
	m := &Manager{}
	m.refreshOrigins([]Row{
		{Kind: KindPlugin, Name: "acme", SignerKeyID: "0123456789abcdef", Developer: true},
		{Kind: KindPlugin, Name: "mine"},
	})
	if !m.CommunityPlugin("acme") || !m.ScriptsRefused("acme") || m.CommunityPlugin("mine") {
		t.Fatal("a developer plugin must get the community policy from its stored row")
	}
}
