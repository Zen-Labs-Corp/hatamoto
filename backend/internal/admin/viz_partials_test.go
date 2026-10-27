package admin

import (
	"strings"
	"testing"

	"hatamoto.dev/core/internal/admin/ui"
)

func TestPartialBarList(t *testing.T) {
	t.Parallel()

	a := newPartialAdmin(t)
	s := checkPartial(t, a, "hm/barlist", "default", ui.BarList{
		Label: "Referrals by assistant", NameHeading: "Assistant", ValueHeading: "Visits",
		Items: []ui.BarItem{
			{Label: "ChatGPT", Value: "1,204", N: 1204, Href: "/admin/analytics/referrals?source=chatgpt"},
			{Label: "Perplexity", Value: "602", N: 602},
			{Label: "Claude", Value: "3", N: 3},
			{Label: "Gemini", Value: "0", N: 0},
		},
	})
	for _, want := range []string{`width="100%"`, `width="50%"`, `width="1%"`, `width="0%"`} {
		if !strings.Contains(s, want) {
			t.Errorf("hm/barlist missing %s", want)
		}
	}
	checkPartial(t, a, "hm/barlist", "empty", ui.BarList{Label: "Referrals", Empty: "No referrals yet."})
	if _, err := a.RenderPartial("hm/barlist", ui.BarList{Items: []ui.BarItem{{Label: "x"}}}); err == nil {
		t.Error("hm/barlist without a Label: expected a render error")
	}
}

func TestPartialTracker(t *testing.T) {
	t.Parallel()

	a := newPartialAdmin(t)
	s := checkPartial(t, a, "hm/tracker", "default", ui.Tracker{Label: "Crawler responses, last 5 days", Blocks: []ui.TrackerBlock{
		{Kind: "success", Tip: "Sep 13: all 2xx"},
		{Kind: "warning", Tip: "Sep 14: 4 redirects"},
		{Kind: "danger", Tip: "Sep 15: 12 errors"},
		{Tip: "Sep 16: no fetches"},
		{Kind: "success", Tip: "Sep 17: all 2xx"},
	}})
	if !strings.Contains(s, "hm-viz--neutral") {
		t.Error("hm/tracker: a block without Kind must be neutral")
	}
	if _, err := a.RenderPartial("hm/tracker", ui.Tracker{Label: "x", Blocks: []ui.TrackerBlock{{Kind: "success"}}}); err == nil {
		t.Error("hm/tracker block without a Tip: expected a render error")
	}
}

// The analytics modifiers: hatched/short/hollow tracker blocks, a legend with Value and Share
// columns, and a regular-weight empty sentence. Each is opt-in; the default partials stay as they were.
func TestPartialAnalyticsModifiers(t *testing.T) {
	t.Parallel()

	a := newPartialAdmin(t)
	s := checkPartial(t, a, "hm/tracker", "states", ui.Tracker{Label: "Days with crawler errors", States: true, Blocks: []ui.TrackerBlock{
		{Kind: "success", Tip: "Sep 13: healthy"}, {Kind: "warning", Tip: "Sep 14: some errors"}, {Kind: "danger", Tip: "Sep 15: many errors"}, {Tip: "Sep 16: no fetches"}},
		Legend: []ui.TrackerKey{{Kind: "danger", Label: "Many errors"}, {Kind: "neutral", Label: "No fetches"}}})
	if strings.Count(s, "hm-tracker--states") != 2 {
		t.Errorf("hm/tracker States: want the modifier on the row and the legend:\n%s", s)
	}
	if strings.Contains(mustRender(t, a, "hm/tracker", ui.Tracker{Label: "x", Blocks: []ui.TrackerBlock{{Tip: "t"}}}), "--states") {
		t.Error("hm/tracker without States must not carry the modifier")
	}
	var many []ui.TrackerBlock
	for range 46 {
		many = append(many, ui.TrackerBlock{Kind: "success", Tip: "d"})
	}
	if !strings.Contains(mustRender(t, a, "hm/tracker", ui.Tracker{Label: "x", States: true, Blocks: many}), "hm-tracker--dense") {
		t.Error("hm/tracker States with more than 45 blocks must use the dense gap")
	}
	if strings.Contains(mustRender(t, a, "hm/tracker", ui.Tracker{Label: "x", Blocks: many}), "--dense") {
		t.Error("hm/tracker without States must not change with the block count")
	}
	s = checkPartial(t, a, "hm/categorybar", "columns", ui.CategoryBar{Label: "Responses by status", Columns: true, Segments: []ui.CategorySegment{
		{Label: "2xx success", Value: "55,574", Share: "81%", N: 81, Kind: "success"}, {Label: "4xx client error", Value: "12", Share: "19%", N: 19, Kind: "warning"}}})
	for _, want := range []string{"hm-catbar__legend--cols", `<span class="hm-catbar__share">81%</span>`, `<span class="hm-catbar__share">19%</span>`} {
		if !strings.Contains(s, want) {
			t.Errorf("hm/categorybar Columns missing %s:\n%s", want, s)
		}
	}
	if got := mustRender(t, a, "hm/categorybar", ui.CategoryBar{Label: "x", Segments: []ui.CategorySegment{{Label: "a", Value: "1", Share: "9%", N: 1}}}); strings.Contains(got, "share") {
		t.Errorf("hm/categorybar without Columns must not render the Share:\n%s", got)
	}
	s = checkPartial(t, a, "hm/empty", "regular", map[string]any{"Title": "No fetches recorded.", "Plain": true, "Regular": true})
	if !strings.Contains(s, "hm-empty--plain hm-empty--regular") {
		t.Errorf("hm/empty Regular missing the modifier:\n%s", s)
	}
	if strings.Contains(mustRender(t, a, "hm/empty", map[string]any{"Title": "x", "Plain": true}), "regular") {
		t.Error("hm/empty without Regular must not carry the modifier")
	}
}

