package admin

import (
	"context"
	"net/url"
	"strconv"
	"time"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/agents"
	"hatamoto.dev/core/internal/searchperf"
)

// googleLede is the lede of the Google section: what the page shows, and what
// the Googlebot crawl feeds.
const googleLede = "Googlebot crawling from your own logs. Google AI Overviews and Gemini use the same Googlebot crawl; Google-Extended only controls training use."

// googleStatusClasses are the response classes of the crawling chart, in stack order.
var googleStatusClasses = []string{"2xx", "3xx", "4xx", "5xx"}

// googleFilters are the filters of the page table, as the ?filter= values.
var googleFilters = []struct{ Value, Label string }{
	{agents.GoogleFilterAll, "All"}, {agents.GoogleFilterNotCrawled, "Not crawled"}, {agents.GoogleFilterErrors, "Errors"}, {agents.GoogleFilterStale, "Stale"},
}

// googleView is what google.json serves and the page draws from: one builder
// for the chart, the table under it and the JSON.
type googleView struct {
	From   string               `json:"from"`
	To     string               `json:"to"`
	Charts map[string]chartSpec `json:"charts"`
	// SearchPerformance is true when a provider supplied data (the "search" chart exists).
	SearchPerformance bool `json:"search_performance"`

	statusM matrix
	searchM matrix
	summary searchperf.Summary
}

// googleData builds the Googlebot status chart and, when a search performance
// provider has data for the range, its daily chart. A missing or failing
// provider only leaves the search parts out.
func (a *Admin) googleData(ctx context.Context, rg agents.Range) (*googleView, error) {
	stats, ok := a.agentStats()
	if !ok {
		return nil, errNoService
	}
	pts, err := stats.GooglebotStatusSeries(ctx, rg)
	if err != nil {
		return nil, err
	}
	days := rangeDays(rg)
	v := &googleView{From: rg.From.Format(dayLayout), To: rg.To.Format(dayLayout), Charts: map[string]chartSpec{}}
	v.statusM = pivotFixed(days, pts, func(k string) string { return k }, googleStatusClasses)
	v.Charts["status"] = v.statusM.chart("bar", true)

	prov := a.deps.Growth.SearchPerf
	if prov == nil {
		return v, nil
	}
	sum, ok, err := prov.Summary(ctx, searchperf.Range{From: rg.From, To: rg.To})
	if err != nil {
		a.logger().Error("search performance summary failed", "err", err)
		return v, nil
	}
	if !ok {
		return v, nil
	}
	imp, clk := map[string]int64{}, map[string]int64{}
	for _, d := range sum.Daily {
		k := d.Day.Format(dayLayout)
		imp[k], clk[k] = d.Impressions, d.Clicks
	}
	v.summary, v.SearchPerformance = sum, true
	v.searchM = matrix{Days: days, Cols: []string{"Impressions", "Clicks"}, Vals: map[string][]int64{"Impressions": dayCounts(days, imp), "Clicks": dayCounts(days, clk)}}
	combo := v.searchM.chart("bar", false)
	combo.Datasets[1].Type, combo.Datasets[1].Axis = "line", "right"
	v.Charts["search"] = combo
	return v, nil
}

func (a *Admin) googleJSON(ctx context.Context, _ url.Values, rg agents.Range) (any, int, string) {
	v, err := a.googleData(ctx, rg)
	return a.result(v, err, "google")
}

// googlePage is the body of the Google section.
type googlePage struct {
	// Banner is true when no search performance data is connected: the one
	// notice that stands in for every search performance part.
	Banner bool
	KPIs   []ui.Stat
	Chart  ui.AiaChart
	// Search is the impressions and clicks chart; set only with provider data.
	Search *ui.AiaChart
	// Filter is the radio set of the table's GET form; Carry the range fields it keeps.
	Filter ui.Segmented
	Carry  []hiddenField
	Rows   ui.AiaRows
}

func (p *googlePage) lede() string { return googleLede }

// medianWords is a duration as the KPI words it: minutes under an hour, hours under two days, else days.
func medianWords(d time.Duration) string {
	switch {
	case d < time.Hour:
		return strconv.Itoa(int(d/time.Minute)) + " min"
	case d < 48*time.Hour:
		return strconv.Itoa(int(d.Round(time.Hour)/time.Hour)) + " h"
	}
	return strconv.Itoa(int(d.Round(24*time.Hour)/(24*time.Hour))) + " days"
}

