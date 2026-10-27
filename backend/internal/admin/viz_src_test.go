package admin

import (
	"os"
	"strings"
	"testing"
)

func readSrc(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// hm-spark sets --viz as a default; a plain `.hm-spark { --viz }` has the same
// specificity as .hm-viz--{kind} and, coming later, would always win, so
// Spark.Kind would do nothing.
func TestSparkDefaultHueDoesNotOverrideKind(t *testing.T) {
	t.Parallel()

	css := readSrc(t, "../../web/admin/css/partials/viz.css")
	for _, line := range strings.Split(css, "\n") {
		if strings.HasPrefix(line, ".hm-spark {") && strings.Contains(line, "--viz:") {
			t.Errorf("viz.css: %q sets --viz at the same specificity as hm-viz--{kind}", line)
		}
	}
	if !strings.Contains(css, `.hm-spark:not([class*="hm-viz--"])`) {
		t.Error("viz.css: the default spark hue must apply only without a hm-viz--{kind} class")
	}
}

// role=progressbar makes its children presentational: the visible value text
// ("120 of 100") must reach assistive tech as aria-valuetext, because
// aria-valuenow is clamped to the bar.
func TestProgressHasValueText(t *testing.T) {
	t.Parallel()

	if !strings.Contains(readSrc(t, "../../web/admin/partials/progress.html"), `aria-valuetext="{{.Value}}"`) {
		t.Error("hm/progress needs aria-valuetext with its visible value")
	}
}

// Tracker.States blocks sit at the bottom of their item (so the warning one can
// be shorter), which collapses a block with no content to 0px unless it is
// given the full height itself.
func TestStatesTrackerBlocksKeepFullHeight(t *testing.T) {
	t.Parallel()

	css := readSrc(t, "../../web/admin/css/partials/viz.css")
	if !strings.Contains(css, ".hm-tracker--states .hm-tracker__block { height: 100%; }") {
		t.Error("viz.css: .hm-tracker--states blocks need height: 100%, or ok/danger blocks collapse to 0px")
	}
}
