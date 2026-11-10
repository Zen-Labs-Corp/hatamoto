package server

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"testing"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/packages"
)

// A developer entry carries the key id its index claims; a main entry has none.
func TestRegistryEntryDeveloper(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	p := &packageAdmin{}
	dev := p.registryEntry(packages.Entry{Kind: packages.KindPlugin, Name: "seo", Tier: packages.TierDeveloper,
		SignerKey: base64.StdEncoding.EncodeToString(pub)}, nil)
	if !dev.Developer || dev.SignerKeyID != packages.KeyID(pub) || len(dev.SignerKeyID) != 16 {
		t.Fatalf("developer entry = %+v", dev)
	}
	main := p.registryEntry(packages.Entry{Kind: packages.KindPlugin, Name: "gtm", Tier: packages.TierFirstParty}, nil)
	if main.Developer || main.SignerKeyID != "" {
		t.Fatalf("main entry = %+v", main)
	}
}

func TestRegistryDeveloperOrder(t *testing.T) {
	es := []admin.RegistryEntry{
		{Name: "zed", Tier: packages.TierFirstParty},
		{Name: "acme", Tier: packages.TierDeveloper, Developer: true},
		{Name: "beta", Tier: packages.TierCommunity},
		{Name: "alpha", Tier: packages.TierFirstParty},
	}
	sortRegistryEntries(es)
	var got []string
	for _, e := range es {
		got = append(got, e.Name)
	}
	want := []string{"alpha", "zed", "acme", "beta"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

func developerRegistryRig(t *testing.T, opts packages.Options) *packageAdmin {
	t.Helper()
	return &packageAdmin{s: &Server{packages: &packages.Manager{Options: opts}, logger: quietLogger()}}
}

// With the developer index off, or unreadable, the main list stays intact and
// no error is reported.
func TestRegistryDeveloperIndexOffOrFailing(t *testing.T) {
	for name, opts := range map[string]packages.Options{
		"off":     {OnlySigners: []string{"x"}},
		"failing": {DeveloperRegistry: &packages.Registry{URL: "http://dev.example.test/registry.json"}},
	} {
		t.Run(name, func(t *testing.T) {
			v := admin.RegistryView{Entries: []admin.RegistryEntry{{Name: "gtm"}}}
			developerRegistryRig(t, opts).addDeveloperEntries(context.Background(), &v, nil)
			if v.Err != "" || v.Off || v.DeveloperAvail || len(v.Entries) != 1 || v.Entries[0].Name != "gtm" {
				t.Fatalf("view = %+v", v)
			}
		})
	}
}
