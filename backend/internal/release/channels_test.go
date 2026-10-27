package release

import (
	"slices"
	"testing"
)

// Adding or reordering a channel changes what every site, the admin page and
// the release tool accept, so it must be a deliberate edit here too.
func TestChannelsAreStableEdgeCanary(t *testing.T) {
	if want := []string{"stable", "edge", "canary"}; !slices.Equal(Channels, want) {
		t.Fatalf("Channels = %v, want %v", Channels, want)
	}
}
