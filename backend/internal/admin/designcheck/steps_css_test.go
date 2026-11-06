package designcheck

import (
	"strings"
	"testing"
)

// The setup wizard has seven steps; in a narrow card a single nowrap row ran out
// of the card on the right. The row has to be allowed to wrap.
func TestStepsCSSWraps(t *testing.T) {
	t.Parallel()

	css := readCSSNormalised(t, "../../../web/admin/css/partials/steps.css")
	rule := css[strings.Index(css, ".hm-steps {"):]
	rule = rule[:strings.Index(rule, "}")]
	if !strings.Contains(rule, "flex-wrap: wrap") {
		t.Errorf(".hm-steps must wrap so a long wizard stays inside its card: %s", rule)
	}
}

// A ticked toggle chip must differ from an unticked one by more than colour
// (WCAG 1.4.1): it draws a check mark.
func TestToggleChipHasCheckMark(t *testing.T) {
	t.Parallel()

	css := readCSSNormalised(t, "../../../web/admin/css/partials/field.css")
	i := strings.Index(css, ".hm-field__chip:has(:checked)::before {")
	if i < 0 {
		t.Fatal("a checked .hm-field__chip needs a ::before check mark")
	}
	rule := css[i:]
	rule = rule[:strings.Index(rule, "}")]
	if !strings.Contains(rule, `content: ""`) || !strings.Contains(rule, "rotate(45deg)") {
		t.Errorf("the check mark rule draws nothing: %s", rule)
	}
}
