package designcheck

import (
	"os"
	"strings"
	"testing"
)

// On a phone the subnav is a horizontal scroller of plain links, not a select with a Go button:
// links navigate only when followed (WCAG 3.2.2), nothing changes on a change event, and the
// list is shown at every width (no second copy of the navigation for a screen reader to meet).
func TestSubnavIsAScrollerOfLinksOnPhones(t *testing.T) {
	t.Parallel()

	css, err := os.ReadFile("../../../web/admin/css/partials/settings.css")
	if err != nil {
		t.Fatal(err)
	}
	s := string(css)
	i := strings.Index(s, "@media (max-width: 640px)")
	if i < 0 {
		t.Fatal("settings.css has no phone block")
	}
	phone := s[i:]
	if !strings.Contains(phone, "flex-direction: row") || !strings.Contains(phone, "overflow-x: auto") {
		t.Error("the phone subnav must be one horizontally scrolling row")
	}
	if strings.Contains(s, ".hm-subnav { display: none; }") || strings.Contains(s, "hm-subnav-select") {
		t.Error("the subnav must not be swapped for a select on phones")
	}
	if strings.Contains(s, ".hm-subnav__link .hm-icon") {
		t.Error("subnav links carry no icons")
	}
}
