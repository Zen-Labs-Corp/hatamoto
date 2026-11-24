package kit

import "slices"

// KnownFeatures are the core feature names a kit may require (docs/KITS.md).
var KnownFeatures = []string{"comment.policy", "comment.rank", "comment.signin", "plugin.entity-write"}

// present lists the features this build has. The part that ships a feature
// adds its name here; a name is never listed before it works.
var present = []string{}

// Features returns the feature names this core has.
func Features() []string { return slices.Clone(present) }

// Missing returns the names in requires that this core does not have, in the
// order given.
func Missing(requires []string) []string {
	var out []string
	for _, r := range requires {
		if !slices.Contains(present, r) && !slices.Contains(out, r) {
			out = append(out, r)
		}
	}
	return out
}