func (a *Admin) buildGoogle(ctx context.Context, q url.Values, rg agents.Range, _ bool) (any, error) {
	stats, ok := a.agentStats()
	if !ok {
		return nil, errNoService
	}
	v, err := a.googleData(ctx, rg)
	if err != nil {
		return nil, err
	}
	k, err := stats.GoogleKPIs(ctx, rg)
	if err != nil {
		return nil, err
	}
	filter := agents.GoogleFilterAll
	for _, f := range googleFilters {
		if q.Get("filter") == f.Value {
			filter = f.Value
		}
	}
	npage := atoiMin(q.Get("gpage"), 1)
	rows, total, err := stats.GooglePages(ctx, rg, filter, analyticsPerPage, (npage-1)*analyticsPerPage)
	if err != nil {
		return nil, err
	}

	crawls := ui.Stat{Label: "Googlebot crawls", Value: fmtNum(k.Hits), Note: "No Googlebot fetches in this period."}
	if k.Hits > 0 {
		crawls.Note = fixed(ratio(k.Verified, k.Hits), 0) + " verified"
	}
	pages := ui.Stat{Label: "Pages crawled", Value: strconv.Itoa(k.PagesCrawled), Note: "of " + strconv.Itoa(k.Published) + " published"}
	first := ui.Stat{Label: "Publish to first crawl", Value: "n/a", Note: "No page was first crawled in this period."}
	if k.MedianFirstCrawl != nil {
		first.Value, first.Note = medianWords(*k.MedianFirstCrawl), "median, pages published since tracking began"
	}
	p := &googlePage{Banner: !v.SearchPerformance, KPIs: []ui.Stat{crawls, pages, first}}
	p.Chart = ui.AiaChart{ID: "w-g-crawl", Title: "Crawling", Description: "Googlebot fetches per " + per(rg) + ", stacked by response class.",
		Src: a.dataURL("google.json", rg, false), Chart: "status", Hero: true,
		Override: `{"4xx":{"cls":"hm-viz--warning"},"5xx":{"cls":"hm-viz--danger"}}`,
		Table:    v.statusM.table("Googlebot fetches per day by response class", "Day")}

	var gsc map[string]searchperf.PageStats
	if v.SearchPerformance {
		s := v.summary
		p.KPIs = append(p.KPIs,
			ui.Stat{Label: "Impressions", Value: fmtNum(s.Impressions)},
			ui.Stat{Label: "Clicks", Value: fmtNum(s.Clicks), Note: strconv.FormatFloat(s.CTR*100, 'f', 1, 64) + "% click-through rate"},
			ui.Stat{Label: "Average position", Value: strconv.FormatFloat(s.Position, 'f', 1, 64)})
		p.Search = &ui.AiaChart{ID: "w-g-search", Title: "Search performance", Description: "Impressions and clicks per " + per(rg) + ".",
			Src: a.dataURL("google.json", rg, false), Chart: "search", Table: v.searchM.table("Impressions and clicks per day", "Day")}
		var paths []string
		for _, r := range rows {
			if r.Path != "" {
				paths = append(paths, r.Path)
			}
		}
		if len(paths) > 0 {
			if gsc, err = a.deps.Growth.SearchPerf.Pages(ctx, searchperf.Range{From: rg.From, To: rg.To}, paths); err != nil {
				a.logger().Error("search performance pages failed", "err", err)
				gsc = nil
			}
		}
	}

	carry := analyticsViewCarry(q)
	for _, name := range []string{"from", "to", "compare"} {
		if carry.Has(name) {
			p.Carry = append(p.Carry, hiddenField{Name: name, Value: carry.Get(name)})
		}
	}
	p.Filter = ui.Segmented{Mode: "radio", Name: "filter", Label: "Filter pages"}
	for _, f := range googleFilters {
		p.Filter.Items = append(p.Filter.Items, ui.SegmentedItem{Label: f.Label, Value: f.Value, Pressed: f.Value == filter})
	}

	pq := analyticsViewCarry(q)
	if filter != agents.GoogleFilterAll {
		pq.Set("filter", filter)
	}
	p.Rows = ui.AiaRows{Caption: "Pages through Google's eyes", Columns: []string{"Page", "Crawls", "Last crawl", "Status"}, Right: []bool{false, true, false, false},
		Empty: "No pages match this filter.",
		Page:  ui.Pagination{Page: npage, PerPage: analyticsPerPage, Total: total, BaseURL: a.adminPath + "/analytics/google", Param: "gpage", Query: pq}}
	if gsc != nil {
		p.Rows.Columns = append(p.Rows.Columns, "Index", "Impr.", "Clicks", "Position", "Top query")
		p.Rows.Right = append(p.Rows.Right, false, true, true, true, false)
	}
	for _, r := range rows {
		p.Rows.Rows = append(p.Rows.Rows, a.googleRow(r, gsc))
	}
	return p, nil
}

// googleRow is one row of the page table; with gsc, the search performance cells follow.
func (a *Admin) googleRow(r agents.GooglePageRow, gsc map[string]searchperf.PageStats) []ui.AiaCell {
	page := ui.AiaCell{Text: r.Path}
	if r.EntityID != nil {
		page = ui.AiaCell{Text: r.Name, Href: a.adminPath + "/entities/" + r.EntityID.String(), Mono: r.Path}
	}
	last := ui.AiaCell{Text: "Never", Muted: true}
	status := ui.AiaCell{Text: "n/a", Muted: true}
	if r.LastDay != nil {
		last = ui.AiaCell{Text: shortDay(r.LastDay.Format(dayLayout))}
		code := strconv.Itoa(r.LastStatus)
		switch {
		case r.LastStatus >= 400:
			status = ui.AiaCell{Badge: &ui.Badge{Label: code, Kind: "danger"}}
		case r.Stale:
			status = ui.AiaCell{Badge: &ui.Badge{Label: code + ", stale", Kind: "warning"}}
		default:
			status = ui.AiaCell{Text: code}
		}
	}
	row := []ui.AiaCell{page, {Text: fmtNum(r.Crawls)}, last, status}
	if gsc == nil {
		return row
	}
	st, ok := gsc[r.Path]
	if !ok {
		none := ui.AiaCell{Text: "n/a", Muted: true}
		return append(row, none, none, none, none, none)
	}
	return append(row, ui.AiaCell{Text: st.Index}, ui.AiaCell{Text: fmtNum(st.Impressions)}, ui.AiaCell{Text: fmtNum(st.Clicks)},
		ui.AiaCell{Text: strconv.FormatFloat(st.Position, 'f', 1, 64)}, ui.AiaCell{Text: st.TopQuery})
}
