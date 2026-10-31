package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/admin/a11y"
	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/agents"
	"hatamoto.dev/core/internal/analytics"
)

type fakeAgentStats struct {
	mu     sync.Mutex
	denied []agents.Purpose
	series []agents.SeriesPoint
	// searchSeries is what SeriesWithSearch adds to series (search engine hits).
	searchSeries []agents.SeriesPoint
	// extraAgents are added to what Agents answers (search engines, social, SEO tools).
	extraAgents []agents.AgentTotals
	// noAI makes Overview report no AI fetches.
	noAI bool
	// googleStatus, googleKPIs and googlePages are what the Googlebot reads answer
	// (a default set when nil); googleFilter is the filter GooglePages was last asked for.
	googleStatus []agents.SeriesPoint
	googleKPIs   *agents.GoogleKPIs
	googlePages  []agents.GooglePageRow
	googleFilter string
	// lastRange is the range Overview was last asked for.
	lastRange agents.Range
	// prev is what Overview answers for a range ending on prevTo (the previous period).
	prev   *agents.OverviewTotals
	prevTo time.Time
	// prevSeries is what Series answers for the previous period (same prevTo).
	prevSeries []agents.SeriesPoint
	// neverTotal is how many never-fetched pages NeverFetched reports; 0 means one.
	neverTotal int
	// health, when set, is what HealthSeries answers instead of the default rows.
	health []agents.HealthDay
	// healthCalls counts HealthSeries reads.
	healthCalls int
	// verify, when set, is what VerificationHealth answers instead of a healthy default.
	verify *agents.VerificationHealth
	// verifyErr, when set, fails VerificationHealth.
	verifyErr error
	// verifyIDs is the agent ids VerificationHealth was last asked for.
	verifyIDs []string
}

func (f *fakeAgentStats) VerificationHealth(_ context.Context, _ agents.Range, ids []string) (agents.VerificationHealth, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.verifyIDs = ids
	if f.verifyErr != nil {
		return agents.VerificationHealth{}, f.verifyErr
	}
	if f.verify != nil {
		return *f.verify, nil
	}
	return agents.VerificationHealth{Verified: 980, Spoofed: 12, SpoofedIPs: 3, SpoofedIPsInCDN: 3}, nil
}

func day(offset int) string { return today().AddDate(0, 0, offset).Format(dayLayout) }

func (f *fakeAgentStats) Overview(_ context.Context, rg agents.Range) (agents.OverviewTotals, error) {
	if f.prev != nil && rg.To.Equal(f.prevTo) {
		return *f.prev, nil
	}
	f.mu.Lock()
	f.lastRange = rg
	f.mu.Unlock()
	if f.noAI {
		return agents.OverviewTotals{}, nil
	}
	return agents.OverviewTotals{AIFetches: 1234, Verified: 30, Spoofed: 5, Unverifiable: 5, AIVisits: 12, Pageviews: 4500}, nil
}
func (f *fakeAgentStats) Series(_ context.Context, rg agents.Range, _ string) ([]agents.SeriesPoint, error) {
	if f.prevSeries != nil && rg.To.Equal(f.prevTo) {
		return f.prevSeries, nil
	}
	return f.series, nil
}
func (f *fakeAgentStats) SeriesWithSearch(ctx context.Context, rg agents.Range, g string) ([]agents.SeriesPoint, error) {
	base, _ := f.Series(ctx, rg, g)
	if f.prevSeries != nil && rg.To.Equal(f.prevTo) {
		return base, nil
	}
	return append(append([]agents.SeriesPoint{}, base...), f.searchSeries...), nil
}
func (f *fakeAgentStats) GooglebotStatusSeries(context.Context, agents.Range) ([]agents.SeriesPoint, error) {
	if f.googleStatus != nil {
		return f.googleStatus, nil
	}
	return []agents.SeriesPoint{{Day: day(-1), Key: "2xx", Hits: 30}, {Day: day(-1), Key: "4xx", Hits: 3}, {Day: day(0), Key: "2xx", Hits: 20}, {Day: day(0), Key: "5xx", Hits: 1}}, nil
}
func (f *fakeAgentStats) GoogleKPIs(context.Context, agents.Range) (agents.GoogleKPIs, error) {
	if f.googleKPIs != nil {
		return *f.googleKPIs, nil
	}
	med := 6 * time.Hour
	return agents.GoogleKPIs{Hits: 995, Verified: 990, PagesCrawled: 41, Published: 144, MedianFirstCrawl: &med}, nil
}

// GooglePages filters the rows like the SQL does, so a test sees what each filter leaves.
func (f *fakeAgentStats) GooglePages(_ context.Context, _ agents.Range, filter string, limit, offset int) ([]agents.GooglePageRow, int, error) {
	f.mu.Lock()
	f.googleFilter = filter
	f.mu.Unlock()
	all := f.googlePages
	if all == nil {
		d := func(n int) *time.Time { t := today().AddDate(0, 0, n); return &t }
		e1, e2, e3 := uuid.MustParse("00000000-0000-4000-8000-0000000000e1"), uuid.MustParse("00000000-0000-4000-8000-0000000000e2"), uuid.MustParse("00000000-0000-4000-8000-0000000000e3")
		all = []agents.GooglePageRow{
			{Path: "/post/hello", EntityID: &e1, Name: "Hello <world>", Crawls: 556, LastDay: d(-1), LastStatus: 200},
			{Path: "/docs/v0/gone", Crawls: 37, LastDay: d(-2), LastStatus: 404},
			{Path: "/post/edited", EntityID: &e2, Name: "Edited page", Crawls: 12, LastDay: d(-3), LastStatus: 200, Stale: true},
			{EntityID: &e3, Name: "Brand new page"},
		}
	}
	var out []agents.GooglePageRow
	for _, r := range all {
		keep := map[string]bool{"": true, agents.GoogleFilterAll: true, agents.GoogleFilterNotCrawled: r.EntityID != nil && r.Crawls == 0,
			agents.GoogleFilterErrors: r.LastStatus >= 400, agents.GoogleFilterStale: r.Stale}[filter]
		if keep {
			out = append(out, r)
		}
	}
	total := len(out)
	if offset >= len(out) {
		return nil, total, nil
	}
	return out[offset:min(offset+limit, len(out))], total, nil
}
func (f *fakeAgentStats) TopAgents(context.Context, agents.Range, int) ([]agents.AgentTotals, error) {
	return []agents.AgentTotals{{AgentID: "gptbot", Operator: "OpenAI", Purpose: "training", Hits: 900, Verified: 30, LastSeenDay: day(0)}}, nil
}
func (f *fakeAgentStats) TopPages(context.Context, agents.Range, agents.Page) ([]agents.PageRow, int, error) {
	name, slug := "About <us>", "page"
	return []agents.PageRow{{Path: "/about", EntityName: &name, Hits: 40, Agents: 3, LastDay: day(-1), SchemaSlug: &slug}}, 1, nil
}
func (f *fakeAgentStats) FetchesBySchema(context.Context, agents.Range) ([]agents.SeriesPoint, error) {
	return []agents.SeriesPoint{{Day: day(-1), Key: "blog-post", Hits: 5}, {Day: day(0), Key: "blog-post", Hits: 3}, {Day: day(0), Key: "", Hits: 2}}, nil
}
func (f *fakeAgentStats) NeverFetchedBySchema(context.Context) ([]agents.SchemaCount, error) {
	slug := "blog-post"
	return []agents.SchemaCount{{SchemaSlug: &slug, Count: 1}}, nil
}
func (f *fakeAgentStats) HealthSeries(context.Context, agents.Range) ([]agents.HealthDay, error) {
	f.mu.Lock()
	f.healthCalls++
	f.mu.Unlock()
	if f.health != nil {
		return f.health, nil
	}
	return []agents.HealthDay{
		{Day: day(-1), Hits: 100, S2xx: 90, S3xx: 4, S4xx: 5, S5xx: 1, AI4xx: 5, AI5xx: 1, Verified: 30, Spoofed: 2},
		{Day: day(0), Hits: 50, S2xx: 45, S4xx: 3, S5xx: 2, AI4xx: 3, AI5xx: 2, Verified: 10},
	}, nil
}
func (f *fakeAgentStats) FirstCrawlAggregates(context.Context, agents.Range) (agents.FirstCrawlAgg, error) {
	return agents.FirstCrawlAgg{SameDay: 1, ByAgent: []agents.AgentCount{{AgentID: "gptbot", Count: 1}}}, nil
}
func (f *fakeAgentStats) PublishedPages(context.Context) (int, error) { return 120, nil }
func (f *fakeAgentStats) NeverFetched(context.Context, agents.Page) ([]agents.NeverFetchedRow, int, error) {
	total := 1
	if f.neverTotal > 0 {
		total = f.neverTotal
	}
	return []agents.NeverFetchedRow{{EntityID: uuid.New(), Name: "Lonely", Slug: "lonely"}}, total, nil
}
func (f *fakeAgentStats) Agents(ctx context.Context, rg agents.Range) ([]agents.AgentTotals, error) {
	top, _ := f.TopAgents(ctx, rg, 0)
	// chatgpt-user is in the registry with ignores_robots: true.
	return append(append(top, agents.AgentTotals{AgentID: "chatgpt-user", Operator: "OpenAI", Purpose: "user_fetch", Hits: 10, LastSeenDay: day(0)}), f.extraAgents...), nil
}
func (f *fakeAgentStats) AgentDetail(_ context.Context, _ agents.Range, id string) (agents.AgentDetailData, error) {
	if id == "ghost" { // not in the registry, active only outside the range
		return agents.AgentDetailData{Found: true, Totals: agents.AgentTotals{AgentID: id}}, nil
	}
	if id != "gptbot" {
		return agents.AgentDetailData{}, nil
	}
	return agents.AgentDetailData{Found: true, Totals: agents.AgentTotals{AgentID: id, Hits: 900, Verified: 30}, Series: []agents.DayCount{{Day: day(0), Hits: 10}},
		PagesRead: 17, TopPaths: []agents.PathCount{{Path: "/", Hits: 5}}, ByStatus: []agents.StatusCount{{Status: 200, Hits: 8}, {Status: 404, Hits: 2}}}, nil
}
func (f *fakeAgentStats) Errors(context.Context, agents.Range, agents.Page) ([]agents.ErrorRow, int, error) {
	return []agents.ErrorRow{{Path: "/gone", Status: 404, Hits: 3, AgentIDs: []string{"gptbot"}, LastDay: day(0)}, {Path: "/boom", Status: 500, Hits: 1}}, 2, nil
}
func (f *fakeAgentStats) Spoofed(context.Context, agents.Range, agents.Page) ([]agents.SpoofedRow, int, error) {
	n := "203.0.113.0/24"
	return []agents.SpoofedRow{{OccurredAt: time.Now(), AgentID: "gptbot", Path: "/x", IPMasked: &n}}, 1, nil
}
func (f *fakeAgentStats) FirstCrawls(context.Context, agents.Range, agents.Page) ([]agents.FirstCrawlRow, int, error) {
	pub := time.Now().Add(-2 * time.Hour)
	return []agents.FirstCrawlRow{{EntityID: uuid.New(), Name: "Fresh post", Slug: "fresh", AgentID: "gptbot", FirstAt: time.Now(), PublishedAt: &pub}}, 1, nil
}
func (f *fakeAgentStats) DeniedPurposes(context.Context) ([]agents.Purpose, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.denied, nil
}
func (f *fakeAgentStats) SetDenied(_ context.Context, d []agents.Purpose) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.denied = d
	return nil
}

