// Package standing stores the owner's standing approvals: the one-time
// permission for a cron started plugin run to publish by itself when
// everything it staged stays inside a rule the plugin declared. An approval
// is bound to a fingerprint of the plugin and the schemas it names, and ends
// as soon as either changes.
//
// The package knows nothing about changesets or the plugin host; the callers
// decide what an approval allows and when it is checked.
package standing

import (
	"errors"
	"slices"
	"time"
)

// Policy is the rule an approval grants, copied from the plugin's manifest
// when the owner approves.
type Policy struct {
	Fields     []string `json:"fields"`
	Schemas    []string `json:"schemas"`
	MaxPerHour int      `json:"max_per_hour"`
	// Create is set only when the approved rule opted in to publishing new
	// entries; nil keeps the stored form and the behaviour of an update rule.
	Create *CreatePolicy `json:"create,omitempty"`
}

// CreatePolicy is the create part of an approved rule: the schemas whose new
// entries may publish by themselves and the hourly cap.
type CreatePolicy struct {
	Schemas    []string `json:"schemas"`
	MaxPerHour int      `json:"max_per_hour"`
}

// Fingerprint identifies what the owner approved: the plugin release, its
// canonical manifest and the entity schemas the rule names.
type Fingerprint struct {
	Version      string            `json:"version"`
	ManifestHash string            `json:"manifest_hash"`
	SchemaHashes map[string]string `json:"schema_hashes"`
}

// Approval is one approval row.
type Approval struct {
	ID         int64
	Plugin     string
	Run        string
	Policy     Policy
	FP         Fingerprint
	ApprovedBy string
	ApprovedAt time.Time
}

var (
	// ErrNoApproval means the run has no active approval.
	ErrNoApproval = errors.New("standing: no active approval")
	// ErrStale means an approval exists but no longer matches the installed
	// plugin or its schemas.
	ErrStale = errors.New("standing: approval does not match the installed plugin")
)

// Matches reports whether cur is exactly what was approved: the same version
// and manifest, and the same hash for every schema the policy names. A
// schema missing from cur does not match (fail closed).
func (a Approval) Matches(cur Fingerprint) bool {
	if a.FP.Version != cur.Version || a.FP.ManifestHash != cur.ManifestHash || a.FP.ManifestHash == "" {
		return false
	}
	for _, slug := range a.Policy.Schemas {
		want, ok := a.FP.SchemaHashes[slug]
		got, have := cur.SchemaHashes[slug]
		if !ok || !have || want == "" || want != got {
			return false
		}
	}
	return true
}

// Covers reports whether the policy names the schema slug.
func (p Policy) Covers(slug string) bool { return slices.Contains(p.Schemas, slug) }
