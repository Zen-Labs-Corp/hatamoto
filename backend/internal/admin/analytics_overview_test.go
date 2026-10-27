package admin

import (
	"regexp"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/admin/a11y"
	"hatamoto.dev/core/internal/agents"
)

// kpiCell is the markup of the KPI cell with this label.
func kpiCell(t *testing.T, body, label string) string {
	t.Helper()
	m := regexp.MustCompile(`(?s)<dl class="hm-statcard hm-statcard--ticker"><dt class="hm-statcard__label">` + regexp.QuoteMeta(label) + `</dt>.*?</dl>`).FindString(body)
	if m == "" {
		t.Fatalf("no KPI cell %q", label)
	}
	return m
}

// attentionOrder is the badge words of the attention strip, in the order shown.
func attentionOrder(body string) []string {
	var out []string
	for _, m := range regexp.MustCompile(`<li class="hm-statcard hm-statcard--attention"><span class="hm-badge[^"]*"><span class="hm-badge__dot" aria-hidden="true"></span>([^<]*)</span>`).FindAllStringSubmatch(body, -1) {
		out = append(out, m[1])
	}
	return out
}

func TestOverviewSection(t *testing.T) {
	t.Parallel()

	g, _ := newAnalyticsRig(t)
	rec := g.do("GET", "/admin/analytics", "owner-token", nil, nil)
	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	for _, want := range []string{
		"AI agents made 1,234 fetches of your pages in the last 30 days, and AI assistants sent 12 visits back.",
		"Key figures", "Fetches and visits over time", "Each has its own axis.", "Needs attention", "Top agents", "Top assistants",
		"Most active AI agents. Select a name for details.", "Visits from AI assistants, by assistant.", "Counted per day; no visitors are tracked.",
		`data-panels="fetches|referrals"`, `data-sum="AI fetches|AI-referred visits"`, `data-panel-labels="AI fetches per day|AI-referred visits per day"`, " data-hero",
		`>See all agents<`, `>See referrals<`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("overview missing %q", want)
		}
	}
	// widget order
	last := -1
	for _, h := range []string{"Key figures", "Fetches and visits over time", "Needs attention", "Top agents", "Top assistants"} {
		i := strings.Index(body, h)
		if i < last {
			t.Errorf("%q is out of order", h)
		}
		last = i
	}
	if n := strings.Count(body, `<dl class="hm-statcard hm-statcard--ticker">`); n != 4 {
		t.Errorf("KPI cells = %d, want 4", n)
	}
	// the numbers behind the hero are in the page: both columns, a total row
	for _, want := range []string{`>AI fetches</th>`, `>AI-referred visits</th>`, "<tfoot>", ">8<"} { // 6 + 2 fetches on the last day
		if !strings.Contains(body, want) {
			t.Errorf("hero table missing %q", want)
		}
	}
	if strings.Contains(body, `class="hm-table__right">Total</th>`) {
		t.Error("the hero table adds fetches to visits in a Total column")
	}
	if c := kpiCell(t, body, "AI share of traffic"); !strings.Contains(c, "0.3%") || !strings.Contains(c, ` data-minmax`) || !strings.Contains(c, `data-ratio="referrals,pageviews"`) {
		t.Errorf("share cell: %s", c)
	}
	if c := kpiCell(t, body, "Published pages fetched at least once"); !strings.Contains(c, "99%") || !strings.Contains(c, "119 of 120 published pages, since tracking began") || strings.Contains(c, "data-hm-aia-chart") {
		t.Errorf("coverage cell: %s", c)
	}
	// top lists: share beside the value, agent names link to their page
	for _, want := range []string{`href="/admin/analytics/agents/gptbot`, `<span class="hm-barlist__value">900</span><span class="hm-barlist__share">73%</span>`, `<span class="hm-barlist__value">9</span><span class="hm-barlist__share">75%</span>`} {
		if !strings.Contains(body, want) {
			t.Errorf("top lists missing %q", want)
		}
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
}