type fakeReferrals struct{}

func (fakeReferrals) AIReferrals(context.Context, time.Time, time.Time) ([]analytics.ReferralDay, error) {
	return []analytics.ReferralDay{{Day: day(-1), Assistant: "ChatGPT", Visits: 7}, {Day: day(0), Assistant: "ChatGPT", Visits: 2}, {Day: day(0), Assistant: "Perplexity", Visits: 3}}, nil
}
func (fakeReferrals) Pageviews(context.Context, time.Time, time.Time) ([]analytics.DayCount, error) {
	return []analytics.DayCount{{Day: day(0), Views: 100}}, nil
}

func newAnalyticsRig(t *testing.T) (*rig, *fakeAgentStats) {
	t.Helper()
	fs := &fakeAgentStats{series: []agents.SeriesPoint{{Day: day(-1), Key: "OpenAI", Hits: 4}, {Day: day(0), Key: "OpenAI", Hits: 6}, {Day: day(0), Key: "Anthropic", Hits: 2}}}
	g := newRig(t, func(d *Deps) { d.Growth.Agents, d.Growth.Referrals = fs, fakeReferrals{} })
	return g, fs
}

func TestParseRange(t *testing.T) {
	t.Parallel()

	rg, msg := parseRange(url.Values{})
	if msg != "" || len(rangeDays(rg)) != defaultRangeDays || !rg.To.Equal(today()) {
		t.Fatalf("default: %v %q", rg, msg)
	}
	for name, q := range map[string]url.Values{
		"bad date": {"from": {"yesterday"}},
		"reversed": {"from": {"2026-02-01"}, "to": {"2026-01-01"}},
		"too long": {"from": {"2020-01-01"}, "to": {"2026-01-01"}},
		"bad to":   {"to": {"2026-13-40"}},
	} {
		rg, msg := parseRange(q)
		if msg == "" {
			t.Errorf("%s: expected an error", name)
		}
		if len(rangeDays(rg)) != defaultRangeDays || !rg.To.Equal(today()) {
			t.Errorf("%s: a rejected range must fall back to the default, got %v", name, rg)
		}
	}
	rg, msg = parseRange(url.Values{"to": {"2026-03-31"}})
	if msg != "" || rg.From.Format(dayLayout) != "2026-03-02" {
		t.Errorf("only to: %v %q", rg, msg)
	}
}

func TestPivotGroupsSmallSeriesIntoOther(t *testing.T) {
	t.Parallel()

	days := []string{"2026-01-01", "2026-01-02"}
	var pts []agents.SeriesPoint
	for i, k := range []string{"a", "b", "c", "d", "e", "f", "g"} {
		pts = append(pts, agents.SeriesPoint{Day: "2026-01-01", Key: k, Hits: int64(10 - i)})
	}
	pts = append(pts, agents.SeriesPoint{Day: "2026-01-09", Key: "a", Hits: 99}) // outside the range
	m := pivot(days, pts, func(s string) string { return s })
	if len(m.Cols) != maxChartSeries+1 || m.Cols[len(m.Cols)-1] != "Other" || m.Vals["Other"][0] != 5+4 {
		t.Fatalf("cols %v other %v", m.Cols, m.Vals["Other"])
	}
	if m.Vals["a"][0] != 10 || m.Vals["a"][1] != 0 {
		t.Errorf("a = %v", m.Vals["a"])
	}
	tbl := m.table("x", "Day")
	if tbl.Total == nil || tbl.Total.Cells[len(tbl.Total.Cells)-1] != fmtNum(10+9+8+7+6+5+4) {
		t.Errorf("total row: %+v", tbl.Total)
	}
	if empty := (matrix{Days: days, Cols: []string{"x"}, Vals: map[string][]int64{"x": {0, 0}}}).table("x", "Day"); len(empty.Rows) != 0 {
		t.Error("an all-zero range has no table rows (shows the empty message)")
	}
}

// The section pages answer in the shell with the Analytics sub-sidebar, the
// current section marked, and no canvas charts.
func TestAnalyticsSectionsRenderInTheAreaFrame(t *testing.T) {
	t.Parallel()

	g, _ := newAnalyticsRig(t)
	for _, tc := range []struct{ path, current, heading string }{
		{"/admin/analytics", "Overview", "Are AI systems reading your site?"},
		{"/admin/analytics/crawlers", "Crawlers", "Who is reading your site?"},
		{"/admin/analytics/content", "Content", "What are they reading?"},
		{"/admin/analytics/referrals", "Referrals", "What comes back?"},
		{"/admin/analytics/health", "Health", "Is it running cleanly?"},
		{"/admin/analytics/agents/gptbot", "Crawlers", "GPTBot"},
	} {
		rec := g.do("GET", tc.path, "owner-token", nil, nil)
		body := rec.Body.String()
		if rec.Code != 200 {
			t.Errorf("%s: status %d", tc.path, rec.Code)
			continue
		}
		for _, want := range []string{`class="hm-subnav`, "<h1>" + tc.heading + "</h1>", "/admin/_/js/aia/page."} {
			if !strings.Contains(body, want) {
				t.Errorf("%s lacks %q", tc.path, want)
			}
		}
		current := regexp.MustCompile(`(?s)aria-current="page">.*?<span>([^<]*)</span>`).FindAllStringSubmatch(body[strings.Index(body, `class="hm-subnav"`):], -1)
		if len(current) < 1 || current[0][1] != tc.current {
			t.Errorf("%s: sub-sidebar current = %v, want %s", tc.path, current, tc.current)
		}
		if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
			t.Errorf("%s a11y: %v", tc.path, issues)
		}
	}
}

func TestAnalyticsSectionErrorStates(t *testing.T) {
	t.Parallel()

	g, _ := newAnalyticsRig(t)
	if rec := g.do("GET", "/admin/analytics/agents/nope", "owner-token", nil, nil); rec.Code != 404 || !strings.Contains(rec.Body.String(), "Agent not found") {
		t.Errorf("unknown agent: %d", rec.Code)
	}
	if rec := g.do("GET", "/admin/analytics/crawlers?from=nope", "owner-token", nil, nil); rec.Code != 422 || !strings.Contains(rec.Body.String(), "Showing the last 30 days") {
		t.Errorf("bad range: %d", rec.Code)
	}
	if rec := g.do("GET", "/admin/analytics/agents/gptbot?from=nope", "owner-token", nil, nil); rec.Code != 422 {
		t.Errorf("bad range on the agent page: %d", rec.Code)
	}
}

func jsonOf(t *testing.T, g *rig, u string) (int, map[string]any) {
	t.Helper()
	rec := g.do("GET", u, "owner-token", nil, nil)
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("%s content type %q", u, ct)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("%s must be no-store", u)
	}
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("%s: %v: %s", u, err, rec.Body.String())
	}
	return rec.Code, m
}

