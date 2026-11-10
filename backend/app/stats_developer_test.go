package app

import (
	"testing"

	"hatamoto.dev/core/internal/packages"
)

func TestDeveloperPackagesAreNotFirstParty(t *testing.T) {
	for o, want := range map[packages.Origin]bool{
		packages.OriginSelfBuilt:  false,
		packages.OriginFirstParty: true,
		packages.OriginCommunity:  true,
		packages.OriginDeveloper:  false,
	} {
		if got := isFirstPartyOrigin(o); got != want {
			t.Errorf("isFirstPartyOrigin(%v) = %v, want %v", o, got, want)
		}
	}
}
