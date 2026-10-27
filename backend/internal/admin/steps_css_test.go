package admin

import (
	"strings"
	"testing"
)

// The setup wizard has seven steps; in a narrow card a single nowrap row ran out
// of the card on the right. The row has to be allowed to wrap.
func TestStepsCSSWraps(t *testing.T) {
	t.Parallel()

	css := readCSSNormalised(t, "../../web/admin/css/partials/steps.css")
	rule := css[strings.Index(css, ".hm-steps {"):]
	rule = rule[:strings.Index(rule, "}")]
	if !strings.Contains(rule, "flex-wrap: wrap") {
		t.Errorf(".hm-steps must wrap so a long wizard stays inside its card: %s", rule)
	}
}
