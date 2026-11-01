package admin

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/admin/a11y"
	"hatamoto.dev/core/internal/agents"
	"hatamoto.dev/core/internal/searchperf"
)

// fakeSearchPerf is a search performance provider with data (or a failure).
type fakeSearchPerf struct{ err error }

func (f *fakeSearchPerf) Summary(_ context.Context, rg searchperf.Range) (searchperf.Summary, bool, error) {
	if f.err != nil {
		return searchperf.Summary{}, false, f.err
	}
	return searchperf.Summary{Impressions: 18400, Clicks: 612, CTR: 0.033, Position: 4.2,
		Daily: []searchperf.DayPoint{{Day: today().AddDate(0, 0, -1), Impressions: 900, Clicks: 30}}}, true, nil
}

func (f *fakeSearchPerf) Pages(_ context.Context, _ searchperf.Range, paths []string) (map[string]searchperf.PageStats, error) {
	return map[string]searchperf.PageStats{"/post/hello": {Impressions: 9100, Clicks: 188, Position: 6.8, Index: "Indexed", TopQuery: "self hosted cms"}}, nil
}

func googleRig(t *testing.T, prov searchperf.Provider) (*rig, *fakeAgentStats) {
	t.Helper()
	fs := &fakeAgentStats{}
	g := newRig(t, func(d *Deps) { d.Growth.Agents, d.Growth.Referrals, d.Growth.SearchPerf = fs, fakeReferrals{}, prov })
	return g, fs
}

