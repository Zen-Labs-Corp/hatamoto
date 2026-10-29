package designcheck

import (
	"os"
	"strings"
	"testing"
)

// The sidebar list and the phone select are never both exposed: each is
// display:none where the other shows, so a screen reader meets one.
func TestSettingsNavAndSelectAreNeverBothShown(t *testing.T) {
	t.Parallel()

	css, err := os.ReadFile("../../../web/admin/css/partials/settings.css")
	if err != nil {
		t.Fatal(err)
	}
	s := string(css)
	if !strings.Contains(s, ".hm-subnav-select { display: none; }") {
		t.Error("the select is not hidden on wide screens")
	}
	if !strings.Contains(s, "@media (max-width: 640px)") || !strings.Contains(s, "  .hm-subnav { display: none; }") {
		t.Error("the sidebar list is not hidden at the phone width where the select shows")
	}
}
