package designcheck

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// builderCSS is css/pages/builder.css as written (the layout rules the QA harness checks in a browser).
func builderCSS(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "web", "admin", "css", "pages", "builder.css"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// ruleBody is the declarations of the first rule whose selector list is exactly sel (media blocks included).
func ruleBody(t *testing.T, css, sel string) string {
	t.Helper()
	m := regexp.MustCompile(`(?m)(?:^|[}\s])` + regexp.QuoteMeta(sel) + `\s*\{([^}]*)\}`).FindStringSubmatch(css)
	if m == nil {
		t.Fatalf("no rule %q in builder.css", sel)
	}
	return m[1]
}

// The empty-page start panel sits inside the canvas; on a phone the Navigator and Add sheet is fixed over the page at
// --z-sticky. A start panel at the same level painted over the sheet (it comes later in the page), so the Add tiles
// could not be tapped on an empty page.
func TestBuilderStartPanelStaysUnderPhoneSheets(t *testing.T) {
	t.Parallel()

	css := builderCSS(t)
	start := ruleBody(t, css, ".hm-bs__start")
	if !regexp.MustCompile(`z-index:\s*var\(--z-raised\)`).MatchString(start) {
		t.Errorf(".hm-bs__start must be --z-raised, under the phone sheets (--z-sticky): %s", strings.TrimSpace(start))
	}
}

// The shared segmented control wraps when its bar is full, which stacked Desktop, Tablet and Phone in a column over the
// canvas (1440px) and squeezed the page title and the status chip to one character per line (390px). The device switch
// stays one row and the status chip one line. On a phone the bar wraps: Undo and the device switch take a second row at
// their shared sizes (the consistency contract forbids shrinking the segmented control or the chip), and the chip is its
// dot with its words kept for screen readers.
func TestBuilderTopBarStaysOneRow(t *testing.T) {
	t.Parallel()

	css := builderCSS(t)
	if body := ruleBody(t, css, ".hm-bs__devices .hm-segmented"); !strings.Contains(body, "flex-wrap: nowrap") {
		t.Errorf("the device switch must not wrap: %s", strings.TrimSpace(body))
	}
	if body := ruleBody(t, css, ".hm-bs__devices"); !strings.Contains(body, "flex: none") {
		t.Errorf("the device group must not shrink: %s", strings.TrimSpace(body))
	}
	if body := ruleBody(t, css, ".hm-editor-bar:has([data-hm-bs-title]) .hm-editor-bar__state"); !strings.Contains(body, "white-space: nowrap") {
		t.Errorf("the status chip must stay on one line: %s", strings.TrimSpace(body))
	}
	if body := ruleBody(t, css, ".hm-editor-bar:has([data-hm-bs-title]) .hm-savestate__text"); !strings.Contains(body, "clip-path") {
		t.Errorf("on a phone the chip text is visually hidden, not removed: %s", strings.TrimSpace(body))
	}
	for _, row := range []string{".hm-bs__tools", ".hm-bs__devices"} {
		if body := ruleBody(t, css, ".hm-editor-bar:has([data-hm-bs-title]) "+row); !strings.Contains(body, "order: 1") {
			t.Errorf("on a phone Undo and the device switch take their own row (%s): %s", row, strings.TrimSpace(body))
		}
	}
	for _, shared := range []string{".hm-segmented__btn", ".hm-savestate {"} {
		if strings.Contains(css, shared) {
			t.Errorf("builder.css must not restyle the shared %s (UI-D14)", shared)
		}
	}
}

// Ctrl+K rows show their hint in the muted grey; on the hovered or active row (surface-hover) that grey is under 4.5:1
// (axe color-contrast in builder-ux-tasks), so the hint takes the body colour inside a row.
func TestBuilderCommandHintReadsOnActiveRow(t *testing.T) {
	t.Parallel()

	body := ruleBody(t, builderCSS(t), ".hm-bs-cmdk__o .hm-bs-cmdk__h")
	if !strings.Contains(body, "color: var(--text-body)") {
		t.Errorf("the palette hint must use --text-body inside a row: %s", strings.TrimSpace(body))
	}
}

// UI-D11 (consistency.mjs): a field border at rest is the soft --border-field, never the firm --border-control. In dark
// both tokens had been set to the same grey, so every input on every screen failed the rule; light keeps them apart.
func TestDarkFieldBorderStaysSofterThanControl(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile(filepath.Join("..", "..", "..", "web", "admin", "css", "tokens.css"))
	if err != nil {
		t.Fatal(err)
	}
	field := regexp.MustCompile(`--border-field:\s*([^;]+);`).FindAllStringSubmatch(string(b), -1)
	control := regexp.MustCompile(`--border-control:\s*([^;]+);`).FindAllStringSubmatch(string(b), -1)
	if len(field) < 3 || len(field) != len(control) {
		t.Fatalf("expected light, dark and auto-dark values of both tokens, got %d and %d", len(field), len(control))
	}
	for i := range field {
		if strings.EqualFold(field[i][1], control[i][1]) {
			t.Errorf("--border-field and --border-control are both %s in block %d: inputs would not read as calm at rest", field[i][1], i+1)
		}
	}
}

// A full top bar (the device chip beside the switch) squeezed the More button to nothing under the chip, because every
// item could shrink. Only the title shrinks.
func TestBuilderTopBarOnlyTheTitleShrinks(t *testing.T) {
	t.Parallel()

	css := builderCSS(t)
	if body := ruleBody(t, css, ".hm-editor-bar:has([data-hm-bs-title]) > :not(.hm-editor-bar__title)"); !strings.Contains(body, "flex-shrink: 0") {
		t.Errorf("every bar item but the title must keep its size: %s", strings.TrimSpace(body))
	}
}