func mustRender(t *testing.T, a *Admin, partial string, data any) string {
	t.Helper()
	out, err := a.RenderPartial(partial, data)
	if err != nil {
		t.Fatalf("%s: %v", partial, err)
	}
	return string(out)
}

func TestPartialCategoryBar(t *testing.T) {
	t.Parallel()

	a := newPartialAdmin(t)
	s := checkPartial(t, a, "hm/categorybar", "status", ui.CategoryBar{Label: "Responses by status", Segments: []ui.CategorySegment{
		{Label: "2xx", Value: "75%", N: 75, Kind: "success"},
		{Label: "3xx", Value: "15%", N: 15, Kind: "warning"},
		{Label: "4xx and 5xx", Value: "10%", N: 10, Kind: "danger"},
	}, Marker: &ui.CategoryMarker{At: 90, Label: "Target: 90% 2xx"}})
	for _, want := range []string{`x="0%" width="75%"`, `x="75%" width="15%"`, `x="90%" width="10%"`, `class="hm-catbar__marker" x="90%"`} {
		if !strings.Contains(s, want) {
			t.Errorf("hm/categorybar missing %s", want)
		}
	}
	s = checkPartial(t, a, "hm/categorybar", "series", ui.CategoryBar{Label: "Fetches by operator", Segments: []ui.CategorySegment{
		{Label: "OpenAI", Value: "40", N: 40}, {Label: "Anthropic", Value: "20", N: 20}, {Label: "Google", Value: "0", N: 0},
	}})
	if strings.Count(s, "<rect") != 2 || !strings.Contains(s, "hm-viz--series-3") || strings.Contains(s, "hm-catbar__marker") {
		t.Errorf("hm/categorybar series: want two rects, Google in the legend only, no marker:\n%s", s)
	}
}

