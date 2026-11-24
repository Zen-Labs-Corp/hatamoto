package kit

import (
	"slices"
	"strings"

	"hatamoto.dev/core/internal/packages"
	"hatamoto.dev/core/internal/permtext"
)

// ownerTick reports the permissions no kit grants by itself: every permission
// core words as a warning (browser scripts, a way out to the internet, seeing
// every draft, visitor keys, ...) and the UI features each need the owner's
// separate tick.
func ownerTick(p string) bool {
	return strings.HasPrefix(p, "ui.allow:") || permtext.Lookup(p).Warn
}

// GrantFor applies a kit's plugins[].grant to the permissions the plugin asks
// for (docs/KITS.md). granted is what the kit grants on its own, extra every
// asked permission left for the owner to tick (always including every
// permission ownerTick names), notAsked the listed names the plugin does not ask for.
func GrantFor(asked []string, g packages.KitGrant) (granted, extra, notAsked []string) {
	for _, p := range asked {
		ok := !ownerTick(p) && (g.All || slices.Contains(g.Perms, p))
		if ok {
			granted = append(granted, p)
		} else {
			extra = append(extra, p)
		}
	}
	for _, p := range g.Perms {
		if !slices.Contains(asked, p) {
			notAsked = append(notAsked, p)
		}
	}
	return granted, extra, notAsked
}
