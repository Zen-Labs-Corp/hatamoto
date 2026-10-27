package admin

import (
	"strings"
	"testing"

	"hatamoto.dev/core/internal/admin/a11y"
	"hatamoto.dev/core/internal/admin/ui"
)

// cardOf is the markup of the card with this id, up to the next card.
func cardOf(t *testing.T, body, id string) string {
	t.Helper()
	i := strings.Index(body, `id="`+id+`"`)
	if i < 0 {
		t.Fatalf("no card %q", id)
	}
	rest := body[i:]
	if j := strings.Index(rest[1:], `<section class="hm-card"`); j >= 0 {
		rest = rest[:j+1]
	}
	return rest
}

func TestContentSection(t *testing.T) {
	t.Parallel()

	g, _ := newAnalyticsRig(t)
	rec := g.do("GET", "/admin/analytics/content", "owner-token", nil, nil)
	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	for _, want := range []string{
		"119 of 120 published pages have been fetched at least once since tracking began.",
		"Fetches by page type", "What AI agents read, grouped by the type of page.",
		"Coverage", "Published pages fetched at least once. As of today, not filtered by the date range.",
		"Time to first fetch", "Days from publish to the first AI fetch.", "Most fetched pages", "Top 10 pages over the selected period.",
		"First crawls", "Pages fetched for the first time, per day.",
		"Published pages no AI agent has fetched", "Newest first. Pages have no importance score yet, so this list is sorted by publish date.",
		"These pages have not been fetched since tracking began. The list is not filtered by the date range.",
		`data-chart="by_type"`, `data-chart="per_day"`, " data-cols",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("content missing %q", want)
		}
	}
	last := -1
	for _, id := range []string{"w-ct-hero", "w-ct-cov", "w-ct-ttf", "w-ct-top", "w-ct-first", "w-ct-never"} {
		i := strings.Index(body, `id="`+id+`"`)
		if i < 0 || i < last {
			t.Errorf("%s is missing or out of order", id)
		}
		last = i
	}
	// the numbers behind the hero chart, by page type
	hero := cardOf(t, body, "w-ct-hero")
	for _, want := range []string{`>Blog post</th>`, `>Other pages</th>`, "<tfoot>"} {
		if !strings.Contains(hero, want) {
			t.Errorf("hero table missing %q", want)
		}
	}
	// coverage: fetched and never fetched, then the never-fetched pages by type
	cov := cardOf(t, body, "w-ct-cov")
	for _, want := range []string{`Fetched at least once <span class="hm-catbar__value">119</span><span class="hm-catbar__share">99%</span>`, `Never fetched <span class="hm-catbar__value">1</span><span class="hm-catbar__share">1%</span>`, "hm-viz--series-1", "hm-viz--series-6",
		"Never fetched, by page type", ">Blog post<", "119 of 120 published pages have been fetched by at least one AI agent since tracking began."} {
		if !strings.Contains(cov, want) {
			t.Errorf("coverage missing %q", want)
		}
	}
	// time to first fetch: the buckets cover the whole range, in the prototype's colours
	ttf := cardOf(t, body, "w-ct-ttf")
	for _, want := range []string{`Same day <span class="hm-catbar__value">1</span><span class="hm-catbar__share">100%</span>`, `1 to 3 days <span class="hm-catbar__value">0</span><span class="hm-catbar__share">0%</span>`, "8 days or more", "hm-viz--series-5", "First fetched by", "GPTBot"} {
		if !strings.Contains(ttf, want) {
			t.Errorf("time to first fetch missing %q", want)
		}
	}
	// most fetched pages: a ranked bar list with the page type beside the path, no day table
	top := cardOf(t, body, "w-ct-top")
	for _, want := range []string{`class="hm-barlist hm-barlist--quiet hm-barlist--mono"`, `aria-label="The ten pages AI agents fetched most"`, `title="/about"`, `<span class="hm-barlist__note">Page</span>`, `<span class="hm-barlist__value">40</span>`, `width="100%"`} {
		if !strings.Contains(top, want) {
			t.Errorf("most fetched pages missing %q", want)
		}
	}
	for _, bad := range []string{"<details", "Show data", "<canvas"} {
		if strings.Contains(top, bad) {
			t.Errorf("most fetched pages contains %q", bad)
		}
	}
	// never fetched: name, mono slug, page type, "Not recorded" for a missing publish date
	never := cardOf(t, body, "w-ct-never")
	for _, want := range []string{`<th scope="row" data-label="Page" class="hm-table__rowhead">Lonely <code class="hm-aia-mono">lonely</code></th>`, `data-label="Page type">Other pages`, `data-label="Published"><span class="hm-aia-small">Not recorded</span>`} {
		if !strings.Contains(never, want) {
			t.Errorf("never fetched missing %q", want)
		}
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
}

// A range over 31 days charts first crawls and the type table by week.
func TestContentNinetyDaysIsWeekly(t *testing.T) {
	t.Parallel()

	g, _ := newAnalyticsRig(t)
	body := g.do("GET", "/admin/analytics/content?from="+day(-89)+"&to="+day(0), "owner-token", nil, nil).Body.String()
	for _, want := range []string{"Pages fetched for the first time, per week.", "Week of ", `<th scope="col">Week</th>`, "AI fetches per week by page type"} {
		if !strings.Contains(body, want) {
			t.Errorf("90-day content missing %q", want)
		}
	}
}

// The never-fetched pager is real links that keep the range, on its own parameter.
func TestContentNeverFetchedPagerKeepsTheRange(t *testing.T) {
	t.Parallel()

	p := ui.Pagination{Page: 1, PerPage: 25, Total: 60, BaseURL: "/admin/analytics/content", Param: "npage", Query: analyticsViewCarry(map[string][]string{"from": {"2026-01-01"}, "to": {"2026-01-31"}, "compare": {"1"}})}
	if got, want := p.Href(2), "/admin/analytics/content?compare=1&from=2026-01-01&npage=2&to=2026-01-31"; got != want {
		t.Errorf("Href(2) = %s, want %s", got, want)
	}
	if got := p.Href(1); strings.Contains(got, "npage") {
		t.Errorf("page one drops the parameter: %s", got)
	}
	g, fs := newAnalyticsRig(t)
	fs.neverTotal = 60
	body := g.do("GET", "/admin/analytics/content?npage=2&from="+day(-29)+"&to="+day(0)+"&compare=1", "owner-token", nil, nil).Body.String()
	pager := body[strings.Index(body, `<nav class="hm-pagination"`):]
	for _, want := range []string{"Showing 26–50 of 60", `rel="prev" href="/admin/analytics/content?compare=1&amp;from=` + day(-29) + `&amp;to=` + day(0) + `"`,
		`rel="next" href="/admin/analytics/content?compare=1&amp;from=` + day(-29) + `&amp;npage=3&amp;to=` + day(0) + `"`} {
		if !strings.Contains(pager, want) {
			t.Errorf("pager missing %q", want)
		}
	}
}