func TestPartialSparkProgressCallout(t *testing.T) {
	t.Parallel()

	a := newPartialAdmin(t)
	s := checkPartial(t, a, "hm/stats", "ticker-spark", map[string]any{"Variant": "ticker", "Items": []ui.Stat{
		{Label: "AI fetches", Value: "112", Spark: ui.SparkOf([]int64{0, 5, 10})}}})
	if !strings.Contains(s, `points="0,22 50,12 100,2"`) || !strings.Contains(s, `d="M0,24 L0,22 L50,12 L100,2 L100,24 Z"`) {
		t.Errorf("spark geometry wrong:\n%s", s)
	}
	live := checkPartial(t, a, "hm/stats", "strip-live", map[string]any{"Variant": "strip", "Items": []ui.Stat{
		{Label: "AI fetches", Value: "112", Spark: &ui.Spark{ID: "spark-live", Kind: "series-1", Live: &ui.SparkLive{Src: "/admin/analytics/data/overview.json", Chart: "fetches"}}},
		{Label: "AI share", Value: "4%", Spark: &ui.Spark{ID: "spark-live-2", Kind: "series-3", Live: &ui.SparkLive{Src: "/admin/analytics/data/referrals.json", Chart: "referrals", Ratio: "referrals,pageviews", MinMax: true}}}}})
	for _, want := range []string{`data-hm-aia-chart="spark"`, `data-src="/admin/analytics/data/overview.json"`, `data-ratio="referrals,pageviews"`, ` data-minmax>`, `d=""/>`, `points=""`, `hm-viz--series-3`} {
		if !strings.Contains(live, want) {
			t.Errorf("live spark lacks %q:\n%s", want, live)
		}
	}
	if strings.Contains(live, `role="img"`) || strings.Contains(live, "data-tooltip") {
		t.Errorf("a live spark has no label or tooltip until the script sets them:\n%s", live)
	}
	checkPartial(t, a, "hm/progress", "default", ui.Progress{Label: "Share of pageviews from AI", Value: "38%", Pct: 38})
	checkPartial(t, a, "hm/progress", "over", ui.Progress{Label: "Quota", Value: "120 of 100", Pct: 120, Kind: "danger"})
	for _, k := range []string{"info", "success", "warning", "danger"} {
		checkPartial(t, a, "hm/callout", k, ui.Callout{Kind: k, Title: "Title " + k, Body: "Body text."})
	}
	checkPartial(t, a, "hm/callout", "compact", ui.Callout{Kind: "warning", Body: "Body only.", Compact: true})
	checkPartial(t, a, "hm/callout", "dismissible", ui.Callout{Kind: "info", Title: "Update", Body: "A new version is out.", DismissKey: "update", Compact: true})
	checkPartial(t, a, "hm/callout", "title-only", ui.Callout{Kind: "error", Title: "Title only"})
	if _, err := a.RenderPartial("hm/callout", ui.Callout{Kind: "info"}); err == nil {
		t.Error("hm/callout without Title and Body: expected a render error")
	}
	out, err := a.RenderPartial("hm/callout-start", map[string]any{"Kind": "error", "Title": "From a dict", "Compact": true})
	if err != nil {
		t.Fatalf("hm/callout-start from a dict: %v", err)
	}
	if got := string(out); !strings.Contains(got, `hm-callout--danger hm-callout--compact`) || !strings.Contains(got, `From a dict`) {
		t.Errorf("dict callout-start wrong:\n%s", got)
	}
}

func TestPartialTableCells(t *testing.T) {
	t.Parallel()

	a := newPartialAdmin(t)
	s := checkPartial(t, a, "hm/table", "cells", ui.Table{
		ID: "pages", Caption: "Pages", BaseURL: "/admin/pages",
		Columns: []ui.Col{{Key: "page", Label: "Page"}, {Key: "share", Label: "Share"}, {Key: "trend", Label: "Trend"}},
		Rows: []ui.Row{
			{ID: "p1", Label: "Home", Cells: []ui.Cell{{Text: "Home"},
				{Text: "38%", Bar: &ui.Bar{Label: "38% of pageviews", Pct: 38}},
				{Text: "up", Spark: ui.SparkOf([]int64{0, 5, 10})}}},
			{ID: "p2", Label: "About", Cells: []ui.Cell{{Text: "About"},
				{Text: "120%", Bar: &ui.Bar{Label: "Over quota", Pct: 120, Kind: "danger"}}, {Text: "-"}}},
		},
		Page: ui.Pagination{Page: 1, PerPage: 10, Total: 2, BaseURL: "/admin/pages"},
	})
	for _, want := range []string{`class="hm-cellbar"`, `aria-label="38% of pageviews"`, `width="38" height="6"`, `hm-viz--danger" width="100"`, `class="hm-spark"`} {
		if !strings.Contains(s, want) {
			t.Errorf("table cells missing %q:\n%s", want, s)
		}
	}
	if _, err := a.RenderPartial("hm/cellbar", ui.Bar{Pct: 5}); err == nil {
		t.Error("hm/cellbar without a Label: expected a render error")
	}
}

