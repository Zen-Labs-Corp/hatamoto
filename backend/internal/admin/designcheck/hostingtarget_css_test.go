package designcheck

import (
	"os"
	"strings"
	"testing"
)

// Settings > Hosting shows only the chosen target's fields. The rule hides the
// data-hosting-only wrappers of hosting.html; losing either side brings back
// both field sets (and the "or" divider) on the page.
func TestHostingTargetHideRule(t *testing.T) {
	t.Parallel()

	css := readCSSNormalised(t, "../../../web/admin/css/partials/hostingtarget.css")
	html, err := os.ReadFile("../../../web/admin/pages/settings/hosting.html")
	if err != nil {
		t.Fatal(err)
	}
	const hide = `form:has(input[name="target"][value="dir"]:checked) [data-hosting-only="s3"], ` +
		`form:has(input[name="target"][value="s3"]:checked) [data-hosting-only="dir"] { display: none; }`
	if !strings.Contains(css, hide) {
		t.Errorf("hostingtarget.css lacks the whole hide rule %q", hide)
	}
	for _, kind := range []string{"dir", "s3"} {
		if !strings.Contains(string(html), `data-hosting-only="`+kind+`"`) {
			t.Errorf("hosting.html lacks the data-hosting-only=%q wrapper", kind)
		}
	}
	if strings.Contains(string(html), `<p class="hm-muted">or</p>`) {
		t.Error("hosting.html still has the \"or\" divider between the targets")
	}
}
