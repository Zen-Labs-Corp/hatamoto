package admin

import (
	"testing"

	"hatamoto.dev/core/internal/admin/ui"
)

func TestOptGroup(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		in   any
		want string
	}{
		"ui option":     {ui.Option{Value: "a", Group: "Site"}, "Site"},
		"ui pointer":    {&ui.Option{Group: "Theme"}, "Theme"},
		"dict":          {map[string]any{"Value": "a", "Group": "G"}, "G"},
		"dict no group": {map[string]any{"Value": "a"}, ""},
		"other struct":  {struct{ Value string }{"a"}, ""},
		"ungrouped":     {ui.Option{Value: "a"}, ""},
		"nil":           {nil, ""},
	} {
		if got := optGroup(tc.in); got != tc.want {
			t.Errorf("%s: optGroup = %q, want %q", name, got, tc.want)
		}
	}
}
