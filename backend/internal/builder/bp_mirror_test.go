package builder

import (
	"os"
	"regexp"
	"strconv"
	"testing"
)

// The builder screen shows each device's width in its label; bp.js keeps its own copy of the
// breakpoint widths, so this fails when the two drift apart.
func TestBreakpointMaxMirrorsBPJS(t *testing.T) {
	t.Parallel()

	src, err := os.ReadFile("../../web/admin/js/builder/bp.js")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`BREAKPOINT_MAX = \{ md: (\d+), sm: (\d+) \}`).FindSubmatch(src)
	if m == nil {
		t.Fatal("bp.js has no BREAKPOINT_MAX = { md: N, sm: N }")
	}
	for i, name := range []string{"md", "sm"} {
		got, _ := strconv.Atoi(string(m[i+1]))
		if want := breakpointMaxWidth[name]; got != want {
			t.Errorf("BREAKPOINT_MAX.%s = %d in bp.js, breakpointMaxWidth[%q] = %d in style.go", name, got, name, want)
		}
	}
}
