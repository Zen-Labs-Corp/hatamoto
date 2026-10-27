package admin

import (
	"context"
	"errors"
	"net/url"
	"sort"
	"strconv"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/agents"
)

// overviewPage is the body of the Overview section, in the order the page shows it.
type overviewPage struct {
	Lede      string
	KPIs      []ui.Stat
	Hero      ui.AiaChart
	Attention []ui.AiaAttention
	// TopAgents and TopAssistants are quiet bar lists; the Href fields are the
	// cards' "See all" links.
	TopAgents, TopAssistants  ui.BarList
	AgentsHref, ReferralsHref string
}

func (p *overviewPage) lede() string { return p.Lede }

// topRows is how many rows the Overview's top lists show.
const topRows = 6

// attnLevel is the severity of a count: 0 when nothing, 2 when bad, else 1.
func attnLevel(n int64, bad bool) int {
	switch {
	case n == 0:
		return 0
	case bad:
		return 2
	}
	return 1
}

// overviewAttentionFor is the four "Needs attention" cells ranked by severity
// (act now, review, nothing to do), each with its count, rate and a link to
// where it is dealt with.
func overviewAttentionFor(prefix string, t totalsJSON, ignoring []agentJSON, never, published int, q url.Values) []ui.AiaAttention {
	var ignF int64
	for _, ag := range ignoring {
		ignF += ag.Hits
	}
	errs := t.Status4xx + t.Status5xx
	ignRate := "Counts only agents on a blocked purpose."
	if len(ignoring) > 0 {
		ignRate = fixed(ratio(ignF, t.AIFetches), 1) + " of fetches. Blocking them needs a firewall or server rule."
	}
	items := []ui.AiaAttention{
		{Sev: attnLevel(t.Spoofed, ratio(t.Spoofed, t.AIFetches) >= 1), Count: fmtNum(t.Spoofed), Text: "spoofed fetches claiming to be an AI agent",
			Rate: fixed(ratio(t.Spoofed, t.AIFetches), 1) + " of fetches", Action: "Review spoofed", Href: sectionHref(prefix+"/analytics/health#w-h-ver", q)},
		{Sev: attnLevel(int64(len(ignoring)), true), Count: strconv.Itoa(len(ignoring)), Text: "agents ignoring robots.txt fetched pages while blocked",
			Rate: ignRate, Action: "See these agents", Href: sectionHref(prefix+"/analytics/crawlers#w-cr-list", q)},
		{Sev: attnLevel(errs, ratio(errs, t.AIFetches) >= 5 || ratio(t.Status5xx, t.AIFetches) >= 0.5), Count: fmtNum(errs), Text: "4xx and 5xx responses served to AI crawlers",
			Rate: fixed(ratio(errs, t.AIFetches), 1) + " of fetches", Action: "See failing paths", Href: sectionHref(prefix+"/analytics/health#w-h-fail", q)},
		{Sev: attnLevel(int64(never), false), Count: strconv.Itoa(never), Text: "published pages never fetched",
			Rate: fixed(ratio(int64(never), int64(published)), 0) + " of published pages", Action: "View the pages", Href: sectionHref(prefix+"/analytics/content#w-ct-never", q)},
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].Sev > items[j].Sev })
	return items
}

// ignoringRobots are the agents that ignore robots.txt, sit on a blocked
// purpose and fetched something in the range.
func (a *Admin) ignoringRobots(ctx context.Context, list []agentJSON) ([]agentJSON, error) {
	form, err := a.robotsData(ctx, nil, nil)
	if err != nil {
		return nil, err
	}
	blocked := map[string]bool{}
	for _, r := range form.Rows {
		if r.Deny {
			blocked[r.Label] = true
		}
	}
	var out []agentJSON
	for _, ag := range list {
		if ag.IgnoresRobots && ag.Hits > 0 && blocked[ag.Purpose] {
			out = append(out, ag)
		}
	}
	return out, nil
}