func TestDashboardJSONEndpoints(t *testing.T) {
	t.Parallel()

	g, _ := newAnalyticsRig(t)
	from, to := day(-6), day(0)
	code, m := jsonOf(t, g, "/admin/analytics/data/overview.json?from="+from+"&to="+to)
	if code != 200 {
		t.Fatalf("overview: %d %v", code, m)
	}
	charts := m["charts"].(map[string]any)
	fetches := charts["fetches"].(map[string]any)
	if n := len(fetches["labels"].([]any)); n != 7 {
		t.Errorf("one label per day: %d", n)
	}
	ds := fetches["datasets"].([]any)
	if len(ds) != 2 || fetches["stacked"] != true || fetches["kind"] != "bar" {
		t.Errorf("datasets: %v", ds)
	}
	if fetches["format"] != "compact" {
		t.Errorf("fetches spec format: %v", fetches["format"])
	}
	first := ds[0].(map[string]any)
	if first["label"] != "OpenAI" || len(first["data"].([]any)) != 7 {
		t.Errorf("series %v", first)
	}
	if m["totals"].(map[string]any)["ai_fetches"].(float64) != 1234 {
		t.Errorf("totals: %v", m["totals"])
	}
	if _, ok := charts["visits"]; !ok {
		t.Error("overview should chart pageviews and AI visits")
	}

	if code, m := jsonOf(t, g, "/admin/analytics/data/overview.json?from=bad"); code != 400 || m["error"] == nil {
		t.Errorf("bad range: %d %v", code, m)
	}
	if code, _ := jsonOf(t, g, "/admin/analytics/data/agent.json?id=nope&from="+from+"&to="+to); code != 404 {
		t.Errorf("unknown agent: %d", code)
	}
	if code, m := jsonOf(t, g, "/admin/analytics/data/agent.json?id=gptbot&from="+from+"&to="+to); code != 200 || m["charts"].(map[string]any)["daily"] == nil || m["name"] != "GPTBot" {
		t.Errorf("agent: %d %v", code, m)
	}
	if code, m := jsonOf(t, g, "/admin/analytics/data/pages.json"); code != 200 || len(m["charts"].(map[string]any)["top_pages"].(map[string]any)["labels"].([]any)) != 1 {
		t.Errorf("pages: %d %v", code, m)
	}
	if code, m := jsonOf(t, g, "/admin/analytics/data/first-crawls.json"); code != 200 || len(m["items"].([]any)) != 1 {
		t.Errorf("first crawls: %d %v", code, m)
	}
	if code, m := jsonOf(t, g, "/admin/analytics/data/referrals.json?from="+from+"&to="+to); code != 200 {
		t.Errorf("referrals: %d", code)
	} else {
		as := m["assistants"].([]any)
		if as[0].(map[string]any)["assistant"] != "ChatGPT" || as[0].(map[string]any)["visits"].(float64) != 9 || m["pageviews"].(float64) != 100 {
			t.Errorf("referrals: %v", m)
		}
	}
	if code, _ := jsonOf(t, g, "/admin/analytics/data/agents.json"); code != 200 {
		t.Errorf("agents: %d", code)
	}
	if rec := g.do("GET", "/admin/analytics/data/overview.json", "", nil, nil); rec.Code == 200 {
		t.Error("the JSON endpoints need a session")
	}
}

