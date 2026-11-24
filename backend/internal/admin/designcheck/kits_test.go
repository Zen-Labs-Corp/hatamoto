package designcheck

import (
	"regexp"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/comment"
)

// The kit review partial is shared by the wizard and Settings > Kits. It uses switches only, takes
// every warning from Go fields (the comment copy), starts with the CSRF field and never disables
// Apply in markup: the server enforces the comments tick (Decision 3).
func TestKitReviewPartial(t *testing.T) {
	t.Parallel()

	src := readWeb(t, "partials/kit-review.html")
	if strings.Contains(src, `type="checkbox"`) || regexp.MustCompile(`"Control" "checkbox"`).MatchString(src) {
		t.Error("kit-review has a checkbox: use hm/field Control \"switch\"")
	}
	for _, lit := range []string{comment.EnableWarning, comment.EnableConfirmWarning, comment.EnableTick, "invite spam"} {
		if strings.Contains(src, lit) {
			t.Errorf("kit-review has the literal warning %q: print the Go field", lit)
		}
	}
	if strings.Contains(src, "data-hm-ack") {
		t.Error("kit-review uses data-hm-ack: Apply is never JS-gated here")
	}
	csrf, sw := strings.Index(src, `template "hm/csrf"`), strings.Index(src, `"Control" "switch"`)
	if csrf < 0 || csrf > sw {
		t.Error("hm/csrf must come before the first control")
	}
	for _, line := range strings.Split(src, "\n") {
		if strings.Contains(line, `"Apply kit"`) && regexp.MustCompile(`(?i)disabled`).MatchString(line) {
			t.Errorf("the Apply button is disabled in the template: %s", line)
		}
	}
}
