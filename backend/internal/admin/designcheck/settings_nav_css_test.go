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

// The subnav is docked: sticky at the top of the column, a hairline on its right; the phone scroller marks
// the current link with a border (forced-colors keeps it, unlike box-shadow) and shows edge shadows (no mask: TestTabRowsHaveNoEdgeFade).
func TestSubnavStickyAndPhoneScroller(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile("../../../web/admin/css/partials/settings.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(b)
	for _, want := range []string{"position: sticky; top: 0;", "max-height: 100dvh; overflow-y: auto;", "calc(var(--subnav-w) - 1px) 0 / 1px 100% no-repeat", "no-repeat scroll", "border-bottom-color: var(--text)"} {
		if !strings.Contains(css, want) {
			t.Errorf("settings.css lacks %q", want)
		}
	}
	// Forced colors paint transparent borders, so the links' border must be Canvas there and only the current one Highlight.
	forced := css[strings.LastIndex(css, "@media (forced-colors: active)"):]
	for _, want := range []string{".hm-subnav__link { border-bottom-color: Canvas; }", `.hm-subnav__link[aria-current="page"] { border-bottom-color: Highlight; }`} {
		if !strings.Contains(forced, want) {
			t.Errorf("the forced-colors block lacks %q: every link would show a marker", want)
		}
	}
	// The hairline is the grid's background, not the subnav's border or min-height: a subnav sized for the unscrolled
	// state (100vh minus the top bar) stops 48px short of the bottom once the top bar has scrolled away.
	desktop := css[:strings.Index(css, "@media (max-width: 960px)")]
	// Forced colors drop the gradient, so only there the subnav draws the hairline as a real border.
	const forcedLine = "@media (forced-colors: active) and (min-width: 641px) { .hm-subnav { border-right: 1px solid CanvasText; } }"
	if !strings.Contains(desktop, forcedLine) {
		t.Errorf("settings.css lacks %q: the docked line vanishes in forced-colors mode", forcedLine)
	}
	desktop = strings.Replace(desktop, forcedLine, "", 1)
	if strings.Contains(desktop, "border-right") || strings.Contains(desktop, ".hm-subnav { position: sticky; top: 0; min-height") {
		t.Error("the docked hairline must not hang on the sticky subnav's border or min-height")
	}
	phone := css[strings.Index(css, "@media (max-width: 640px)"):]
	if !strings.Contains(phone, "padding: var(--space-2) var(--space-4) 0;") {
		t.Error("the phone scroller must be inset like the page below it (16px sides)")
	}
	if !strings.Contains(phone, "background: none;") {
		t.Error("the phone grid must drop the docked hairline (the scroller has its own bottom border)")
	}
	if !strings.Contains(phone, "max-height: none") {
		t.Error("the phone subnav must drop the desktop max-height")
	}
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

// The Colours swatch cells share one top edge: a note under Secondary or Theme accent makes that cell
// taller, so the row must pack its tracks at the start (not centre them) and the note sits in its own row.
func TestSwatchRowsShareATopEdge(t *testing.T) {
	t.Parallel()

	css, err := os.ReadFile("../../../web/admin/css/partials/settings.css")
	if err != nil {
		t.Fatal(err)
	}
	s := string(css)
	for _, want := range []string{".hm-swatchrow { display: grid;", "align-content: start;", ".hm-swatchrow__note { grid-column: 2; grid-row: 3;"} {
		if !strings.Contains(s, want) {
			t.Errorf("settings.css lacks %q: swatch cells would start at different heights", want)
		}
	}
}