func TestRobotsPolicyOwnerOnly(t *testing.T) {
	t.Parallel()

	g, fs := newAnalyticsRig(t)
	if rec := g.do("POST", "/admin/analytics/crawlers/robots", "editor-token", url.Values{"purpose.training": {"deny"}}, nil); rec.Code != 403 || len(fs.denied) != 0 {
		t.Fatalf("editor: %d", rec.Code)
	}
	rec := g.do("POST", "/admin/analytics/crawlers/robots", "owner-token", func() url.Values {
		f := url.Values{"purpose.bogus": {"deny"}}
		for _, p := range robotsPurposes {
			f.Set("purpose."+string(p), "allow")
		}
		f.Set("purpose.training", "deny")
		f.Set("purpose.seo", "deny")
		return f
	}(), nil)
	if rec.Code != 303 || len(fs.denied) != 2 {
		t.Fatalf("owner: %d %v", rec.Code, fs.denied)
	}
	// The form shows the stored policy (training and seo blocked), and an editor
	// sees badges and no Save. The page that hosts the form arrives with the
	// Crawlers section; this renders the same controls from the same data.
	form, err := g.a.robotsData(context.Background(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	controls := func(owner bool) string {
		var b strings.Builder
		pf := ui.AiaPolicyForm{CSRF: "tok", Owner: owner}
		out, err := g.a.RenderPartial("hm/aia-policy-start", pf)
		if err != nil {
			t.Fatal(err)
		}
		b.WriteString(string(out))
		for _, p := range aiaPolicies(form, owner) {
			out, err := g.a.RenderPartial("hm/aia-policy", p)
			if err != nil {
				t.Fatal(err)
			}
			b.WriteString(string(out))
		}
		out, err = g.a.RenderPartial("hm/aia-policy-end", pf)
		if err != nil {
			t.Fatal(err)
		}
		b.WriteString(string(out))
		return b.String()
	}
	own := controls(true)
	if n := strings.Count(own, `type="radio" name="purpose.`); n != 16 {
		t.Errorf("the form posts all 8 purposes as radio pairs, found %d radios", n)
	}
	if !strings.Contains(own, `name="purpose.search" value="allow" checked`) || strings.Contains(own, `name="purpose.training" value="allow" checked`) || !strings.Contains(own, `name="purpose.training" value="deny" checked`) || !strings.Contains(own, `name="purpose.seo" value="deny" checked`) {
		t.Error("the form should show the stored policy")
	}
	if ed := controls(false); strings.Contains(ed, "Save") || strings.Contains(ed, `type="radio"`) || strings.Contains(ed, "<form") || !strings.Contains(ed, "Blocked") {
		t.Error("an editor sees no save button and no controls, only the policy as badges")
	}
}

// Save returns to the page the form was on (an allowlist, never a foreign URL),
// keeping the range, and says so in the flash.
func TestRobotsSaveRedirectsToTheSectionItCameFrom(t *testing.T) {
	t.Parallel()

	g, fs := newAnalyticsRig(t)
	all := func(extra url.Values) url.Values {
		f := url.Values{}
		for _, p := range robotsPurposes {
			f.Set("purpose."+string(p), "allow")
		}
		f.Set("purpose.training", "deny")
		for k, v := range extra {
			f[k] = v
		}
		return f
	}
	for _, tc := range []struct{ ret, want string }{
		{"", "/admin/analytics/crawlers"},
		{"/admin/analytics/crawlers", "/admin/analytics/crawlers"},
		{"/admin/analytics/health", "/admin/analytics/health"},
		{"/admin/analytics/agents/gptbot?from=2026-01-01&to=2026-01-31&compare=1&x=1", "/admin/analytics/agents/gptbot?compare=1&from=2026-01-01&to=2026-01-31"},
		{"https://evil.test/", "/admin/analytics/crawlers"},
		{"//evil.test/admin/analytics/health", "/admin/analytics/crawlers"},
		{"/admin/settings", "/admin/analytics/crawlers"},
		{"/admin/analytics/agents/../../x", "/admin/analytics/crawlers"},
		{"/admin/analytics/agents/GPTBot_2", "/admin/analytics/agents/GPTBot_2"},
		{"/admin/analytics/agents/my%20bot?compare=1", "/admin/analytics/agents/my%20bot?compare=1"},
		{"/admin/analytics/agents/%2e%2e", "/admin/analytics/crawlers"},
		{"/admin/analytics/agents/a%0d%0aSet-Cookie:x", "/admin/analytics/agents/a%0D%0ASet-Cookie:x"},
	} {
		rec := g.do("POST", "/admin/analytics/crawlers/robots", "owner-token", all(url.Values{"return": {tc.ret}}), nil)
		if rec.Code != 303 || rec.Header().Get("Location") != tc.want {
			t.Errorf("return=%q: %d %q, want 303 %q", tc.ret, rec.Code, rec.Header().Get("Location"), tc.want)
		}
		if len(fs.denied) != 1 || fs.denied[0] != agents.PurposeTraining {
			t.Errorf("return=%q: denied = %v", tc.ret, fs.denied)
		}
	}
	rec := g.do("POST", "/admin/analytics/crawlers/robots", "editor-token", all(url.Values{"return": {"/admin/analytics/health"}}), nil)
	if rec.Code != 403 || !strings.Contains(rec.Body.String(), "Only the site owner can change the robots policy.") {
		t.Errorf("editor: %d", rec.Code)
	}
}

// robots.json is the current policy for any signed-in role, and does not care
// about a date range.
func TestRobotsJSON(t *testing.T) {
	t.Parallel()

	g, fs := newAnalyticsRig(t)
	fs.denied = []agents.Purpose{agents.PurposeTraining}
	for _, tc := range []struct {
		token string
		owner bool
	}{{"owner-token", true}, {"editor-token", false}} {
		rec := g.do("GET", "/admin/analytics/data/robots.json?from=nonsense", tc.token, nil, nil)
		if rec.Code != 200 || rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s: %d", tc.token, rec.Code)
		}
		var body struct {
			Owner    bool `json:"owner"`
			Purposes []struct {
				Purpose, Label, Action string
				Tokens                 []string `json:"tokens"`
				Ignores                []string `json:"ignores_robots_agents"`
			}
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.Owner != tc.owner || len(body.Purposes) != 8 {
			t.Fatalf("%s: owner=%v purposes=%d", tc.token, body.Owner, len(body.Purposes))
		}
		got := map[string]string{}
		for _, p := range body.Purposes {
			got[p.Purpose] = p.Action
			if p.Label == "" || p.Tokens == nil || p.Ignores == nil {
				t.Errorf("%s: purpose %q has empty label or null lists: %+v", tc.token, p.Purpose, p)
			}
		}
		if got["training"] != "deny" || got["search_index"] != "allow" {
			t.Errorf("%s: actions = %v", tc.token, got)
		}
	}
	if rec := g.do("GET", "/admin/analytics/data/robots.json", "", nil, nil); rec.Code == 200 {
		t.Error("robots.json needs a session")
	}
}

// A rejected range is never used: the pages show the default 30 days (not a
// table row per day of the rejected span) and say so.
func TestRejectedRangeNeverReachesTheDashboards(t *testing.T) {
	t.Parallel()

	g, fs := newAnalyticsRig(t)
	for _, u := range []string{
		"/admin/analytics/crawlers?from=0001-01-01&to=9999-12-31", "/admin/analytics/crawlers?from=2000-01-01&to=2026-01-01",
		"/admin/analytics/crawlers?from=2026-02-01&to=2026-01-01", "/admin/analytics/referrals?from=2000-01-01&to=2026-01-01",
	} {
		rec := g.do("GET", u, "owner-token", nil, nil)
		body := rec.Body.String()
		if rec.Code != 422 || !strings.Contains(body, "Showing the last 30 days") {
			t.Errorf("%s: %d", u, rec.Code)
		}
		if n := strings.Count(body, "<tr>"); n > 4*(defaultRangeDays+3) || len(body) > 400_000 {
			t.Errorf("%s: %d table rows, %d bytes: the rejected range was rendered", u, n, len(body))
		}
		if !fs.lastRange.From.IsZero() && fs.lastRange.From.Before(today().AddDate(0, 0, -defaultRangeDays)) {
			t.Errorf("%s: the store was asked for %v", u, fs.lastRange)
		}
	}
}

func TestPageviewsChartIsAnArea(t *testing.T) {
	t.Parallel()

	g, _ := newAnalyticsRig(t)
	_, m := jsonOf(t, g, "/admin/analytics/data/referrals.json?from="+day(-6)+"&to="+day(0))
	if kind := m["charts"].(map[string]any)["pageviews"].(map[string]any)["kind"]; kind != "area" {
		t.Errorf("pageviews chart kind = %v, want area", kind)
	}
}

func TestRangeLivesInThePageHeader(t *testing.T) {
	t.Parallel()

	g, _ := newAnalyticsRig(t)
	for _, p := range []string{"/admin/analytics", "/admin/analytics/crawlers", "/admin/analytics/content", "/admin/analytics/referrals", "/admin/analytics/health", "/admin/analytics/agents/gptbot"} {
		body := g.do("GET", p, "owner-token", nil, nil).Body.String()
		head := body[strings.Index(body, `<header class="hm-pageheader">`):]
		head = head[:strings.Index(head, "</header>")]
		for _, want := range []string{`class="hm-daterange"`, "Custom range"} {
			if !strings.Contains(head, want) {
				t.Errorf("%s: page header missing %q", p, want)
			}
		}
		if strings.Count(body, `class="hm-daterange"`) != 1 {
			t.Errorf("%s: want exactly one range control", p)
		}
	}
}

// Export CSV is gone from analytics: the URL falls through to the admin
// fallback and no analytics page offers it.
func TestAnalyticsHasNoExportCSV(t *testing.T) {
	t.Parallel()

	g, _ := newAnalyticsRig(t)
	if rec := g.do("GET", "/admin/analytics/export.csv?kind=crawlers", "owner-token", nil, nil); rec.Code != 404 {
		t.Errorf("export.csv = %d, want 404", rec.Code)
	}
	for _, p := range []string{"/admin/analytics", "/admin/analytics/crawlers", "/admin/analytics/content", "/admin/analytics/referrals", "/admin/analytics/health"} {
		if strings.Contains(g.do("GET", p, "owner-token", nil, nil).Body.String(), "Export CSV") {
			t.Errorf("%s still offers Export CSV", p)
		}
	}
}

func daily(from string, n int) matrix {
	t, _ := time.Parse(dayLayout, from)
	m := matrix{Cols: []string{"A"}, Vals: map[string][]int64{"A": {}}}
	for i := 0; i < n; i++ {
		m.Days = append(m.Days, t.AddDate(0, 0, i).Format(dayLayout))
		m.Vals["A"] = append(m.Vals["A"], int64(i+1))
	}
	return m
}

func TestBucketsByISOWeekBeyondAMonth(t *testing.T) {
	t.Parallel()

	// 31 days stay daily, 32 become weekly
	if m, unit := daily("2026-09-14", 31).bucketed(); unit != "day" || len(m.Days) != 31 {
		t.Errorf("31 days = %s x%d, want day x31", unit, len(m.Days))
	}
	// Wed 2026-09-16 .. 32 days: partial first week (Wed-Sun), whole weeks, partial last
	m := daily("2026-09-16", 32)
	w, unit := m.bucketed()
	if unit != "week" {
		t.Fatalf("unit = %s, want week", unit)
	}
	wantDays := []string{"2026-09-14", "2026-09-21", "2026-09-28", "2026-10-05", "2026-10-12"}
	if strings.Join(w.Days, ",") != strings.Join(wantDays, ",") {
		t.Fatalf("weeks = %v, want %v (labelled by their Monday, the first one before the range)", w.Days, wantDays)
	}
	// 16,17,18,19,20 are Wed..Sun = values 1..5; the next weeks are 7 days each; the last has 5 days (Mon Oct 12..Fri Oct 16)
	want := []int64{15, 6 + 7 + 8 + 9 + 10 + 11 + 12, 13 + 14 + 15 + 16 + 17 + 18 + 19, 20 + 21 + 22 + 23 + 24 + 25 + 26, 27 + 28 + 29 + 30 + 31 + 32}
	for i, v := range w.Vals["A"] {
		if v != want[i] {
			t.Errorf("week %s = %d, want %d", w.Days[i], v, want[i])
		}
	}
	var sum, daySum int64
	for _, v := range w.Vals["A"] {
		sum += v
	}
	for _, v := range m.Vals["A"] {
		daySum += v
	}
	if sum != daySum {
		t.Errorf("weekly total %d != daily total %d", sum, daySum)
	}
}

func TestWeeklyWeekStartsOnMondayAcrossYearEnd(t *testing.T) {
	t.Parallel()

	// Sun 2027-01-03 belongs to the ISO week of Mon 2026-12-28; Mon 2027-01-04 starts the next
	w := daily("2026-12-28", 40).weekly()
	if w.Days[0] != "2026-12-28" || w.Days[1] != "2027-01-04" {
		t.Errorf("weeks = %v", w.Days[:2])
	}
	if w.Vals["A"][0] != 1+2+3+4+5+6+7 {
		t.Errorf("first week = %d", w.Vals["A"][0])
	}
	// Sunday-only start: its own week is labelled by the Monday six days earlier
	if s := daily("2026-09-20", 40).weekly(); s.Days[0] != "2026-09-14" || s.Vals["A"][0] != 1 {
		t.Errorf("sunday start: %v %v", s.Days[0], s.Vals["A"][0])
	}
}

// The Tremor-style pieces are wired into the dashboards with real numbers.
// The overview share chart is a doughnut of fetches by operator.
func TestOverviewShareChartJSON(t *testing.T) {
	t.Parallel()

	g, _ := newAnalyticsRig(t)
	code, m := jsonOf(t, g, "/admin/analytics/data/overview.json")
	share, _ := m["charts"].(map[string]any)["share"].(map[string]any)
	if code != 200 || share["kind"] != "doughnut" || share["center"] != "fetches" || len(share["labels"].([]any)) != 2 {
		t.Errorf("overview share chart = %v", share)
	}
}

func TestStatChangeOfNeedsSomethingToCompare(t *testing.T) {
	t.Parallel()

	if c := ui.StatChangeOf(0, 0, fmtNum); c != nil {
		t.Errorf("0 vs 0 = %+v, want nil", c)
	}
	if c := ui.StatChangeOf(5, 0, fmtNum); c == nil || c.Text != "+5" || c.Caption != "vs previous period" {
		t.Errorf("5 vs 0 = %+v", c)
	}
}

func TestStatChangeLowerIsBetter(t *testing.T) {
	t.Parallel()

	if c := ui.StatChangeOf(3, 5, fmtNum).LowerIsBetter(); c.Dir != "down" || c.Tone != "success" {
		t.Errorf("a fall in a lower-is-better metric = %+v, want success", c)
	}
	if c := ui.StatChangeOf(5, 3, fmtNum).LowerIsBetter(); c.Dir != "up" || c.Tone != "danger" {
		t.Errorf("a rise in a lower-is-better metric = %+v, want danger", c)
	}
	if c := ui.StatChangeOf(0, 0, fmtNum).LowerIsBetter(); c != nil {
		t.Errorf("nil change = %+v", c)
	}
}

func TestPartialStatVariants(t *testing.T) {
	t.Parallel()

	a := newPartialAdmin(t)
	up := ui.StatChangeOf(112, 100, fmtNum)
	ticker := checkPartial(t, a, "hm/stats", "ticker", map[string]any{"Variant": "ticker", "Items": []ui.Stat{
		{Label: "AI fetches", Value: "112", Change: up}, {Label: "Spoofed", Value: "3", Change: ui.StatChangeOf(3, 5, fmtNum)}, {Label: "Pageviews", Value: "40"}}})
	for _, want := range []string{"&#43;12 (&#43;12.0%)", "vs previous period", `<span class="hm-visually-hidden">up </span>`, `<span class="hm-visually-hidden">down </span>`, "hm-statcard__delta--success", "hm-statcard__delta--danger"} {
		if !strings.Contains(ticker, want) {
			t.Errorf("ticker lacks %q", want)
		}
	}
	ring := checkPartial(t, a, "hm/stats", "ring", map[string]any{"Variant": "ring", "Items": []ui.Stat{
		{Label: "Pages crawled", Value: "550 / 1,000", Pct: 55, Link: "/admin/analytics/crawlers?tab=pages", LinkLabel: "View more"}}})
	if !strings.Contains(ring, `role="img" aria-label="55%"`) || !strings.Contains(ring, `stroke-dasharray="55 100"`) || !strings.Contains(ring, "View more") {
		t.Errorf("ring: %s", ring)
	}
	tiny := checkPartial(t, a, "hm/stats", "ring-tiny", map[string]any{"Variant": "ring", "Items": []ui.Stat{{Label: "Verified", Value: "7 / 511", Pct: 1}}})
	if !strings.Contains(tiny, `stroke-dasharray="2 100"`) {
		t.Errorf("a 1%% ring must still draw a visible arc: %s", tiny)
	}
	strip := checkPartial(t, a, "hm/stats", "strip", map[string]any{"Variant": "strip", "Items": []ui.Stat{
		{Label: "AI fetches", Value: "112", Change: up}, {Label: "Spoofed", Value: "3"}, {Label: "Pageviews", Value: "40"}, {Label: "Agents", Value: "9"}}})
	if !strings.Contains(strip, `class="hm-statgrid hm-statgrid--strip"`) || strings.Count(strip, `<dl class="hm-statcard hm-statcard--ticker">`) != 4 {
		t.Errorf("strip: %s", strip)
	}
	status := checkPartial(t, a, "hm/stats", "status", map[string]any{"Variant": "ticker", "Items": []ui.Stat{
		{Label: "Version", Value: "v1.4.2"}, {Label: "Status", Badge: &ui.Badge{Label: "Up to date", Kind: "success"}}}})
	if strings.Count(status, `class="hm-statcard__value"`) != 1 || !strings.Contains(status, `<dd class="hm-statcard__status"><span class="hm-badge hm-badge--success">`) {
		t.Errorf("a status card shows a badge, not a second value style: %s", status)
	}
	checkPartial(t, a, "hm/stats", "overview", map[string]any{"Variant": "overview", "Title": "Coverage", "Description": "How much of the site agents read.", "Items": []ui.Stat{
		{Label: "Fetched", Detail: "40 of 50 published pages", Pct: 80}, {Label: "Verified", Detail: "30 of 40 fetches", Pct: 75}}})
}

func TestRangePresetsAreUTCAndInsideTheCap(t *testing.T) {
	t.Parallel()

	// 2026-10-16 is late in the evening in UTC-8 and already the 17th in UTC+10:
	// presets follow the UTC day of the clock, never the server's local zone.
	now := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	want := map[string][2]string{
		"Today":         {"2026-10-16", "2026-10-16"},
		"Last 7 days":   {"2026-10-10", "2026-10-16"},
		"Last 30 days":  {"2026-09-17", "2026-10-16"},
		"Last 3 months": {"2026-07-16", "2026-10-16"},
		"Last 6 months": {"2026-04-16", "2026-10-16"},
		"Month to date": {"2026-10-01", "2026-10-16"},
		"Year to date":  {"2026-01-01", "2026-10-16"},
	}
	got := rangePresets(now)
	if len(got) != len(want) {
		t.Fatalf("%d presets, want %d", len(got), len(want))
	}
	for _, p := range got {
		w, ok := want[p.Label]
		if !ok || p.From.Format(dayLayout) != w[0] || p.To.Format(dayLayout) != w[1] || p.From.Location() != time.UTC {
			t.Errorf("%s = %v to %v, want %v", p.Label, p.From, p.To, w)
		}
		if _, msg := parseRangeRaw(url.Values{"from": {p.From.Format(dayLayout)}, "to": {p.To.Format(dayLayout)}}); msg != "" {
			t.Errorf("%s is refused by the range parser: %s", p.Label, msg)
		}
	}
}

func TestDatePickerRendersOnEveryAnalyticsPage(t *testing.T) {
	t.Parallel()

	g, _ := newAnalyticsRig(t)
	for _, p := range []string{"/admin/analytics", "/admin/analytics/crawlers", "/admin/analytics/content", "/admin/analytics/referrals", "/admin/analytics/health", "/admin/analytics/agents/gptbot"} {
		body := g.do("GET", p, "owner-token", nil, nil).Body.String()
		for _, want := range []string{
			`popovertarget="range-pop"`, `id="range-pop"`, `popover`, "Today", "Last 3 months", "Year to date",
			`<input class="hm-field__control" type="date" id="range-from" name="from"`, `name="to"`, `max="` + day(0) + `"`,
			`popovertargetaction="hide"><span>Cancel</span>`, `type="submit" class="hm-btn hm-btn--primary"><span>Apply</span>`,
			// A preset is a link that applies at once; Cancel/Apply only serve the custom dates.
			`<a class="hm-daterange__preset" href="/admin/analytics`, `<div class="hm-daterange__actions">`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("%s: picker lacks %q", p, want)
			}
		}
		if strings.Contains(body, "<details class=\"hm-daterange__custom\"") {
			t.Errorf("%s: the Custom range disclosure should be gone", p)
		}
	}
}

func TestDatePickerShowsPresetNameOrDates(t *testing.T) {
	// Pinned (so not parallel) to the 30th of a 30 day month, where Last 30 days
	// and Month to date span the same days and the first listed must name it.
	analyticsNow = func() time.Time { return time.Date(2026, time.September, 30, 12, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { analyticsNow = time.Now })

	g, _ := newAnalyticsRig(t)
	body := g.do("GET", "/admin/analytics/crawlers?from="+day(-29)+"&to="+day(0), "owner-token", nil, nil).Body.String()
	wantPreset := "<span>Last 30 days · " + span(today().AddDate(0, 0, -29), today()) + "</span></button>"
	if !strings.Contains(body, wantPreset) {
		t.Errorf("button should show the preset name and span %q", wantPreset)
	}
	if !strings.Contains(body, `popovertarget="range-pop"`) || !strings.Contains(body, `id="range-from"`) {
		t.Error("popover ids default to range")
	}
	body = g.do("GET", "/admin/analytics/crawlers?from="+day(-3)+"&to="+day(-1), "owner-token", nil, nil).Body.String()
	want := fmtDate(today().AddDate(0, 0, -3)) + " - " + fmtDate(today().AddDate(0, 0, -1))
	if !strings.Contains(body, "<span>"+want+"</span>") {
		t.Errorf("button should show %q", want)
	}
}

// The picker span is day-month like fmtDate and the tracker caption, and a
// one day range ("Today") shows one date.
func TestSpanIsDayMonth(t *testing.T) {
	t.Parallel()

	d := func(day int) time.Time { return time.Date(2026, time.September, day, 0, 0, 0, 0, time.UTC) }
	if got, want := span(d(2), time.Date(2026, time.October, 19, 0, 0, 0, 0, time.UTC)), "2 Sep – 19 Oct"; got != want {
		t.Errorf("span = %q, want %q", got, want)
	}
	if got, want := span(d(2), d(2)), "2 Sep"; got != want {
		t.Errorf("one day span = %q, want %q", got, want)
	}
}

// A big site's total must fit the donut's centre: share charts print it compact.
func TestShareDonutCentreIsCompact(t *testing.T) {
	t.Parallel()

	c, _ := matrix{}.share("cap", "Agent", "fetches")
	if c.Kind != "doughnut" || c.Format != "compact" {
		t.Errorf("share chart = kind %q format %q, want doughnut compact", c.Kind, c.Format)
	}
}

func TestAnalyticsPagesJSONCoverageFields(t *testing.T) {
	t.Parallel()

	g, _ := newAnalyticsRig(t)
	code, m := jsonOf(t, g, "/admin/analytics/data/pages.json")
	if code != 200 {
		t.Fatalf("pages: %d %v", code, m)
	}
	if m["published_pages_total"] != float64(120) {
		t.Errorf("published_pages_total = %v, want 120", m["published_pages_total"])
	}
	top := m["top"].([]any)[0].(map[string]any)
	if top["schema_slug"] != "page" {
		t.Errorf("top[0].schema_slug = %v, want page", top["schema_slug"])
	}
}

func TestAnalyticsPagesJSONByType(t *testing.T) {
	t.Parallel()

	g, _ := newAnalyticsRig(t)
	code, m := jsonOf(t, g, "/admin/analytics/data/pages.json?from="+day(-9)+"&to="+day(0))
	if code != 200 {
		t.Fatalf("pages: %d %v", code, m)
	}
	c := m["charts"].(map[string]any)["by_type"].(map[string]any)
	if c["kind"] != "bar" || c["stacked"] != true || c["bucket"] != nil {
		t.Errorf("by_type spec = %v", c)
	}
	var labels []string
	for _, d := range c["datasets"].([]any) {
		labels = append(labels, d.(map[string]any)["label"].(string))
	}
	if strings.Join(labels, ",") != "Blog post,Other pages" {
		t.Errorf("by_type series = %v", labels)
	}
	never := m["never_fetched_by_type"].([]any)
	var sum float64
	for _, n := range never {
		sum += n.(map[string]any)["count"].(float64)
	}
	if first := never[0].(map[string]any); first["label"] != "Blog post" || first["schema_slug"] != "blog-post" || sum != m["never_fetched_total"].(float64) {
		t.Errorf("never_fetched_by_type = %v (total %v)", never, m["never_fetched_total"])
	}

	_, wk := jsonOf(t, g, "/admin/analytics/data/pages.json?from="+day(-39)+"&to="+day(0))
	if wk["charts"].(map[string]any)["by_type"].(map[string]any)["bucket"] != "week" {
		t.Errorf("40 days should be weekly: %v", wk["charts"].(map[string]any)["by_type"])
	}
}

func TestSchemaLabel(t *testing.T) {
	t.Parallel()

	for in, want := range map[string]string{"blog-post": "Blog post", "page": "Page", "": "Other pages", "case_study": "Case study"} {
		if got := schemaLabel(in); got != want {
			t.Errorf("schemaLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAnalyticsOverviewJSONHealth(t *testing.T) {
	t.Parallel()

	g, _ := newAnalyticsRig(t)
	code, m := jsonOf(t, g, "/admin/analytics/data/overview.json?from="+day(-2)+"&to="+day(0))
	if code != 200 {
		t.Fatalf("overview: %d %v", code, m)
	}
	tot := m["totals"].(map[string]any)
	if tot["status_4xx"] != float64(8) || tot["status_5xx"] != float64(3) {
		t.Errorf("status totals = %v/%v, want 8/3", tot["status_4xx"], tot["status_5xx"])
	}
	series := func(chart, label string) []any {
		c := m["charts"].(map[string]any)[chart].(map[string]any)
		for _, d := range c["datasets"].([]any) {
			if d.(map[string]any)["label"] == label {
				return d.(map[string]any)["data"].([]any)
			}
		}
		t.Fatalf("%s has no series %q: %v", chart, label, c)
		return nil
	}
	for _, c := range []struct {
		chart, label string
		want         []float64
	}{
		{"verification", "Verified", []float64{0, 30, 10}},
		{"verification", "Spoofed", []float64{0, 2, 0}},
		{"status_classes", "4xx client error", []float64{0, 5, 3}},
		{"status_classes", "5xx server error", []float64{0, 1, 2}},
		{"errors_per_day", "Failed fetches", []float64{0, 6, 5}},
		{"errors_per_day", "All fetches", []float64{0, 100, 50}},
	} {
		got := series(c.chart, c.label)
		for i, w := range c.want {
			if got[i] != w {
				t.Errorf("%s/%s = %v, want %v", c.chart, c.label, got, c.want)
				break
			}
		}
	}
	if m["charts"].(map[string]any)["status_classes"].(map[string]any)["stacked"] != true {
		t.Error("status_classes should be stacked")
	}
}

func TestAnalyticsFirstCrawlsJSONBuckets(t *testing.T) {
	t.Parallel()

	g, _ := newAnalyticsRig(t)
	code, m := jsonOf(t, g, "/admin/analytics/data/first-crawls.json")
	if code != 200 {
		t.Fatalf("first crawls: %d %v", code, m)
	}
	it := m["items"].([]any)[0].(map[string]any)
	if it["after_publish_seconds"] != float64(7200) || it["after_publish"] != "2 h" {
		t.Errorf("item = %v, want 7200 seconds and %q", it, "2 h")
	}
	var sum float64
	for _, b := range m["buckets"].([]any) {
		sum += b.(map[string]any)["count"].(float64)
	}
	first := m["buckets"].([]any)[0].(map[string]any)
	if first["label"] != "Same day" || first["count"] != float64(1) || sum != m["total"].(float64) {
		t.Errorf("buckets = %v (total %v)", m["buckets"], m["total"])
	}
	by := m["by_agent"].([]any)[0].(map[string]any)
	if by["agent_id"] != "gptbot" || by["name"] != "GPTBot" || by["count"] != float64(1) {
		t.Errorf("by_agent = %v", m["by_agent"])
	}
}

func TestAnalyticsAgentsJSONIgnoresRobots(t *testing.T) {
	t.Parallel()

	g, _ := newAnalyticsRig(t)
	code, m := jsonOf(t, g, "/admin/analytics/data/agents.json")
	if code != 200 {
		t.Fatalf("agents: %d %v", code, m)
	}
	got := map[string]any{}
	for _, a := range m["agents"].([]any) {
		row := a.(map[string]any)
		got[row["agent_id"].(string)] = row["ignores_robots"]
	}
	if got["gptbot"] != false || got["chatgpt-user"] != true {
		t.Errorf("ignores_robots = %v, want gptbot false and chatgpt-user true", got)
	}
}

func TestAnalyticsAgentJSONPagesRead(t *testing.T) {
	t.Parallel()

	g, _ := newAnalyticsRig(t)
	code, m := jsonOf(t, g, "/admin/analytics/data/agent.json?id=gptbot")
	if code != 200 || m["pages_read"] != float64(17) || m["ignores_robots"] != false {
		t.Errorf("agent: %d pages_read=%v ignores_robots=%v", code, m["pages_read"], m["ignores_robots"])
	}
}

func TestAnalyticsErrorsJSON(t *testing.T) {
	t.Parallel()

	g, _ := newAnalyticsRig(t)
	code, m := jsonOf(t, g, "/admin/analytics/data/errors.json?from="+day(-6)+"&to="+day(0)+"&page=2")
	if code != 200 || m["total"] != float64(2) || m["page"] != float64(2) {
		t.Fatalf("errors: %d %v", code, m)
	}
	byPath := map[string]map[string]any{}
	for _, it := range m["items"].([]any) {
		row := it.(map[string]any)
		byPath[row["path"].(string)] = row
	}
	gone, boom := byPath["/gone"], byPath["/boom"]
	if gone["create_redirect"] != "/admin/settings/redirects/new?source=%2Fgone" || gone["status"] != float64(404) || gone["agents"].([]any)[0] != "GPTBot" {
		t.Errorf("/gone = %v", gone)
	}
	if _, has := boom["create_redirect"]; has || boom["status"] != float64(500) {
		t.Errorf("/boom = %v", boom)
	}
	if code, m := jsonOf(t, g, "/admin/analytics/data/errors.json?from=bad"); code != 400 || m["error"] == nil {
		t.Errorf("bad range: %d %v", code, m)
	}
}

// rangeReferrals answers by range, so the previous period gets its own numbers.
type rangeReferrals struct{ rows map[string]int64 }

func (r rangeReferrals) AIReferrals(_ context.Context, from, to time.Time) ([]analytics.ReferralDay, error) {
	var out []analytics.ReferralDay
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		if n := r.rows[d.Format(dayLayout)]; n > 0 {
			out = append(out, analytics.ReferralDay{Day: d.Format(dayLayout), Assistant: "ChatGPT", Visits: n})
		}
	}
	return out, nil
}
func (r rangeReferrals) Pageviews(_ context.Context, from, to time.Time) ([]analytics.DayCount, error) {
	return []analytics.DayCount{{Day: from.Format(dayLayout), Views: 40}}, nil
}

func TestPreviousRange(t *testing.T) {
	t.Parallel()

	rg := agents.Range{From: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC)}
	got := previousRange(rg)
	if got.From.Format(dayLayout) != "2026-02-19" || got.To.Format(dayLayout) != "2026-02-28" {
		t.Errorf("previousRange = %v to %v, want 2026-02-19 to 2026-02-28", got.From, got.To)
	}
	if !parseCompare(url.Values{"compare": {"1"}}) || parseCompare(url.Values{"compare": {"0"}}) || parseCompare(url.Values{}) {
		t.Error("parseCompare should be true only for compare=1")
	}
}

func TestPivotFixed(t *testing.T) {
	t.Parallel()

	days := []string{"2026-03-01", "2026-03-02"}
	pts := []agents.SeriesPoint{{Day: days[0], Key: "a", Hits: 1}, {Day: days[1], Key: "b", Hits: 2}, {Day: days[1], Key: "c", Hits: 4}, {Day: "2000-01-01", Key: "a", Hits: 9}}
	id := func(s string) string { return s }
	cols := []string{"a", "Other"}
	got := pivotFixed(days, pts, id, cols)
	if fmt.Sprint(got.Vals["a"], got.Vals["Other"]) != "[1 0] [0 6]" || fmt.Sprint(got.Cols) != "[a Other]" {
		t.Errorf("with Other: %v", got.Vals)
	}
	got = pivotFixed(days, pts, id, []string{"a"})
	if fmt.Sprint(got.Vals["a"]) != "[1 0]" || len(got.Vals) != 1 {
		t.Errorf("without Other the rest is dropped: %v", got.Vals)
	}
	if fmt.Sprint(cols) != "[a Other]" {
		t.Errorf("cols mutated: %v", cols)
	}
}

func TestCompareOverviewJSON(t *testing.T) {
	t.Parallel()

	g, fs := newAnalyticsRig(t)
	from, to := day(-9), day(0)
	fs.prev = &agents.OverviewTotals{AIFetches: 700, Spoofed: 3}
	fs.prevTo = today().AddDate(0, 0, -10)
	fs.prevSeries = []agents.SeriesPoint{{Day: day(-12), Key: "OpenAI", Hits: 9}, {Day: day(-11), Key: "Nobody", Hits: 5}}

	code, m := jsonOf(t, g, "/admin/analytics/data/overview.json?compare=1&from="+from+"&to="+to)
	if code != 200 {
		t.Fatalf("overview: %d %v", code, m)
	}
	prev := m["previous"].(map[string]any)
	if prev["from"] != day(-19) || prev["to"] != day(-10) || prev["totals"].(map[string]any)["ai_fetches"] != float64(700) {
		t.Errorf("previous = %v", prev)
	}
	fetches := m["charts"].(map[string]any)["fetches"].(map[string]any)
	pc := fetches["previous"].(map[string]any)
	if pc["from"] != day(-19) || len(pc["labels"].([]any)) != 10 {
		t.Errorf("fetches.previous = %v", pc)
	}
	// Same series as the current chart, by name: "Nobody" is not one of them.
	var cur, got []string
	for _, d := range fetches["datasets"].([]any) {
		cur = append(cur, d.(map[string]any)["label"].(string))
	}
	for _, d := range pc["datasets"].([]any) {
		got = append(got, d.(map[string]any)["label"].(string))
	}
	if fmt.Sprint(cur) != fmt.Sprint(got) || pc["datasets"].([]any)[0].(map[string]any)["data"].([]any)[7] != float64(9) {
		t.Errorf("previous series %v vs current %v: %v", got, cur, pc["datasets"])
	}
	for _, name := range []string{"verification", "status_classes", "errors_per_day"} {
		if m["charts"].(map[string]any)[name].(map[string]any)["previous"] == nil {
			t.Errorf("%s has no previous", name)
		}
	}

	rec := g.do("GET", "/admin/analytics/data/overview.json?from="+from+"&to="+to, "owner-token", nil, nil)
	if strings.Contains(rec.Body.String(), "previous") {
		t.Errorf("previous must be absent without compare: %s", rec.Body.String())
	}
}

func TestCompareWeeklyBuckets(t *testing.T) {
	t.Parallel()

	g, fs := newAnalyticsRig(t)
	fs.prevTo = today().AddDate(0, 0, -40)
	fs.prevSeries = []agents.SeriesPoint{{Day: day(-45), Key: "OpenAI", Hits: 9}}
	code, m := jsonOf(t, g, "/admin/analytics/data/overview.json?compare=1&from="+day(-39)+"&to="+day(0))
	if code != 200 {
		t.Fatalf("overview: %d", code)
	}
	c := m["charts"].(map[string]any)["fetches"].(map[string]any)
	if c["bucket"] != "week" {
		t.Errorf("current bucket = %v, want week", c["bucket"])
	}
	labels := c["previous"].(map[string]any)["labels"].([]any)
	if len(labels) < 5 || len(labels) > 7 {
		t.Fatalf("previous weekly labels = %v", labels)
	}
	for _, l := range labels {
		if d, err := time.Parse(dayLayout, l.(string)); err != nil || d.Weekday() != time.Monday {
			t.Errorf("previous label %v is not a Monday", l)
		}
	}
}

func TestCompareReferralsAndPagesJSON(t *testing.T) {
	t.Parallel()

	fs := &fakeAgentStats{}
	g := newRig(t, func(d *Deps) {
		d.Growth.Agents = fs
		d.Growth.Referrals = rangeReferrals{rows: map[string]int64{day(-1): 7, day(-12): 5}}
	})
	code, m := jsonOf(t, g, "/admin/analytics/data/referrals.json?compare=1&from="+day(-9)+"&to="+day(0))
	if code != 200 {
		t.Fatalf("referrals: %d %v", code, m)
	}
	prev := m["previous"].(map[string]any)
	tot := prev["totals"].(map[string]any)
	if prev["from"] != day(-19) || tot["ai_referred_visits"] != float64(5) || tot["pageviews"] != float64(40) {
		t.Errorf("referrals.previous = %v", prev)
	}
	charts := m["charts"].(map[string]any)
	for _, name := range []string{"referrals", "pageviews"} {
		if charts[name].(map[string]any)["previous"] == nil {
			t.Errorf("%s has no previous", name)
		}
	}
	if m["ai_referred_visits"] != float64(7) {
		t.Errorf("current visits = %v, want 7", m["ai_referred_visits"])
	}

	code, pm := jsonOf(t, g, "/admin/analytics/data/pages.json?compare=1&from="+day(-9)+"&to="+day(0))
	if code != 200 || pm["charts"].(map[string]any)["by_type"].(map[string]any)["previous"] == nil {
		t.Errorf("pages by_type has no previous: %d %v", code, pm["charts"])
	}
	_, off := jsonOf(t, g, "/admin/analytics/data/pages.json?from="+day(-9)+"&to="+day(0))
	if off["charts"].(map[string]any)["by_type"].(map[string]any)["previous"] != nil {
		t.Error("by_type previous must be absent without compare")
	}
}

// Every old analytics URL still lands: ?tab= values, pageviews, the agents
// index and the robots page, with the range and compare switch kept. The agent
// URLs go through the real routes (a real route wins over the legacy table).
func TestAnalyticsLegacyURLsRedirect(t *testing.T) {
	t.Parallel()

	g, _ := newAnalyticsRig(t)
	keep := "from=2026-01-01&to=2026-01-31&compare=1"
	for _, tc := range []struct {
		url, want string
		code      int
	}{
		{"/admin/analytics/crawlers?tab=overview", "/admin/analytics/crawlers", 301},
		{"/admin/analytics/crawlers?tab=", "/admin/analytics/crawlers", 301},
		{"/admin/analytics/crawlers?tab=agents", "/admin/analytics/crawlers", 301},
		{"/admin/analytics/crawlers?tab=agents&agent=gptbot", "/admin/analytics/agents/gptbot", 301},
		{"/admin/analytics/crawlers?tab=pages", "/admin/analytics/content", 301},
		{"/admin/analytics/crawlers?tab=first-crawls", "/admin/analytics/content#w-ct-first", 301},
		{"/admin/analytics/crawlers?tab=errors", "/admin/analytics/health#w-h-fail", 301},
		{"/admin/analytics/crawlers?tab=spoofed", "/admin/analytics/health#w-h-ver", 301},
		{"/admin/analytics/crawlers?tab=robots", "/admin/analytics/crawlers#w-cr-list", 301},
		{"/admin/analytics/crawlers?tab=nonsense", "/admin/analytics/crawlers", 301},
		{"/admin/analytics/pageviews", "/admin/analytics/referrals", 301},
		{"/admin/analytics/agents/robots", "/admin/analytics/crawlers#w-cr-list", 301},
		{"/admin/analytics/agents", "/admin/analytics/crawlers", 301},
		{"/admin/analytics/agents/", "/admin/analytics/crawlers", 301},
		{"/admin/analytics?section=ai-health", "/admin/analytics/health", 303},
		{"/admin/analytics?section=ai-crawlers", "/admin/analytics/crawlers", 303},
	} {
		sep := "?"
		if strings.Contains(tc.url, "?") {
			sep = "&"
		}
		for _, carry := range []bool{false, true} {
			u, want := tc.url, tc.want
			if carry {
				u += sep + keep
				path, frag, _ := strings.Cut(want, "#")
				want = path + "?" + keep
				if frag != "" {
					want += "#" + frag
				}
				if strings.HasPrefix(tc.url, "/admin/analytics/agents") && !strings.Contains(tc.url, "robots") {
					want = tc.want + "?" + keep // the legacy table appends the query as given
				}
			}
			rec := g.do("GET", u, "owner-token", nil, nil)
			loc := rec.Header().Get("Location")
			if rec.Code != tc.code || (loc != want && !sameURL(loc, want)) {
				t.Errorf("%s: %d %s, want %d %s", u, rec.Code, loc, tc.code, want)
			}
		}
	}
	// With no tab there is nothing to redirect: the page answers.
	if rec := g.do("GET", "/admin/analytics/crawlers?from=2026-01-01&to=2026-01-31", "owner-token", nil, nil); rec.Code != 200 {
		t.Errorf("crawlers without tab: %d", rec.Code)
	}
	if rec := g.do("GET", "/admin/analytics/export.csv", "owner-token", nil, nil); rec.Code != 404 {
		t.Errorf("export.csv = %d, want 404", rec.Code)
	}
}

// sameURL compares two URLs ignoring the order of their query parameters.
func sameURL(a, b string) bool {
	pa, errA := url.Parse(a)
	pb, errB := url.Parse(b)
	return errA == nil && errB == nil && pa.Path == pb.Path && pa.Fragment == pb.Fragment && pa.Query().Encode() == pb.Query().Encode()
}

func compareBody(t *testing.T, g *rig, u string) string {
	t.Helper()
	rec := g.do("GET", u, "owner-token", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("%s: %d", u, rec.Code)
	}
	return rec.Body.String()
}

// The compare switch lives in the picker, every preset and every sidebar link
// carries compare=1 and the range, and the switch is the only compare input.
func TestAnalyticsCompareIsCarried(t *testing.T) {
	t.Parallel()

	g, _ := newAnalyticsRig(t)
	from, to := day(-29), day(0)
	body := compareBody(t, g, "/admin/analytics/crawlers?from="+from+"&to="+to+"&compare=1")
	prev := span(today().AddDate(0, 0, -59), today().AddDate(0, 0, -30))
	if !regexp.MustCompile(`(?s)role="switch"[^>]*name="compare"[^>]*checked|name="compare"[^>]*role="switch"[^>]*checked`).MatchString(body) {
		t.Error("the compare switch should be on")
	}
	for _, want := range []string{`<span class="hm-daterange__vs">vs ` + prev + `</span>`, "Compared with " + prev + "."} {
		if !strings.Contains(body, want) {
			t.Errorf("compare on lacks %q", want)
		}
	}
	presets := regexp.MustCompile(`class="hm-daterange__preset" href="([^"]*)"`).FindAllStringSubmatch(body, -1)
	if len(presets) == 0 {
		t.Fatal("no presets")
	}
	for _, m := range presets {
		if !strings.Contains(m[1], "compare=1") {
			t.Errorf("preset %s lacks compare=1", m[1])
		}
	}
	links := regexp.MustCompile(`class="hm-subnav__link" href="([^"]*)"`).FindAllStringSubmatch(body, -1)
	if len(links) != 8 {
		t.Fatalf("%d sub-sidebar links, want 8", len(links))
	}
	for _, m := range links {
		if h := html.UnescapeString(m[1]); !strings.Contains(h, "compare=1") || !strings.Contains(h, "from="+from) || !strings.Contains(h, "to="+to) {
			t.Errorf("sidebar link %s lacks the carry", h)
		}
	}
	for _, want := range []string{`<input type="hidden" name="from" value="` + from + `">`, `<input type="hidden" name="compare" value="1">`} {
		sel := body[strings.Index(body, `class="hm-subnav-select"`):]
		if !strings.Contains(sel[:strings.Index(sel, "</form>")], want) {
			t.Errorf("phone select lacks %q", want)
		}
	}
	// the picker form posts compare through the switch, never as a hidden input
	pop := body[strings.Index(body, `data-hm-daterange`):]
	if strings.Contains(pop[:strings.Index(pop, "</form>")], `type="hidden" name="compare"`) {
		t.Error("the picker must not hold a hidden compare input")
	}

	// off: no compare anywhere, switch unchecked
	off := compareBody(t, g, "/admin/analytics/crawlers?from="+from+"&to="+to)
	if strings.Contains(off, "compare=1") || strings.Contains(off, "hm-daterange__vs") || strings.Contains(off, `name="compare" value="1" checked`) {
		t.Error("compare off must not carry compare")
	}
	if !strings.Contains(off, `name="compare"`) || !strings.Contains(off, "Compare with "+prev+".") {
		t.Error("compare off still offers the switch")
	}
	// the agent page carries it too
	if ag := compareBody(t, g, "/admin/analytics/agents/gptbot?compare=1"); !strings.Contains(ag, `class="hm-subnav__link" href="/admin/analytics/health?compare=1"`) {
		t.Error("agent page sidebar lacks compare=1")
	}
}

// A previous period that starts before the picker's first day cannot be
// compared: the switch is disabled and says why.
func TestAnalyticsCompareDisabledWithoutEarlierData(t *testing.T) {
	t.Parallel()

	g, _ := newAnalyticsRig(t)
	body := compareBody(t, g, "/admin/analytics/crawlers?from=2020-01-10&to=2020-02-10&compare=1")
	if !strings.Contains(body, "There is no earlier data to compare with this range.") || !strings.Contains(body, "disabled") {
		t.Error("the switch should be disabled with its help text")
	}
	if strings.Contains(body, "hm-daterange__vs") {
		t.Error("no vs line when comparison is unavailable")
	}
}

// Flat traffic must read as equal in every weekly bucket: the previous period
// is lined up day by day with the current one and bucketed by the current
// range's ISO weeks, even when the range does not start on a Monday.
func TestPreviousWeeklyAlignsToCurrentWeeks(t *testing.T) {
	t.Parallel()

	rg := agents.Range{From: time.Date(2026, 7, 23, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)}
	prg := previousRange(rg)
	flat := func(days []string) []agents.SeriesPoint {
		var pts []agents.SeriesPoint
		for _, d := range days {
			pts = append(pts, agents.SeriesPoint{Day: d, Key: "OpenAI", Hits: 10})
		}
		return pts
	}
	name := func(s string) string { return s }
	days, pdays := rangeDays(rg), rangeDays(prg)
	cur := pivot(days, flat(days), name).chart("bar", true)
	got := cur.withPrevious(prg, pivotFixed(pdays, flat(pdays), name, cur.seriesLabels()).chart("bar", true))

	if got.Bucket != "week" || got.Previous == nil {
		t.Fatalf("bucket %q previous %v", got.Bucket, got.Previous)
	}
	if len(got.Previous.Labels) != len(got.Labels) {
		t.Fatalf("previous has %d buckets, current %d", len(got.Previous.Labels), len(got.Labels))
	}
	for i, v := range got.Datasets[0].Data {
		if p := got.Previous.Datasets[0].Data[i]; p != v {
			t.Errorf("bucket %d (%s): previous %d, current %d", i, got.Labels[i], p, v)
		}
	}
	if first := got.Previous.Labels[0]; first != "2026-04-20" {
		t.Errorf("first previous label = %s, want the Monday of the first previous day (2026-04-20)", first)
	}
}

// The attention rate divides AI errors by AI fetches: errors served to search
// or SEO crawlers must not count toward it.
func TestOverviewErrorsCountOnlyAICrawlers(t *testing.T) {
	t.Parallel()

	g, fs := newAnalyticsRig(t)
	fs.health = []agents.HealthDay{{Day: day(0), Hits: 2000, S2xx: 1700, S4xx: 300, AI4xx: 0}}
	code, m := jsonOf(t, g, "/admin/analytics/data/overview.json?from="+day(-6)+"&to="+day(0))
	if code != 200 {
		t.Fatalf("overview: %d", code)
	}
	tot := m["totals"].(map[string]any)
	if tot["status_4xx"] != float64(0) || tot["status_5xx"] != float64(0) {
		t.Errorf("totals count non-AI errors: 4xx %v, 5xx %v", tot["status_4xx"], tot["status_5xx"])
	}
}

// A real page type called "other-pages" or "other" must not merge with the
// no-entity bucket or the overflow "Other" series.
func TestSchemaLabelsNeverCollideWithReservedSeries(t *testing.T) {
	t.Parallel()

	days := []string{day(0)}
	pts := []agents.SeriesPoint{{Day: day(0), Key: "", Hits: 4}, {Day: day(0), Key: "other-pages", Hits: 3}, {Day: day(0), Key: "other", Hits: 2}}
	m := pivot(days, pts, schemaLabel)
	if len(m.Cols) != 3 {
		t.Fatalf("columns = %v, want three distinct series", m.Cols)
	}
	seen := map[string]bool{}
	for _, c := range m.Cols {
		seen[c] = true
	}
	if len(seen) != 3 || m.Vals[otherPages][0] != 4 {
		t.Errorf("columns = %v, other pages = %v", m.Cols, m.Vals[otherPages])
	}
	if schemaLabel("other") == "Other" {
		t.Error(`the page type "other" is labelled like the overflow series`)
	}
}

// An old paged URL keeps its page number on the section that now lists it.
func TestLegacyTabRedirectKeepsPageNumber(t *testing.T) {
	t.Parallel()

	g, _ := newAnalyticsRig(t)
	for u, want := range map[string]string{
		"/admin/analytics/crawlers?tab=pages&npage=3&from=2026-01-01": "/admin/analytics/content?from=2026-01-01&npage=3",
		"/admin/analytics/crawlers?tab=errors&page=2":                 "/admin/analytics/health?fpage=2#w-h-fail",
	} {
		rec := g.do("GET", u, "owner-token", nil, nil)
		if loc := rec.Header().Get("Location"); rec.Code != 301 || !sameURL(loc, want) {
			t.Errorf("%s: %d %s, want %s", u, rec.Code, loc, want)
		}
	}
}

// Screen-reader legends and notes keep acronyms as written.
func TestPurposePhrasesKeepAcronyms(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ purpose, label, want string }{
		{"seo", "SEO", "Robots policy for SEO crawlers"},
		{"other_ai", "Other AI", "Robots policy for all other AI agents"},
		{"search_index", "Search index", "Robots policy for all search index agents"},
		{"social", "Social", "Robots policy for social crawlers"},
	} {
		if got := robotsLegend(tc.purpose, tc.label); got != tc.want {
			t.Errorf("robotsLegend(%q) = %q, want %q", tc.purpose, got, tc.want)
		}
	}
}

// The "Show data" table names daily rows with day-month dates, as the tooltip does.
func TestDataTableDailyRowsUseDayMonthDates(t *testing.T) {
	t.Parallel()

	m := matrix{Days: []string{"2026-01-01", "2026-01-02"}, Cols: []string{"A"}, Vals: map[string][]int64{"A": {1, 2}}}
	tab := m.table("A per day", "Day")
	if tab.Rows[0].Label != "1 Jan 2026" || tab.Rows[1].Label != "2 Jan 2026" {
		t.Errorf("row labels = %q, %q", tab.Rows[0].Label, tab.Rows[1].Label)
	}
}

// The Health page reads the day-by-day health rows once, not once for the
// overview figures and again for itself.
func TestHealthPageReadsHealthSeriesOnce(t *testing.T) {
	t.Parallel()

	g, fs := newAnalyticsRig(t)
	if rec := g.do("GET", "/admin/analytics/health", "owner-token", nil, nil); rec.Code != 200 {
		t.Fatalf("health: %d", rec.Code)
	}
	if fs.healthCalls != 1 {
		t.Errorf("HealthSeries read %d times, want 1", fs.healthCalls)
	}
}
