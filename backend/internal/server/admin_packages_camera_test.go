package server

import (
	"testing"

	"hatamoto.dev/sdk"
)

// The page's camera needs both the manifest ask and the owner's grant.
func TestUIInfoCameraNeedsAskAndGrant(t *testing.T) {
	t.Parallel()
	p := &packageAdmin{}
	for _, tc := range []struct {
		ask, grant, want bool
	}{{false, false, false}, {true, false, false}, {false, true, false}, {true, true, true}} {
		ui := &sdk.AdminUI{Title: "t"}
		if tc.ask {
			ui.Allow = []string{sdk.UIAllowCamera}
		}
		var granted []string
		if tc.grant {
			granted = []string{sdk.PermUICamera}
		}
		if got := p.uiInfo(ui, granted).Camera; got != tc.want {
			t.Errorf("ask=%v grant=%v: camera=%v, want %v", tc.ask, tc.grant, got, tc.want)
		}
	}
}
