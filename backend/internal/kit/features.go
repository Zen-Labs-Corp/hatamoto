package kit

import "slices"

// KnownFeatures are the core feature names a kit may require (docs/KITS.md).
var KnownFeatures = []string{"comment.policy", "comment.rank", "comment.signin", "plugin.entity-write"}

// present lists the features this build has. A name is added only by the change
// that makes it work: comment.policy is the per-type community policy (Settings >
// Comments > Content types), which a kit turns on through Deps.SchemaComments;
// comment.rank is the rank sort of a comment type (order "rank"), which orders
// every sibling level by the rank a plugin writes.
var present = []string{"comment.policy", "comment.rank"}

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
