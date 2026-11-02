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

// The sticky subnav scrolls inside itself and sits at the group padding; the phone scroller marks
// the current link with a border (forced-colors keeps it, unlike box-shadow) and shows edge shadows (no mask: TestTabRowsHaveNoEdgeFade).
func TestSubnavStickyAndPhoneScroller(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile("../../../web/admin/css/partials/settings.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(b)
	for _, want := range []string{"top: var(--page-pad);", "max-height: calc(100dvh - 2 * var(--page-pad)); overflow-y: auto", "no-repeat scroll", "border-bottom-color: var(--text)"} {
		if !strings.Contains(css, want) {
			t.Errorf("settings.css lacks %q", want)
		}
	}
	// Forced colors paint transparent borders, so the links' border must be Canvas there and only the current one Highlight.
	forced := css[strings.Index(css, "@media (forced-colors: active)"):]
	for _, want := range []string{".hm-subnav__link { border-bottom-color: Canvas; }", `.hm-subnav__link[aria-current="page"] { border-bottom-color: Highlight; }`} {
		if !strings.Contains(forced, want) {
			t.Errorf("the forced-colors block lacks %q: every link would show a marker", want)
		}
	}
	phone := css[strings.Index(css, "@media (max-width: 640px)"):]
	if strings.Contains(phone, "box-shadow") {
		t.Error("the phone subnav must not mark the current link with box-shadow (lost in forced-colors)")
	}
}

// The subnav is centred by setting the scroller's own scrollLeft: scrollIntoView would also scroll the window
// (and undo a #fragment position after a save redirect).
func TestSubnavCentresWithoutScrollingWindow(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile("../../../web/admin/js/admin.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(b)
	if strings.Contains(js, "scrollIntoView") {
		t.Error("admin.js must not use scrollIntoView: it scrolls the window")
	}
	if !strings.Contains(js, "subnav.scrollLeft") {
		t.Error("admin.js must centre the current subnav link by setting the scroller's scrollLeft")
	}
}

// The rail's tooltip is a native title, which CSS cannot hide, so the collapse handler sets it only while collapsed.
func TestRailTitlesOnlyWhileCollapsed(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile("../../../web/admin/js/admin.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(b)
	for _, want := range []string{"railTitles(true)", "railTitles(collapsed)", `removeAttribute("title")`} {
		if !strings.Contains(js, want) {
			t.Errorf("admin.js lacks %q", want)
		}
	}
	h, err := os.ReadFile("../../../web/admin/partials/sidebar.html")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(h), `class="hm-nav-link" href="{{.Href}}" title=`) {
		t.Error("sidebar.html must not carry a static title on nav links")
	}
}