func (a *Admin) buildOverview(ctx context.Context, q url.Values, rg agents.Range, cmp bool) (any, error) {
	ov, err := a.overviewData(ctx, rg, "operator")
	if err != nil {
		return nil, err
	}
	t := ov.Totals
	var prev totalsJSON
	if cmp {
		stats, _ := a.agentStats()
		if p := a.previousTotals(ctx, stats, rg); p != nil {
			prev = totalsJSON{AIFetches: p.AIFetches, AIVisits: p.AIVisits, Pageviews: p.Pageviews}
		} else {
			cmp = false
		}
	}
	// Assistants need the referral reader; without it the page still shows the rest.
	refs, err := a.referralsData(ctx, rg)
	if err != nil && !errors.Is(err, errNoService) {
		return nil, err
	}
	list, err := a.agentsData(ctx, rg)
	if err != nil {
		return nil, err
	}
	ignoring, err := a.ignoringRobots(ctx, list.Agents)
	if err != nil {
		return nil, err
	}
	never, published, err := a.publishedCoverage(ctx)
	if err != nil {
		return nil, err
	}

	src := func(endpoint string) string { return a.dataURL(endpoint, rg, false) }
	fetched := int64(published - never)
	cov := ui.Stat{Label: "Published pages fetched at least once", Value: "n/a", Note: "No published pages yet."}
	if published > 0 {
		cov.Value = fixed(ratio(fetched, int64(published)), 0)
		cov.Note = strconv.FormatInt(fetched, 10) + " of " + strconv.Itoa(published) + " published pages, since tracking began"
	}
	shareNow, sharePrev := ratio(t.AIVisits, t.Pageviews), ratio(prev.AIVisits, prev.Pageviews)
	p := &overviewPage{
		Lede: "AI agents made " + fmtNum(t.AIFetches) + " fetches of your pages " + rangeWords(rg) + ", and AI assistants sent " + fmtNum(t.AIVisits) + " visits back.",
		KPIs: []ui.Stat{
			{Label: "AI fetches", Value: fmtNum(t.AIFetches), Change: kpiDelta(cmp, rg, float64(t.AIFetches), float64(prev.AIFetches), false, false),
				Spark: liveSpark("aia-spark-fetches", 1, ui.SparkLive{Src: src("overview.json"), Chart: "fetches"})},
			{Label: "AI-referred visits", Value: fmtNum(t.AIVisits), Change: kpiDelta(cmp, rg, float64(t.AIVisits), float64(prev.AIVisits), false, false),
				Spark: liveSpark("aia-spark-visits", 2, ui.SparkLive{Src: src("referrals.json"), Chart: "referrals"})},
			{Label: "AI share of traffic", Value: fixed(shareNow, 1), Change: kpiDelta(cmp, rg, shareNow, sharePrev, true, false),
				Spark: liveSpark("aia-spark-share", 3, ui.SparkLive{Src: src("referrals.json"), Chart: "referrals", Ratio: "referrals,pageviews", MinMax: true})},
			cov,
		},
		Attention:     overviewAttentionFor(a.adminPath, t, ignoring, never, published, q),
		AgentsHref:    sectionHref(a.adminPath+"/analytics/crawlers", q),
		ReferralsHref: sectionHref(a.adminPath+"/analytics/referrals", q),
	}

	// The hero: AI fetches over AI-referred visits, one axis each; the table under it carries both.
	days := rangeDays(rg)
	tbl := matrix{Days: days, Cols: []string{"AI fetches", "AI-referred visits"}, Vals: map[string][]int64{"AI fetches": dailySums(ov.fetchM), "AI-referred visits": make([]int64, len(days))}}
	if refs != nil {
		tbl.Vals["AI-referred visits"] = dailySums(refs.refM)
	}
	unit := per(rg)
	p.Hero = ui.AiaChart{ID: "w-o-hero", Title: "Fetches and visits over time",
		Description: "AI agents reading your pages, and people arriving from AI assistants. Each has its own axis.",
		Src:         a.dataURL("overview.json", rg, cmp), Chart: "fetches", Panels: "fetches|referrals",
		Srcs:        a.dataURL("overview.json", rg, cmp) + "|" + a.dataURL("referrals.json", rg, cmp),
		PanelLabels: "AI fetches per " + unit + "|AI-referred visits per " + unit, Sum: "AI fetches|AI-referred visits",
		Override: `{"AI-referred visits":{"n":2,"marker":"square","dash":""}}`, Hero: true, Compare: cmp,
		Table: withoutTotal(tbl.table("AI fetches and AI-referred visits per day", "Day"))}

	p.TopAgents = ui.BarList{Label: "Most active AI agents", Quiet: true, Empty: "No data for this period."}
	if t.AIFetches > 0 {
		for i, ag := range ov.Top {
			if i == topRows {
				break
			}
			p.TopAgents.Items = append(p.TopAgents.Items, ui.BarItem{Label: ag.Name, Href: sectionHref(a.adminPath+"/analytics/agents/"+url.PathEscape(ag.ID), q),
				Value: fmtNum(ag.Hits), Share: shareText(ratio(ag.Hits, t.AIFetches)), N: float64(ag.Hits)})
		}
	}
	p.TopAssistants = ui.BarList{Label: "AI-referred visits by assistant", Quiet: true, Empty: "No data for this period."}
	if refs != nil && refs.Visits > 0 {
		for i, as := range refs.Assistants {
			if i == topRows {
				break
			}
			p.TopAssistants.Items = append(p.TopAssistants.Items, ui.BarItem{Label: assistantName(as.Assistant), Value: fmtNum(as.Visits),
				Share: shareText(ratio(as.Visits, refs.Visits)), N: float64(as.Visits)})
		}
	}
	return p, nil
}
