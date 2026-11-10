package config

import (
	"strings"
	"testing"
)

func TestDeveloperRegistryConfig(t *testing.T) {
	t.Setenv("HATAMOTO_DEVELOPER_REGISTRY_URL", "")
	p := LoadPackageConfig()
	if p.DeveloperRegistryURL != DefaultDeveloperRegistryURL || p.DeveloperRegistryOff() || p.Validate() != nil {
		t.Fatalf("default: %+v, %v", p, p.Validate())
	}

	t.Setenv("HATAMOTO_DEVELOPER_REGISTRY_URL", "https://dev.example.test/registry.json")
	if p = LoadPackageConfig(); p.DeveloperRegistryURL != "https://dev.example.test/registry.json" || p.Validate() != nil {
		t.Fatalf("override: %+v", p)
	}

	t.Setenv("HATAMOTO_DEVELOPER_REGISTRY_URL", "OFF")
	if p = LoadPackageConfig(); !p.DeveloperRegistryOff() || p.Validate() != nil {
		t.Fatalf("off: %+v, %v", p, p.Validate())
	}

	t.Setenv("HATAMOTO_DEVELOPER_REGISTRY_URL", "http://dev.example.test/registry.json")
	err := LoadPackageConfig().Validate()
	if err == nil || !strings.Contains(err.Error(), "HATAMOTO_DEVELOPER_REGISTRY_URL") {
		t.Fatalf("http without the override must be refused, got %v", err)
	}
	t.Setenv("HATAMOTO_REGISTRY_ALLOW_HTTP", "true")
	if err := LoadPackageConfig().Validate(); err != nil {
		t.Fatalf("http with override: %v", err)
	}

	t.Setenv("HATAMOTO_DEVELOPER_REGISTRY_URL", "ftp://x/y")
	if LoadPackageConfig().Validate() == nil {
		t.Fatal("a non-http scheme must be refused")
	}
}

func TestListingKeyIsNeverAPackageTrustKey(t *testing.T) {
	for _, p := range []PackageConfig{{}, {NoFirstParty: true}, {TrustedKeys: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="}} {
		if strings.Contains(p.TrustedKeyList(), DeveloperListingKey) {
			t.Errorf("TrustedKeyList %q holds the listing key", p.TrustedKeyList())
		}
	}
}
