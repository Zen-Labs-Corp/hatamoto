package designcheck

import (
	"os"
	"strings"
	"testing"
)

// Settings > Hosting shows only the chosen target's fields, and the live origin
// field only for a separate live origin. The rules hide the data-hosting-only
// wrappers of hosting.html; losing either side brings back both field sets (and
// the "or" divider) or an always-visible live origin on the page.
func TestHostingTargetHideRule(t *testing.T) {
	t.Parallel()

	css := readCSSNormalised(t, "../../../web/admin/css/partials/hostingtarget.css")
	html, err := os.ReadFile("../../../web/admin/pages/settings/hosting.html")
	if err != nil {
		t.Fatal(err)
	}
	const hide = `form:has(input[name="target"][value="dir"]:checked) [data-hosting-only="s3"], ` +
		`form:has(input[name="target"][value="dir"]:checked) [data-hosting-only="sftp"], ` +
		`form:has(input[name="target"][value="s3"]:checked) [data-hosting-only="dir"], ` +
		`form:has(input[name="target"][value="s3"]:checked) [data-hosting-only="sftp"], ` +
		`form:has(input[name="target"][value="sftp"]:checked) [data-hosting-only="dir"], ` +
		`form:has(input[name="target"][value="sftp"]:checked) [data-hosting-only="s3"] { display: none; }`
	const hideAuth = `form:has(input[name="sftp_auth"][value="password"]:checked) [data-hosting-only="sftp-key"], ` +
		`form:has(input[name="sftp_auth"][value="key"]:checked) [data-hosting-only="sftp-password"] { display: none; }`
	if !strings.Contains(css, hide) {
		t.Errorf("hostingtarget.css lacks the whole hide rule %q", hide)
	}
	if !strings.Contains(css, hideAuth) {
		t.Errorf("hostingtarget.css lacks the whole SFTP sign-in rule %q", hideAuth)
	}
	const hideLive = `form:has(input[name="live_mode"][value="same"]:checked) [data-hosting-only="live"] { display: none; }`
	if !strings.Contains(css, hideLive) {
		t.Errorf("hostingtarget.css lacks the whole live origin rule %q", hideLive)
	}
	for _, kind := range []string{"dir", "s3", "sftp", "sftp-password", "sftp-key", "live"} {
		if !strings.Contains(string(html), `data-hosting-only="`+kind+`"`) {
			t.Errorf("hosting.html lacks the data-hosting-only=%q wrapper", kind)
		}
	}
	if strings.Contains(string(html), `<p class="hm-muted">or</p>`) {
		t.Error("hosting.html still has the \"or\" divider between the targets")
	}
}
