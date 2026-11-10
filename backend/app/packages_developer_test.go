package app

import (
	"os"
	"path/filepath"
	"testing"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/packages"
)

func TestIsPackageName(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile(filepath.Join(dir, "local-theme"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for arg, want := range map[string]bool{
		"acme-seo":                     true,
		"local-theme":                  false, // an existing file wins
		"./acme-seo":                   false,
		"acme.hmp":                     false,
		"dist/acme-seo.hmp":            false,
		"https://example.com/acme.hmp": false,
		"http://example.com/acme":      false,
		"Not A Name":                   false,
		"":                             false,
	} {
		if got := isPackageName(arg); got != want {
			t.Errorf("isPackageName(%q) = %v, want %v", arg, got, want)
		}
	}
}

// The listing key signs only the developer index and is never a package key.
func TestPackageRegistriesKeepTheListingKeyOutOfTheTrustSet(t *testing.T) {
	trust := packages.TrustSet{}
	main, dev := packageRegistries(config.PackageConfig{RegistryURL: "https://reg.example/r.json", DeveloperRegistryURL: "https://dev.example/r.json"}, trust)
	if main == nil || dev == nil || main.URL != "https://reg.example/r.json" || dev.URL != "https://dev.example/r.json" {
		t.Fatalf("registries: %+v %+v", main, dev)
	}
	listing, err := packages.ParsePublicKey(config.DeveloperListingKey)
	if err != nil {
		t.Fatal(err)
	}
	id := packages.KeyID(listing)
	if _, ok := dev.Trust[id]; !ok || len(dev.Trust) != 1 {
		t.Errorf("developer registry trust: %v", dev.Trust)
	}
	if _, ok := main.Trust[id]; ok {
		t.Error("the main registry trusts the listing key")
	}
	if _, ok := trust[id]; ok {
		t.Error("the package trust set holds the listing key")
	}
}

func TestPackageRegistriesOff(t *testing.T) {
	main, dev := packageRegistries(config.PackageConfig{RegistryURL: "off", DeveloperRegistryURL: "off"}, nil)
	if main != nil || dev != nil {
		t.Fatalf("off registries are built: %+v %+v", main, dev)
	}
}