func TestGoogleWithoutProviderShowsTheBanner(t *testing.T) {
	t.Parallel()

	g, _ := googleRig(t, nil)
	rec := g.do("GET", "/admin/analytics/google", "owner-token", nil, nil)
	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	for _, want := range []string{"What is Google doing?", "Google AI Overviews and Gemini use the same Googlebot crawl; Google-Extended only controls training use.",
		"Connect Search Console</a> (plugin) to add impressions, clicks, position and index status.",
		"Googlebot crawls", "995", "99% verified", "Pages crawled", "of 144 published", "Publish to first crawl", "6 h",
		"Crawling", "Googlebot fetches per day, stacked by response class.", "google.json", "Pages through Google&#39;s eyes", ">Apply<",
		"Hello &lt;world&gt;", "/post/hello", "556", "Edited page", ", stale", "/docs/v0/gone", ">404<", "Never", "Brand new page"} {
		if !strings.Contains(body, want) {
			t.Errorf("google missing %q", want)
		}
	}
	for _, bad := range []string{"Impressions", "Average position", "Top query", "w-g-search", "data-hm-aia-autosubmit"} {
		if strings.Contains(body, bad) {
			t.Errorf("without a provider the page shows %q", bad)
		}
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
}

func TestGoogleWithProviderDataShowsSearchParts(t *testing.T) {
	t.Parallel()

	prov := &fakeSearchPerf{}
	g, _ := googleRig(t, prov)
	body := g.do("GET", "/admin/analytics/google", "owner-token", nil, nil).Body.String()
	for _, want := range []string{"Impressions", "18,400", "Clicks", "3.3% click-through rate", "Average position", "4.2", "Search performance", "w-g-search",
		"Index", "Impr.", "Position", "Top query", "Indexed", "self hosted cms", "9,100"} {
		if !strings.Contains(body, want) {
			t.Errorf("with data, missing %q", want)
		}
	}
	if strings.Contains(body, "Connect Search Console") {
		t.Error("the banner goes away with data")
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
}

func TestGoogleFailingProviderIsABannerNotAnError(t *testing.T) {
	t.Parallel()

	g, _ := googleRig(t, &fakeSearchPerf{err: errors.New("boom")})
	rec := g.do("GET", "/admin/analytics/google", "owner-token", nil, nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Connect Search Console</a> (plugin)") {
		t.Fatalf("status %d, banner missing", rec.Code)
	}
}

func TestGoogleFilters(t *testing.T) {
	t.Parallel()

	for filter, want := range map[string][]string{
		"":            {"Hello &lt;world&gt;", "/docs/v0/gone", "Edited page", "Brand new page"},
		"all":         {"Hello &lt;world&gt;", "/docs/v0/gone", "Edited page", "Brand new page"},
		"not-crawled": {"Brand new page"},
		"errors":      {"/docs/v0/gone"},
		"stale":       {"Edited page"},
		"nonsense":    {"Hello &lt;world&gt;", "/docs/v0/gone", "Edited page", "Brand new page"},
	} {
		g, fs := googleRig(t, nil)
		body := g.do("GET", "/admin/analytics/google?filter="+filter, "owner-token", nil, nil).Body.String()
		rows := body[strings.Index(body, `id="w-g-pages-title"`):]
		for _, name := range []string{"Hello &lt;world&gt;", "/docs/v0/gone", "Edited page", "Brand new page"} {
			has := strings.Contains(rows, name)
			wanted := false
			for _, w := range want {
				wanted = wanted || w == name
			}
			if has != wanted {
				t.Errorf("filter %q: %q shown=%v, want %v", filter, name, has, wanted)
			}
		}
		wantFilter := filter
		if filter == "nonsense" {
			wantFilter = "all"
		}
		if wantFilter != "" && fs.googleFilter != wantFilter {
			t.Errorf("filter %q reached the store as %q", filter, fs.googleFilter)
		}
		if filter == "errors" && !strings.Contains(rows, `value="errors" checked`) {
			t.Error("the chosen filter is checked")
		}
	}
	// the range survives a filter change: it rides along as hidden fields
	g, _ := googleRig(t, nil)
	body := g.do("GET", "/admin/analytics/google?from="+day(-6)+"&to="+day(0)+"&compare=1", "owner-token", nil, nil).Body.String()
	for _, want := range []string{`<input type="hidden" name="from" value="` + day(-6) + `">`, `<input type="hidden" name="compare" value="1">`} {
		if !strings.Contains(body, want) {
			t.Errorf("the filter form lost %q", want)
		}
	}
}

func TestGoogleJSON(t *testing.T) {
	t.Parallel()

	g, _ := googleRig(t, nil)
	code, m := jsonOf(t, g, "/admin/analytics/data/google.json?from="+day(-1)+"&to="+day(0))
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	charts := m["charts"].(map[string]any)
	var got []string
	var total float64
	for _, d := range charts["status"].(map[string]any)["datasets"].([]any) {
		ds := d.(map[string]any)
		got = append(got, ds["label"].(string))
		for _, v := range ds["data"].([]any) {
			total += v.(float64)
		}
	}
	if strings.Join(got, ",") != "2xx,3xx,4xx,5xx" || total != 54 {
		t.Errorf("status series %v total %v", got, total)
	}
	if _, ok := charts["search"]; ok || m["search_performance"] != false {
		t.Error("no search chart without a provider")
	}

	g, _ = googleRig(t, &fakeSearchPerf{})
	_, m = jsonOf(t, g, "/admin/analytics/data/google.json?from="+day(-1)+"&to="+day(0))
	if _, ok := m["charts"].(map[string]any)["search"]; !ok || m["search_performance"] != true {
		t.Error("the search chart comes with provider data")
	}
	if code, _ := jsonOf(t, g, "/admin/analytics/data/google.json?from=nope"); code != 400 {
		t.Errorf("bad range answers %d", code)
	}
}

func TestMedianWords(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		h    float64
		want string
	}{{0.25, "15 min"}, {6, "6 h"}, {47, "47 h"}, {72, "3 days"}} {
		if got := medianWords(durationHours(tc.h)); got != tc.want {
			t.Errorf("%vh = %q, want %q", tc.h, got, tc.want)
		}
	}
}

var _ agents.StatsStore = (*fakeAgentStats)(nil)

func durationHours(h float64) time.Duration { return time.Duration(h * float64(time.Hour)) }
