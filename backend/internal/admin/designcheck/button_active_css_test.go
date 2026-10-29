package designcheck

import (
	"regexp"
	"strings"
	"testing"
)

// The generic .hm-btn:active rule (0,2,0) outranks a variant's resting rule (0,1,0), so a
// keyboard press (Space holds :active without :hover) once repainted primary and danger
// kasumi with paper text. Every variant that sets its own fill needs its own :active rule.
func TestButtonVariantsHaveActiveRule(t *testing.T) {
	t.Parallel()

	css := cssSource(t, "partials/button.css")
	rest := regexp.MustCompile(`(?m)^(\.hm-btn--[a-z-]+) \{[^}]*\bbackground:`)
	m := rest.FindAllStringSubmatch(css, -1)
	if len(m) < 3 {
		t.Fatalf("expected the primary, ghost and danger variants to set a background, found %d", len(m))
	}
	for _, v := range m {
		if !strings.Contains(css, "\n"+v[1]+":active {") {
			t.Errorf("%s sets a background but has no %s:active rule; .hm-btn:active would repaint it", v[1], v[1])
		}
	}
}