// A finished step shows the check icon in its circle and a green connector; the current step is
// accented, upcoming steps keep their number, and "Step n of N" and the aria-label stay.
func TestPartialStepsDone(t *testing.T) {
	t.Parallel()

	a := newPartialAdmin(t)
	s := checkPartial(t, a, "hm/steps", "done", map[string]any{
		"Label": "Import progress",
		"Items": []ui.Step{{Label: "Source", Done: true}, {Label: "Scan report", Done: true}, {Label: "Options", Current: true}, {Label: "Run"}, {Label: "Report"}},
	})
	for _, want := range []string{
		`aria-label="Import progress"`,
		`hm-steps__item hm-steps__item--done"><span class="hm-steps__num" aria-hidden="true"><svg class="hm-icon"`,
		`<path d="M20 6 9 17l-5-5"/>`,
		`Source<span class="hm-visually-hidden"> (done)</span>`,
		`aria-current="step"`,
		`Step 3 of 5`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("hm/steps missing %s in:\n%s", want, s)
		}
	}
	if n := strings.Count(s, `hm-steps__item--done`); n != 2 {
		t.Errorf("want 2 done steps, got %d", n)
	}
}

// Start, End and the Legend are template features the plain tracker case skips.
func TestPartialTrackerCaptionAndLegend(t *testing.T) {
	t.Parallel()

	a := newPartialAdmin(t)
	s := checkPartial(t, a, "hm/tracker", "caption", ui.Tracker{Label: "Fetches per day", Start: "16 Sep", End: "25 Oct",
		Legend: []ui.TrackerKey{{Kind: "info", Label: "Fetched"}, {Kind: "neutral", Label: "No fetch"}},
		Blocks: []ui.TrackerBlock{{Kind: "info", Tip: "16 Sep: 3 fetches"}, {Tip: "17 Sep: no fetches"}}})
	for _, want := range []string{"16 Sep", "25 Oct", "Fetched", "No fetch"} {
		if !strings.Contains(s, want) {
			t.Errorf("hm/tracker caption case missing %q", want)
		}
	}
}

// Two pickers share a page by giving each its own DateRange.ID.
func TestPartialDateRangeCustomID(t *testing.T) {
	t.Parallel()

	a := newPartialAdmin(t)
	s := checkPartial(t, a, "hm/daterange", "cmp", ui.DateRange{ID: "cmp", Action: "/admin/analytics/crawlers", From: "2026-09-01", To: "2026-09-30",
		Label: "1 Sep – 30 Sep", Min: "2026-01-01", Max: "2026-10-22"})
	for _, want := range []string{`popovertarget="cmp-pop"`, `id="cmp-pop"`, `for="cmp-from"`, `id="cmp-from"`, `for="cmp-to"`, `id="cmp-to"`} {
		if !strings.Contains(s, want) {
			t.Errorf("hm/daterange ID cmp missing %s", want)
		}
	}
	if strings.Contains(s, `range-pop`) || strings.Contains(s, `"range-from"`) {
		t.Error("hm/daterange ID cmp still has a default-id attribute (Cancel popovertarget?)")
	}
}

// Compare is opt-in: with it set the popover gains the switch and the button its second line.
func TestPartialDateRangeCompare(t *testing.T) {
	t.Parallel()

	a := newPartialAdmin(t)
	s := checkPartial(t, a, "hm/daterange", "cmpc", ui.DateRange{Action: "/admin/analytics", From: "2026-09-24", To: "2026-10-23",
		Label: "Last 30 days · 24 Sep – 23 Oct", Min: "2020-01-01", Max: "2026-10-23",
		Compare: &ui.DateCompare{Field: "compare", On: true, Label2: "vs 25 Aug – 23 Sep", Help: "Compared with 25 Aug – 23 Sep."}})
	for _, want := range []string{`role="switch"`, `name="compare"`, `value="1" checked`, `Compare to previous period`, `hm-daterange__vs">vs 25 Aug – 23 Sep`} {
		if !strings.Contains(s, want) {
			t.Errorf("hm/daterange compare missing %s", want)
		}
	}
}
