package designcheck

import (
	"regexp"
	"strings"
	"testing"
)

// In forced colors the resting edge is a 1px ButtonText outline. When its selector can match
// :focus-visible it competes with the keyboard ring, and a later or equally specific edge rule
// erases the ring. Each edge selector must exclude :focus-visible, or a later
// "<base>:focus-visible" rule in the same file must restore the Highlight ring.
func TestForcedColorsEdgeKeepsFocusRing(t *testing.T) {
	t.Parallel()

	rule := regexp.MustCompile(`(?m)^\s*([^{}\n]+?) \{ ([^}\n]*)\}`)
	base := regexp.MustCompile(`^\.hm-(?:btn|bs-opt)\b`)
	for _, file := range []string{"partials/button.css", "pages/builderinspector.css"} {
		css := cssSource(t, file)
		i := strings.Index(css, "@media (forced-colors: active)")
		if i < 0 {
			continue
		}
		block := css[i:]
		checked := 0
		for _, m := range rule.FindAllStringSubmatchIndex(block, -1) {
			sels, body := block[m[2]:m[3]], block[m[4]:m[5]]
			if !strings.Contains(body, "outline: 1px solid ButtonText") {
				continue
			}
			for _, sel := range splitSelectors(sels) {
				sel = strings.TrimSpace(sel)
				b := base.FindString(sel)
				if b == "" {
					continue
				}
				checked++
				if strings.Contains(sel, ":focus-visible") {
					continue // it excludes :focus-visible (":not(:focus-visible)") or restores the ring itself
				}
				later := block[m[1]:]
				if !regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(b) + `:focus-visible \{[^}]*Highlight`).MatchString(later) {
					t.Errorf("%s: forced-colors edge selector %q can match :focus-visible and no later %s:focus-visible rule restores the Highlight ring", file, sel, b)
				}
			}
		}
		if checked == 0 {
			t.Errorf("%s: found no forced-colors edge rule to check", file)
		}
	}
}

// splitSelectors splits a selector list on top-level commas, keeping :not(a, b) whole.
func splitSelectors(list string) []string {
	var out []string
	depth, start := 0, 0
	for i, r := range list {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, list[start:i])
				start = i + 1
			}
		}
	}
	return append(out, list[start:])
}
