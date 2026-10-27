package config

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"strings"
)

// DefaultRegistryURL is the registry plugins and themes are browsed from.
const DefaultRegistryURL = "https://hatamoto.dev/registry.json"

// FirstPartyKey is the public half of the "hatamoto first-party" signing key:
// the key that signs the first-party plugins in the default
// registry. It is always trusted, so those plugins install with one click. The
// private half is never in this repository (`make registry` reads it from
// HATAMOTO_REGISTRY_KEY_FILE).
const FirstPartyKey = "HsfRxf3cApV2VddoUEfOM76UrOWbr2yGT2nD/xeesAo=" // gitleaks:allow (public key)

// PackageConfig controls plugin and theme packages.
type PackageConfig struct {
	// RegistryURL is HATAMOTO_REGISTRY_URL: the signed registry index. "off"
	// disables browsing (upload still works).
	RegistryURL string
	// TrustedKeys is HATAMOTO_TRUSTED_KEYS: base64 ed25519 public keys of the
	// signers whose packages and registry indexes are accepted, separated by
	// commas or spaces.
	TrustedKeys string
	// AllowInsecureRegistry is HATAMOTO_REGISTRY_ALLOW_HTTP=true: accept an
	// http:// registry (development and tests only; the default is https only).
	AllowInsecureRegistry bool
	// NoFirstParty is HATAMOTO_TRUST_FIRST_PARTY=false: do not trust the
	// built-in first-party key, only HATAMOTO_TRUSTED_KEYS. The first-party
	// plugins then install only from a registry you sign yourself.
	NoFirstParty bool
}

// TrustedKeyList is every trusted signer key: the first-party key (unless
// HATAMOTO_TRUST_FIRST_PARTY=false) and the ones in HATAMOTO_TRUSTED_KEYS.
func (p PackageConfig) TrustedKeyList() string {
	if p.NoFirstParty {
		return strings.TrimSpace(p.TrustedKeys)
	}
	return strings.TrimSpace(FirstPartyKey + " " + p.TrustedKeys)
}

// RegistryOff reports that browsing a registry is disabled.
func (p PackageConfig) RegistryOff() bool { return strings.EqualFold(p.RegistryURL, "off") }

// LoadPackageConfig reads the package env vars.
func LoadPackageConfig() PackageConfig {
	p := PackageConfig{
		RegistryURL:           strings.TrimSpace(os.Getenv("HATAMOTO_REGISTRY_URL")),
		TrustedKeys:           strings.TrimSpace(os.Getenv("HATAMOTO_TRUSTED_KEYS")),
		AllowInsecureRegistry: strings.EqualFold(strings.TrimSpace(os.Getenv("HATAMOTO_REGISTRY_ALLOW_HTTP")), "true"),
		NoFirstParty:          strings.EqualFold(strings.TrimSpace(os.Getenv("HATAMOTO_TRUST_FIRST_PARTY")), "false"),
	}
	if p.RegistryURL == "" {
		p.RegistryURL = DefaultRegistryURL
	}
	return p
}

// Validate refuses a malformed registry URL and the theme directory variables
// 1.0 no longer reads (a site must not silently change theme).
func (p PackageConfig) Validate() error {
	if !p.RegistryOff() {
		u, err := url.Parse(p.RegistryURL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return fmt.Errorf("HATAMOTO_REGISTRY_URL %q must be an https URL (or \"off\")", p.RegistryURL)
		}
		if u.Scheme == "http" && !p.AllowInsecureRegistry {
			return fmt.Errorf("HATAMOTO_REGISTRY_URL %q is http: the registry must be served over https (HATAMOTO_REGISTRY_ALLOW_HTTP=true allows http for development)", p.RegistryURL)
		}
	}
	for _, k := range strings.FieldsFunc(p.TrustedKeys, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\t' }) {
		if b, err := base64.StdEncoding.DecodeString(k); err != nil || len(b) != ed25519.PublicKeySize {
			return fmt.Errorf("HATAMOTO_TRUSTED_KEYS holds %q, which is not an ed25519 public key (32 bytes, base64); fix or remove it", abbreviate(k))
		}
	}
	for _, name := range []string{"HATAMOTO_THEME_DIR", "HATAMOTO_SITE_THEME_DIR"} {
		if strings.TrimSpace(os.Getenv(name)) != "" {
			return fmt.Errorf("%s is no longer read: themes are packages stored in the database. Pack the directory with `hatamoto theme pack`, install it with `hatamoto package install`, activate it in Admin > Themes, then unset %s (docs/THEMES.md)", name, name)
		}
	}
	return nil
}

func abbreviate(s string) string {
	if len(s) > 16 {
		return s[:16] + "..."
	}
	return s
}
