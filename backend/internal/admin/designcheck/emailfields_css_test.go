package designcheck

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The email form shows only the fields of the chosen provider. The rule hides the
// data-email-only wrappers of emailfields.html; losing either side brings back
// the SES region field under Gmail.
func TestEmailFieldsHideRule(t *testing.T) {
	t.Parallel()

	css := readCSSNormalised(t, "../../../web/admin/css/partials/emailfields.css")
	html, err := os.ReadFile("../../../web/admin/partials/emailfields.html")
	if err != nil {
		t.Fatal(err)
	}
	const hide = `form:has(#provider option:checked:not([value="ses"])) [data-email-only="ses"], ` +
		`form:has(#provider option:checked:not([value="custom"])) [data-email-only="custom"] { display: none; }`
	if !strings.Contains(css, hide) {
		t.Errorf("emailfields.css lacks the whole hide rule %q", hide)
	}
	for _, kind := range []string{"ses", "custom"} {
		if !strings.Contains(string(html), `data-email-only="`+kind+`"`) {
			t.Errorf("emailfields.html lacks the data-email-only=%q wrapper", kind)
		}
	}
}

var (
	cssComment = regexp.MustCompile(`(?s)/\*.*?\*/`)
	cssSpace   = regexp.MustCompile(`\s+`)
)

// readCSSNormalised returns the file with comments removed and whitespace collapsed,
// so a rule that is commented out no longer matches.
func readCSSNormalised(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return cssSpace.ReplaceAllString(cssComment.ReplaceAllString(string(b), ""), " ")
}