// Compare shows a change badge on the three trend cells and none otherwise (ISC-17).
func TestOverviewKPIDeltasFollowCompare(t *testing.T) {
	t.Parallel()

	g, fs := newAnalyticsRig(t)
	fs.prev = &agents.OverviewTotals{AIFetches: 1000, Verified: 20, Spoofed: 10, AIVisits: 12, Pageviews: 1000}
	fs.prevTo = today().AddDate(0, 0, -defaultRangeDays)

	off := g.do("GET", "/admin/analytics", "owner-token", nil, nil).Body.String()
	if strings.Contains(off, "hm-statcard__change") || strings.Contains(off, "vs previous 30 days") {
		t.Error("deltas show without compare")
	}
	on := g.do("GET", "/admin/analytics?compare=1", "owner-token", nil, nil).Body.String()
	if n := strings.Count(on, `<dd class="hm-statcard__change">`); n != 3 {
		t.Fatalf("delta lines = %d, want 3 (the coverage cell has none)", n)
	}
	for label, want := range map[string][]string{
		"AI fetches":          {"&#43;23.4%", "hm-badge--success", "up ", "vs previous 30 days"},
		"AI-referred visits":  {"Unchanged", "vs previous 30 days"},
		"AI share of traffic": {"-0.9 pts", "hm-badge--danger", "down "}, // 12/4500 = 0.27% against 12/1000 = 1.2%
	} {
		c := kpiCell(t, on, label)
		for _, w := range want {
			if !strings.Contains(c, w) {
				t.Errorf("%s cell lacks %q:\n%s", label, w, c)
			}
		}
	}
	if flat := kpiCell(t, on, "AI-referred visits"); strings.Contains(flat, "hm-badge--success") || strings.Contains(flat, "hm-badge--danger") {
		t.Errorf("a flat cell is neutral: %s", flat)
	}
	if !strings.Contains(on, `data-compare`) || !strings.Contains(on, "compare=1") {
		t.Error("the hero must ask for the previous period")
	}
}

// Attention cells are ranked: act now, then review, then nothing to do.
func TestOverviewAttentionIsRankedBySeverity(t *testing.T) {
	t.Parallel()

	g, fs := newAnalyticsRig(t)
	body := g.do("GET", "/admin/analytics", "owner-token", nil, nil).Body.String()
	// 5 of 1234 spoofed (0.4%), 11 errors (0.9%, 5xx 0.2%) and one never-fetched page are reviews; nobody is blocked.
	if got, want := strings.Join(attentionOrder(body), ","), "Review,Review,Review,Nothing to do"; got != want {
		t.Fatalf("badges = %s, want %s", got, want)
	}
	for _, want := range []string{"0.4% of fetches", "0.9% of fetches", "1% of published pages", "Counts only agents on a blocked purpose.",
		`href="/admin/analytics/health#w-h-ver"`, `href="/admin/analytics/health#w-h-fail"`, `href="/admin/analytics/content#w-ct-never"`} {
		if !strings.Contains(body, want) {
			t.Errorf("attention missing %q", want)
		}
	}
	if strings.Contains(body, `href="/admin/analytics/crawlers#w-cr-list"`) {
		t.Error("a calm cell has no action link")
	}

	// chatgpt-user ignores robots.txt: blocked user_fetch with fetches is "Act now" and goes first
	fs.denied = []agents.Purpose{agents.PurposeUserFetch}
	body = g.do("GET", "/admin/analytics?from="+day(-29)+"&to="+day(0)+"&compare=0", "owner-token", nil, nil).Body.String()
	if got, want := strings.Join(attentionOrder(body), ","), "Act now,Review,Review,Review"; got != want {
		t.Fatalf("badges = %s, want %s", got, want)
	}
	for _, want := range []string{"agents ignoring robots.txt fetched pages while blocked", "0.8% of fetches. Blocking them needs a firewall or server rule.", `#w-cr-list"`} {
		if !strings.Contains(body, want) {
			t.Errorf("act-now cell missing %q", want)
		}
	}
}

func TestAttentionThresholds(t *testing.T) {
	t.Parallel()

	tot := func(f, sp, e4, e5 int64) totalsJSON {
		return totalsJSON{AIFetches: f, Spoofed: sp, Status4xx: e4, Status5xx: e5}
	}
	badges := func(t totalsJSON) string {
		var out []string
		for _, a := range overviewAttention(t, nil, 0, 10, nil) {
			out = append(out, a.Badge().Label)
		}
		return strings.Join(out, ",")
	}
	if got := badges(tot(1000, 10, 0, 0)); !strings.HasPrefix(got, "Act now,") { // spoofed at 1% is act now
		t.Errorf("1%% spoofed: %s", got)
	}
	if got := badges(tot(1000, 9, 0, 0)); !strings.HasPrefix(got, "Review,") {
		t.Errorf("0.9%% spoofed: %s", got)
	}
	if got := badges(tot(1000, 0, 50, 0)); !strings.HasPrefix(got, "Act now,") { // errors at 5%
		t.Errorf("5%% errors: %s", got)
	}
	if got := badges(tot(1000, 0, 0, 5)); !strings.HasPrefix(got, "Act now,") { // 5xx at 0.5%
		t.Errorf("0.5%% 5xx: %s", got)
	}
	if got := badges(tot(1000, 0, 40, 4)); !strings.HasPrefix(got, "Review,") {
		t.Errorf("4.4%% errors: %s", got)
	}
	if got := badges(tot(0, 0, 0, 0)); got != "Nothing to do,Nothing to do,Nothing to do,Nothing to do" {
		t.Errorf("calm: %s", got)
	}
}

