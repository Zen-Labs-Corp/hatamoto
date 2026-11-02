package designcheck

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// Phones keep a trail: the top bar must never be hidden at the phone breakpoint, because
// it is the only place a page shows where it sits (and a way back to the parent).
func TestTopbarSurvivesPhones(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile("../../../web/admin/css/base.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(b)
	if regexp.MustCompile(`\.hm-topbar\s*\{[^}]*display:\s*none`).MatchString(css) {
		t.Error("base.css hides .hm-topbar: phones would have no breadcrumb trail")
	}
	if !strings.Contains(css, ".hm-topbar__crumbs li:nth-last-child(2)") {
		t.Error("base.css has no compact phone trail (back link to the parent crumb)")
	}
}
