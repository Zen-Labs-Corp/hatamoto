package admin

import (
	"os"
	"strings"
	"testing"
)

func partialCSS(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("../../web/admin/css/partials/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Every partial lands in the one global bundle, so the analytics-only rules in
// it must be scoped: other screens' tables and page headers stay as they were.
func TestAnalyticsCSSDoesNotLeakIntoOtherScreens(t *testing.T) {
	t.Parallel()

	aia := partialCSS(t, "aia.css")
	for _, line := range strings.Split(aia, "\n") {
		if strings.Contains(line, ".hm-table--cards") && !strings.Contains(line, ".hm-aia-robots") {
			t.Errorf("aia.css rule %q applies to every hm/table; analytics-only hooks must be named (hm-aia-robots)", strings.TrimSpace(line))
		}
	}
	if !strings.Contains(partialCSS(t, "table.css"), ".hm-table--cards td:empty") {
		t.Error("table.css must hide empty cells in the phone cards")
	}

	header := partialCSS(t, "pageheader.css")
	for _, line := range strings.Split(header, "\n") {
		if strings.Contains(line, "24rem") && !strings.Contains(line, ":has(.hm-daterange)") {
			t.Errorf("pageheader.css rule %q changes every page header; scope it to headers with a daterange", strings.TrimSpace(line))
		}
	}
}

// The analytics CSS reuses the shared pulse keyframes and the global [hidden] rule.
func TestAnalyticsCSSHasNoDuplicates(t *testing.T) {
	t.Parallel()

	if chart := partialCSS(t, "aia-chart.css"); strings.Contains(chart, "@keyframes hm-aia-pulse") || !strings.Contains(chart, "animation: hm-chart-pulse") {
		t.Error("aia-chart.css must reuse hm-chart-pulse instead of its own identical keyframes")
	}
	if strings.Contains(partialCSS(t, "aia.css"), ".hm-aia-lgrow [hidden]") {
		t.Error("aia.css repeats the global [hidden] rule")
	}
}

// On a phone each "Other crawlers" row stacks its Allow/Block under the label;
// left to wrap, a short description let the control sit inline in one row only.
func TestOtherCrawlersRowsStackOnPhones(t *testing.T) {
	t.Parallel()

	aia := partialCSS(t, "aia.css")
	i := strings.Index(aia, "@media (max-width: 719px) {")
	if i < 0 || !strings.Contains(aia[i:], ".hm-aia-other__row { flex-direction: column; align-items: flex-start; }") {
		t.Error("aia.css: the 719px block must stack .hm-aia-other__row in a column")
	}
}

// Every table header is styled once, in table.css; analytics must not restyle .hm-table.
func TestAnalyticsDoesNotRestyleTables(t *testing.T) {
	t.Parallel()

	aia := partialCSS(t, "aia.css")
	for _, line := range strings.Split(aia, "\n") {
		if strings.Contains(line, ".hm-table ") || strings.Contains(line, ".hm-table{") || strings.Contains(line, ".hm-card .hm-table__scroll") {
			if !strings.Contains(line, ".hm-table--cards") {
				t.Errorf("aia.css restyles the shared table: %q", strings.TrimSpace(line))
			}
		}
	}
}

// hm/grid2 replaced the fixed two-column analytics grid; an auto-fit with a bare 300px minimum
// gave three columns on a wide screen and squeezed the chart cards.
func TestGrid2StaysTwoColumns(t *testing.T) {
	for _, line := range strings.Split(partialCSS(t, "chart.css"), "\n") {
		if strings.HasPrefix(line, ".hm-grid2 {") && !strings.Contains(line, "calc(50% - var(--space-4))") {
			t.Errorf("chart.css .hm-grid2 must keep each column at least half the row, got %q", strings.TrimSpace(line))
		}
	}
}
