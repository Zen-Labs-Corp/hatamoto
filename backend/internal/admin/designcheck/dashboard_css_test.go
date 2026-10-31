package designcheck

import (
	"os"
	"strings"
	"testing"
)

// The cards sit in two independent columns (no shared grid rows, so a short card leaves no
// gap in the other column) that stack on narrow screens.
func TestDashboardColumnsAreIndependent(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile("../../../web/admin/css/pages/dashboard.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(b)
	for _, want := range []string{
		".hm-dash--cols { grid-template-columns: repeat(2, minmax(0, 1fr)); align-items: start; }",
		".hm-dash--one { grid-template-columns: minmax(0, 1fr); }",
		".hm-dash__col { display: grid; gap: var(--space-4);",
		"@media (max-width: 1099px) { .hm-dash--cols { grid-template-columns: minmax(0, 1fr); } }",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("dashboard.css lacks %q", want)
		}
	}
	for _, gone := range []string{"repeat(24", "--dash-card", "grid-row: span"} {
		if strings.Contains(css, gone) {
			t.Errorf("dashboard.css still has %q: the cards are no longer one shared grid", gone)
		}
	}
}

// Activity rows keep a target column on desktop, but on phones they stack:
// the fixed desktop tracks would squeeze the target to zero width. The badge
// must not stretch to its track either.
func TestDashboardActivityRowsStackOnPhones(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile("../../../web/admin/css/pages/dashboard.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(b)
	_, phone, ok := strings.Cut(css, "@media (max-width: 640px) {\n  /* Phones")
	if !ok {
		t.Fatal("dashboard.css has no phone rule for the Activity rows")
	}
	for _, want := range []string{"#dash-activity .hm-dash__row { grid-template-columns: minmax(0, 1fr) auto;", "white-space: normal", "> .hm-badge { grid-column: 1 / -1;", "grid-row: 3; overflow: visible"} {
		if !strings.Contains(phone, want) {
			t.Errorf("phone rule lacks %q", want)
		}
	}
	if !strings.Contains(css, "#dash-activity .hm-dash__row > .hm-badge { justify-self: start; max-width: 100%;") {
		t.Error("the Activity kind badge stretches to its grid track")
	}
}
