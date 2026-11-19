package server

import (
	"slices"
	"testing"
)

func TestPermissionsTabListsKeptEntityFilterGrant(t *testing.T) {
	asked := []string{"kv"}
	if got := withKeptFilterGrant(asked, []string{"kv", "entities.filter"}); !slices.Equal(got, []string{"kv", "entities.filter"}) {
		t.Errorf("kept grant not listed: %v", got)
	}
	if len(asked) != 1 {
		t.Errorf("the input slice was changed: %v", asked)
	}
	if got := withKeptFilterGrant(asked, []string{"kv"}); !slices.Equal(got, asked) {
		t.Errorf("not granted: %v", got)
	}
	both := []string{"entities.filter", "kv"}
	if got := withKeptFilterGrant(both, []string{"entities.filter"}); !slices.Equal(got, both) {
		t.Errorf("asked and granted must not repeat: %v", got)
	}
}
