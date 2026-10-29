package designcheck

import (
	"strings"
	"testing"
)

// hm-spark sets --viz as a default; a plain `.hm-spark { --viz }` has the same
// specificity as .hm-viz--{kind} and, coming later, would always win, so
// Spark.Kind would do nothing.
func TestSparkDefaultHueDoesNotOverrideKind(t *testing.T) {
	t.Parallel()

	css := readSrc(t, "../../../web/admin/css/partials/viz.css")
	for _, line := range strings.Split(css, "\n") {
		if strings.HasPrefix(line, ".hm-spark {") && strings.Contains(line, "--viz:") {
			t.Errorf("viz.css: %q sets --viz at the same specificity as hm-viz--{kind}", line)
		}
	}
	if !strings.Contains(css, `.hm-spark:not([class*="hm-viz--"])`) {
		t.Error("viz.css: the default spark hue must apply only without a hm-viz--{kind} class")
	}
}

// Tracker.States blocks sit at the bottom of their item (so the warning one can
// be shorter), which collapses a block with no content to 0px unless it is
// given the full height itself.
func TestStatesTrackerBlocksKeepFullHeight(t *testing.T) {
	t.Parallel()

	css := readSrc(t, "../../../web/admin/css/partials/viz.css")
	if !strings.Contains(css, ".hm-tracker--states .hm-tracker__block { height: 100%; }") {
		t.Error("viz.css: .hm-tracker--states blocks need height: 100%, or ok/danger blocks collapse to 0px")
	}
}
