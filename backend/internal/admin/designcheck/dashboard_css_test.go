package designcheck

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// At the widest breakpoint the first row (health and its cards) and the
// Activity row each add up to the 24 columns, whichever cards are missing.
func TestDashboardGridFillsTheRow(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile("../../../web/admin/css/pages/dashboard.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(b)
	num := func(re string) int {
		m := regexp.MustCompile(re).FindStringSubmatch(css)
		if m == nil {
			t.Fatalf("dashboard.css lacks %s", re)
		}
		n, _ := strconv.Atoi(m[1])
		return n
	}
	healthSpan := num(`#dash-health \{ grid-column: span (\d+);`)
	for _, c := range []struct {
		health bool
		n      int
		card   string
		act    string
	}{
		{false, 3, `\.hm-dash--n3 \{ --dash-card: (\d+);`, `--dash-activity: (\d+); \}`},
		{false, 2, `\.hm-dash--n2 \{ --dash-card: (\d+);`, `--dash-activity: (\d+); \}`},
		{false, 1, `\.hm-dash--n1 \{ --dash-card: (\d+);`, `--dash-activity: (\d+); \}`},
		{true, 3, `\.hm-dash--health\.hm-dash--n3 \{ --dash-card: (\d+);`, `\.hm-dash--health \{ --dash-activity: (\d+);`},
		{true, 2, `\.hm-dash--health\.hm-dash--n2 \{ --dash-card: (\d+);`, `\.hm-dash--health \{ --dash-activity: (\d+);`},
		{true, 1, `\.hm-dash--health\.hm-dash--n1 \{ --dash-card: (\d+);`, `\.hm-dash--health \{ --dash-activity: (\d+);`},
	} {
		row := c.n * num(c.card)
		if c.health {
			row += healthSpan
		}
		if row != 24 {
			t.Errorf("health=%v n=%d: first row spans %d of 24 columns", c.health, c.n, row)
		}
		act := num(c.act)
		if c.health {
			act += healthSpan
		}
		if act != 24 {
			t.Errorf("health=%v: activity row spans %d of 24 columns", c.health, act)
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
