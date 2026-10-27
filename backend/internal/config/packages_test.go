package config

import (
	"strings"
	"testing"
)

func TestPackageConfigDefaultsAndValidate(t *testing.T) {
	t.Setenv("HATAMOTO_REGISTRY_URL", "")
	p := LoadPackageConfig()
	if p.RegistryURL != DefaultRegistryURL || p.Validate() != nil {
		t.Fatalf("defaults: %+v, %v", p, p.Validate())
	}
	for _, bad := range []string{"ftp://x/y", "not a url", "/relative"} {
		if (PackageConfig{RegistryURL: bad}).Validate() == nil {
			t.Errorf("registry %q should be refused", bad)
		}
	}
	if err := (PackageConfig{RegistryURL: "off"}).Validate(); err != nil {
		t.Errorf("off: %v", err)
	}
	if (PackageConfig{RegistryURL: "http://registry.test/registry.json"}).Validate() == nil {
		t.Error("an http registry must be refused without the explicit override")
	}
	if err := (PackageConfig{RegistryURL: "http://registry.test/registry.json", AllowInsecureRegistry: true}).Validate(); err != nil {
		t.Errorf("http with override: %v", err)
	}
}

func TestTrustedKeysAreValidatedAtBoot(t *testing.T) {
	good := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	if err := (PackageConfig{RegistryURL: "off", TrustedKeys: good + ", " + good}).Validate(); err != nil {
		t.Errorf("valid keys: %v", err)
	}
	err := (PackageConfig{RegistryURL: "off", TrustedKeys: good + " not-a-key"}).Validate()
	if err == nil || !strings.Contains(err.Error(), "HATAMOTO_TRUSTED_KEYS") {
		t.Errorf("a malformed key must stop boot, got %v", err)
	}
}

func TestRemovedThemeDirVariablesAreRefused(t *testing.T) {
	for _, name := range []string{"HATAMOTO_THEME_DIR", "HATAMOTO_SITE_THEME_DIR"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(name, "/srv/theme")
			err := LoadPackageConfig().Validate()
			if err == nil || !strings.Contains(err.Error(), name) || !strings.Contains(err.Error(), "hatamoto theme pack") {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestFirstPartyKeyIsAlwaysTrusted(t *testing.T) {
	if err := (PackageConfig{RegistryURL: "off", TrustedKeys: FirstPartyKey}).Validate(); err != nil {
		t.Fatalf("the shipped first-party key is not a valid public key: %v", err)
	}
	if got := (PackageConfig{}).TrustedKeyList(); got != FirstPartyKey {
		t.Fatalf("default trust = %q", got)
	}
	if got := (PackageConfig{TrustedKeys: "k2"}).TrustedKeyList(); got != FirstPartyKey+" k2" {
		t.Fatalf("with env keys = %q", got)
	}
}

func TestFirstPartyTrustCanBeSwitchedOff(t *testing.T) {
	t.Setenv("HATAMOTO_TRUST_FIRST_PARTY", "false")
	t.Setenv("HATAMOTO_TRUSTED_KEYS", "k2")
	got := LoadPackageConfig().TrustedKeyList()
	if got != "k2" || strings.Contains(got, FirstPartyKey) {
		t.Fatalf("trust with the first-party key off = %q", got)
	}
	t.Setenv("HATAMOTO_TRUST_FIRST_PARTY", "")
	if got := LoadPackageConfig().TrustedKeyList(); got != FirstPartyKey+" k2" {
		t.Fatalf("the first-party key is trusted by default: %q", got)
	}
}

func TestFirstPartyKeyIsTheRotatedKey(t *testing.T) {
	if FirstPartyKey != "HsfRxf3cApV2VddoUEfOM76UrOWbr2yGT2nD/xeesAo=" {
		t.Fatalf("FirstPartyKey = %q", FirstPartyKey)
	}
	list := strings.Fields(PackageConfig{}.TrustedKeyList())
	if len(list) != 1 || list[0] != FirstPartyKey {
		t.Fatalf("default trust = %v, want only the first-party key", list)
	}
}
