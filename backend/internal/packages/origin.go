package packages

import (
	"slices"

	"hatamoto.dev/core/internal/config"
)

// Origin says whose code a plugin is, for the community-script policy.
type Origin int

const (
	// OriginSelfBuilt is unsigned, dev, or signed by another owner-trusted key.
	OriginSelfBuilt Origin = iota
	// OriginFirstParty is signed by a first-party key and on FirstPartyPlugins.
	OriginFirstParty
	// OriginCommunity is signed by a first-party key but not on FirstPartyPlugins
	// (the registry signs community builds with the same key).
	OriginCommunity
	// OriginDeveloper is signed by a developer's own key and installed from the
	// developer index. It gets the community policy. It is last so the stored
	// values of the other origins never change.
	OriginDeveloper
)

// restricted reports whether the origin gets the community policy: no scripts
// in the site origin and image hosts only in the csp block.
func (o Origin) restricted() bool { return o == OriginCommunity || o == OriginDeveloper }

// FirstPartyPlugins are the plugins the core team maintains. The list is the
// rule; the author is not checked.
var FirstPartyPlugins = []string{"searchconsole"}

// FirstPartyKeyIDs are the key ids first-party packages were ever signed under:
// the key rotated out on 2026-10-16 (docs/PACKAGES.md) and the one that replaced
// it. config.FirstPartyKey is checked as well. The list is fixed so a later
// rotation does not strand a package signed under an earlier key.
var FirstPartyKeyIDs = []string{"a592a1276b3947b1", "adea8e00f5ac74ca"}

// IsFirstPartyKey reports whether id is a first-party signing key id.
func IsFirstPartyKey(id string) bool {
	if id == "" {
		return false
	}
	if slices.Contains(FirstPartyKeyIDs, id) {
		return true
	}
	pub, err := ParsePublicKey(config.FirstPartyKey)
	return err == nil && id == KeyID(pub)
}

// Classify returns whose code the plugin name signed by signerKeyID is.
func Classify(name, signerKeyID string) Origin {
	if !IsFirstPartyKey(signerKeyID) {
		return OriginSelfBuilt
	}
	if slices.Contains(FirstPartyPlugins, name) {
		return OriginFirstParty
	}
	return OriginCommunity
}

// ClassifyRow classifies a stored package row. A developer row is
// OriginDeveloper unless a first-party key signed it: that key never makes a
// package a developer package.
func ClassifyRow(r Row) Origin {
	if r.Developer && !IsFirstPartyKey(r.SignerKeyID) {
		return OriginDeveloper
	}
	return Classify(r.Name, r.SignerKeyID)
}
