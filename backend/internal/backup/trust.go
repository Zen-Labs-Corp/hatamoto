package backup

import (
	"sort"

	"hatamoto.dev/core/internal/packages"
	"hatamoto.dev/core/internal/release"
)

// TrustIDs are the key IDs a backup records: the release and manifest keys
// embedded in the binary that made it, and the operator's own keys.
type TrustIDs struct {
	Release  []string `json:"release,omitempty"`
	Manifest []string `json:"manifest,omitempty"`
	Env      []string `json:"env,omitempty"`
}

// embeddedTrust is the default Options.Trust: the IDs embedded in this binary.
// The app adds the env key IDs, which this package never reads itself.
func embeddedTrust() (TrustIDs, error) {
	return TrustIDs{
		Release:  sortedKeys(release.ReleaseTrust()),
		Manifest: sortedKeys(release.ManifestTrust()),
	}, nil
}

func sortedKeys(s packages.TrustSet) []string {
	ids := make([]string, 0, len(s))
	for id := range s {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
