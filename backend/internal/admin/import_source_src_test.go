package admin

import (
	"os"
	"strings"
	"testing"
)

// The source page submits from inside its cards, and the redirect form keeps
// the precedence rule that used to sit in a paragraph on the list page.
func TestImportSourceAndRedirectFormSource(t *testing.T) {
	t.Parallel()

	src, err := os.ReadFile("../../web/admin/pages/import/source.html")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(src), `"hm/foot"`) || strings.Count(string(src), `"hm/card-foot"`) != 2 {
		t.Error("source.html: the submit belongs in each card's footer, not a bare hm/foot")
	}
	form, err := os.ReadFile("../../web/admin/partials/form-redirect.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(form), "Exact rules win over wildcard rules.") {
		t.Error("form-redirect.html lost the exact-over-wildcard precedence sentence")
	}
}
