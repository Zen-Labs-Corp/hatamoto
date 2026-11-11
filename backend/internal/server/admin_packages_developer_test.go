package server

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/config"
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

// Browse does not offer a developer package the developer index revokes, by
// key, by name, or by the listed version.
func TestAppendDeveloperSkipsRevoked(t *testing.T) {
	keep, gone := newDevKey(t), newDevKey(t)
	dev := &packages.Index{
		Packages: []packages.Entry{
			{Kind: packages.KindPlugin, Name: "keep", Version: "1.0.0", SignerKey: keep, Tier: packages.TierDeveloper},
			{Kind: packages.KindPlugin, Name: "bykey", Version: "1.0.0", SignerKey: gone, Tier: packages.TierDeveloper},
			{Kind: packages.KindPlugin, Name: "byname", Version: "1.0.0", SignerKey: keep, Tier: packages.TierDeveloper},
			{Kind: packages.KindPlugin, Name: "byversion", Version: "1.0.0", SignerKey: keep, Tier: packages.TierDeveloper},
			{Kind: packages.KindPlugin, Name: "otherversion", Version: "1.0.0", SignerKey: keep, Tier: packages.TierDeveloper},
		},
		Revoked: []packages.Revocation{
			{SignerKey: gone}, {Name: "byname"}, {Name: "byversion", Version: "1.0.0"}, {Name: "otherversion", Version: "2.0.0"},
		},
	}
	var v admin.RegistryView
	(&packageAdmin{}).appendDeveloper(&v, dev, nil)
	var got []string
	for _, e := range v.Entries {
		got = append(got, e.Name)
	}
	if want := "keep,otherversion"; strings.Join(got, ",") != want {
		t.Fatalf("entries = %v, want %s", got, want)
	}
}

func newDevKey(t *testing.T) string {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(pub)
}

// Admin Browse resolves a developer entry's screenshot against the developer
// index, not the main registry.
func TestRegistryEntryDeveloperScreenshot(t *testing.T) {
	p := developerRegistryRig(t, packages.Options{DeveloperRegistry: &packages.Registry{URL: "https://dev.example.test/p/devportal/registry.json"}})
	p.s.cfg = &config.Config{}
	e := p.registryEntry(packages.Entry{Kind: packages.KindTheme, Name: "acme", Tier: packages.TierDeveloper, Screenshot: "screenshots/acme.png"}, nil)
	if want := "https://dev.example.test/p/devportal/screenshots/acme.png"; e.Screenshot != want {
		t.Fatalf("screenshot = %q, want %q", e.Screenshot, want)
	}
}