// A range over 31 days charts and tables by week.
func TestOverviewNinetyDaysIsWeekly(t *testing.T) {
	t.Parallel()

	g, _ := newAnalyticsRig(t)
	body := g.do("GET", "/admin/analytics?from="+day(-89)+"&to="+day(0), "owner-token", nil, nil).Body.String()
	for _, want := range []string{"AI fetches per week|AI-referred visits per week", "Week of ", `<th scope="col">Week</th>`, "AI fetches and AI-referred visits per week"} {
		if !strings.Contains(body, want) {
			t.Errorf("90-day overview missing %q", want)
		}
	}
	if strings.Contains(body, "per day|") || strings.Contains(body, "overview per day") {
		t.Error("a weekly chart still says per day")
	}
}

func TestAssistantName(t *testing.T) {
	t.Parallel()

	for in, want := range map[string]string{"chatgpt": "ChatGPT", "meta_ai": "Meta AI", "copilot": "Copilot", "ChatGPT": "ChatGPT", "some_new-bot": "Some New Bot"} {
		if got := assistantName(in); got != want {
			t.Errorf("assistantName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestKPIDelta(t *testing.T) {
	t.Parallel()

	rg, _ := parseRange(nil)
	if kpiDelta(false, rg, 2, 1, false, false) != nil {
		t.Error("no delta without compare")
	}
	if d := kpiDelta(true, rg, 110, 100, false, false); d.Text != "+10.0%" || d.Tone != "success" || d.Dir != "up" || d.Caption != "vs previous 30 days" {
		t.Errorf("up: %+v", d)
	}
	if d := kpiDelta(true, rg, 5, 10, false, true); d.Text != "-50.0%" || d.Tone != "success" { // lower is better
		t.Errorf("lower is better: %+v", d)
	}
	if d := kpiDelta(true, rg, 3.9, 3.5, true, false); d.Text != "+0.4 pts" {
		t.Errorf("points: %+v", d)
	}
	if d := kpiDelta(true, rg, 5, 0, false, false); d.Dir != "flat" || d.Text != "New" || d.Tone != "neutral" {
		t.Errorf("traffic after none is New, neutral: %+v", d)
	}
	if d := kpiDelta(true, rg, 0, 0, false, false); d.Text != "Unchanged" {
		t.Errorf("nothing before and now is Unchanged: %+v", d)
	}
	one := agents.Range{From: today(), To: today()}
	if got := compareWords(one); got != "vs yesterday" {
		t.Errorf("one day: %q", got)
	}
}

// The heading outline matches the visual one: the hero and the Top cards are
// not children of "Key figures" or "Needs attention".
func TestOverviewHeadingOutline(t *testing.T) {
	t.Parallel()

	g, _ := newAnalyticsRig(t)
	body := g.do("GET", "/admin/analytics", "owner-token", nil, nil).Body.String()
	var h2 string
	parents := map[string]string{} // h3 text -> the h2 above it
	for _, m := range regexp.MustCompile(`<h([23])[^>]*>([^<]*)</h[23]>`).FindAllStringSubmatch(body, -1) {
		if m[1] == "2" {
			h2 = m[2]
		} else if _, seen := parents[m[2]]; !seen {
			parents[m[2]] = h2
		}
	}
	for _, card := range []string{"Top agents", "Top assistants"} {
		if p := parents[card]; p == "Needs attention" || p == "Key figures" || p == "" {
			t.Errorf("%q sits under the h2 %q", card, p)
		}
	}
	for card, p := range parents {
		if strings.HasPrefix(card, "AI fetches") && (p == "Key figures" || p == "") {
			t.Errorf("the hero %q sits under the h2 %q", card, p)
		}
	}
}
