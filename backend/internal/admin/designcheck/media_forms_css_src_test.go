package designcheck

import (
	"os"
	"strings"
	"testing"
)

func readAdminCSS(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("../../../web/admin/css/pages/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// A summary switched to display:flex loses its native marker, and the rule hit every schemaform
// repeater. The grip must stay inline so the marker survives.
func TestRepeaterGripKeepsTheNativeMarker(t *testing.T) {
	t.Parallel()

	css := readAdminCSS(t, "forms.css")
	if strings.Contains(css, "__summary:has(") {
		t.Error("forms.css must not change the display of .hm-repeater__summary")
	}
}

// The builder hides the native marker, so a row needs its own chevron that turns when open.
func TestFormBuilderRowHasExpandIndicator(t *testing.T) {
	t.Parallel()

	css := readAdminCSS(t, "forms.css")
	if !strings.Contains(css, ".hm-fieldlist__chev") || !strings.Contains(css, ".hm-fieldlist__row[open] > summary .hm-fieldlist__chev") {
		t.Error("builder row summary lacks an open/closed indicator")
	}
}

// The dialog body already scrolls; a second scroll box on .hm-picker clips tile focus rings.
func TestPickerDoesNotClipFocusRings(t *testing.T) {
	t.Parallel()

	for _, line := range strings.Split(readAdminCSS(t, "media.css"), "\n") {
		if strings.HasPrefix(line, ".hm-picker {") && strings.Contains(line, "overflow") {
			t.Errorf("picker must not set overflow: %s", line)
		}
	}
}

// Phone table cards name each cell with its visible data-label and end without a stray dot.
func TestTableCardsShowLabelsWithoutTrailingDot(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile("../../../web/admin/css/partials/table.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(b)
	if strings.Contains(css, `\00b7`) {
		t.Error("table.css must not draw a dot between card cells")
	}
	i := strings.Index(css, "td[data-label]::before")
	if i < 0 {
		t.Fatal("table.css has no data-label rule for cards")
	}
	rule := css[i : i+strings.Index(css[i:], "}")]
	if !strings.Contains(rule, "attr(data-label)") || strings.Contains(rule, "clip-path") || strings.Contains(rule, "absolute") {
		t.Errorf("the data-label must be visible, got %s", rule)
	}
}

// A hidden tooltip must have no box: an invisible but laid-out ::after widened phone pages.
func TestTooltipHiddenHasNoBox(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile("../../../web/admin/css/partials/tooltip.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(b)
	i := strings.Index(css, "[data-tooltip]::after {")
	if i < 0 {
		t.Fatal("tooltip.css has no ::after rule")
	}
	rest := css[i : i+strings.Index(css[i:], "}")]
	if !strings.Contains(rest, "display: none") || strings.Contains(rest, "visibility") {
		t.Errorf("the resting tooltip must be display: none, got %s", rest)
	}
	if !strings.Contains(rest, "max-width: min(") {
		t.Error("a shown tooltip must be clamped to the viewport")
	}
}

// A selected media tile has one look: the library tile and the picker tile share the ink ring.
func TestPickerTileSelectedRingMatchesLibraryTile(t *testing.T) {
	ring := func(css, selector string) string {
		for _, line := range strings.Split(css, "\n") {
			if strings.HasPrefix(line, selector+" {") {
				return line[strings.Index(line, "{"):strings.Index(line, "}")]
			}
		}
		t.Fatalf("no %s rule", selector)
		return ""
	}
	lib, err := os.ReadFile("../../../web/admin/css/partials/mediatile.css")
	if err != nil {
		t.Fatal(err)
	}
	want := ring(string(lib), ".hm-mediatile--selected")
	if got := ring(readAdminCSS(t, "media.css"), `.hm-pickertile[aria-pressed="true"]`); got != want {
		t.Errorf("picker selected ring %q differs from the library tile's %q", got, want)
	}
}

// A table's scroll box clips an absolutely positioned tooltip, so inside it the tip opens above the
// control from its right edge (641px and up), and a card-mode table (no sideways scroll, 640px and
// below) does not clip and hangs the tip from the card row so it stays on screen.
func TestTooltipInsideATableIsNotClipped(t *testing.T) {
	b, err := os.ReadFile("../../../web/admin/css/partials/tooltip.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(b)
	for _, want := range []string{
		"top: auto; bottom: calc(100% + 6px); left: auto; right: 0; transform: none;",
		"@media (min-width: 40.0625rem) { .hm-table__scroll [data-tooltip]::after {",
		".hm-table__scroll:has(> .hm-table--cards [data-tooltip]) { overflow: visible; }",
		".hm-table--cards [data-tooltip]::after { left: 0; right: auto; transform: none; max-width: min(20rem, 100%); }",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("tooltip.css lacks %q", want)
		}
	}
}
