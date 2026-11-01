package admin

import (
	"context"
	"net/url"
	"sort"
	"strconv"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/agents"
)

// healthPage is the body of the Health section.
type healthPage struct {
	Hero ui.AiaChart
	// Verification is nil when no fetch was checked; Spoofed lists the claimed
	// agents behind the spoofed fetches.
	Verification *ui.CategoryBar
	Spoofed      ui.BarList
	// Responses is nil when nothing was fetched. Tracker is one block per day,
	// with its caption and its numbers as a table.
	Responses      *ui.CategoryBar
	Tracker        ui.Tracker
	TrackerCaption string
	TrackerTable   ui.DataTable
	Failing        ui.AiaRows
	Compliance     healthCompliance
}

// healthCompliance is the robots.txt compliance table: every AI agent, those
// ignoring robots.txt first, paged like the failing paths above it.
type healthCompliance struct {
	Rows       ui.AiaRows
	PolicyHref string
}

// trackerKind is the colour of one day of the errors tracker: a neutral step
// up to 5% of fetches failing, amber above that, red above 10%, an outline when
// the day had no fetches. Only the bad days carry a hue.
func trackerKind(failed, total int64) string {
	switch f := ratio(failed, total); {
	case total == 0:
		return "neutral"
	case f > 10:
		return "danger"
	case f > 5:
		return "warning"
	}
	return healthyDay
}

// healthyDay is the tracker kind of a day without errors: a neutral step.
const healthyDay = "series-4"

// failingText is the tooltip words of one tracker day.
func failingText(failed, total int64) string {
	if total == 0 {
		return "No fetches"
	}
	return fmtNum(failed) + " of " + fmtNum(total) + " fetches failed (" + fixed(ratio(failed, total), 1) + ")"
}

// responsesBar is the status classes as one bar; nil when nothing was fetched.
func responsesBar(s2, s3, s4, s5 int64) *ui.CategoryBar {
	all := s2 + s3 + s4 + s5
	if all == 0 {
		return nil
	}
	seg := func(label, kind string, n int64) ui.CategorySegment {
		return ui.CategorySegment{Label: label, Kind: kind, N: float64(n), Value: fmtNum(n), Share: shareText(ratio(n, all))}
	}
	return &ui.CategoryBar{Label: "Responses by status class", Columns: true, Segments: []ui.CategorySegment{
		seg("2xx success", "series-2", s2), seg("3xx redirect", "series-3", s3), seg("4xx client error", "warning", s4), seg("5xx server error", "danger", s5)}}
}

// verificationBar is the verification result of fetches as one bar; nil when none.
func verificationBar(verified, unverifiable, spoofed int64) *ui.CategoryBar {
	all := verified + unverifiable + spoofed
	if all == 0 {
		return nil
	}
	seg := func(label, kind string, n int64) ui.CategorySegment {
		return ui.CategorySegment{Label: label, Kind: kind, N: float64(n), Value: fmtNum(n), Share: shareText(ratio(n, all))}
	}
	return &ui.CategoryBar{Label: "Fetches by verification result", Columns: true, Segments: []ui.CategorySegment{
		seg("Verified", "series-2", verified), seg("Unverifiable", "series-4", unverifiable), seg("Spoofed", "danger", spoofed)}}
}

func (a *Admin) buildHealth(ctx context.Context, q url.Values, rg agents.Range, cmp bool) (any, error) {
	stats, ok := a.agentStats()
	if !ok {
		return nil, errNoService
	}
	ov, err := a.overviewData(ctx, rg, "operator")
	if err != nil {
		return nil, err
	}
	health := ov.health
	list, err := a.agentsData(ctx, rg)
	if err != nil {
		return nil, err
	}
	form, err := a.robotsData(ctx, nil, nil)
	if err != nil {
		return nil, err
	}
	fpage := atoiMin(q.Get("fpage"), 1)
	failing, failTotal, err := stats.Errors(ctx, rg, agents.Page{Limit: analyticsPerPage, Offset: (fpage - 1) * analyticsPerPage})
	if err != nil {
		return nil, err
	}

	days := rangeDays(rg)
	byDay := map[string]agents.HealthDay{}
	var s2, s3, s4, s5 int64
	for _, h := range health {
		byDay[h.Day] = h
		s2, s3, s4, s5 = s2+h.S2xx, s3+h.S3xx, s4+h.S4xx, s5+h.S5xx
	}
	t := ov.Totals
	src := a.dataURL("overview.json", rg, cmp)
	vm := matrix{Days: days, Cols: []string{"Verified", "Spoofed"}, Vals: map[string][]int64{"Verified": make([]int64, len(days)), "Spoofed": make([]int64, len(days))}}
	for i, d := range days {
		vm.Vals["Verified"][i], vm.Vals["Spoofed"][i] = byDay[d].Verified, byDay[d].Spoofed
	}
	unit := per(rg)

	p := &healthPage{
		Hero: ui.AiaChart{ID: "w-h-hero", Title: "Verified and spoofed fetches", Description: "Each fetch is checked against the operator\u2019s published method.",
			Src: src, Chart: "verified", Panels: "verified|spoofed", PanelLabels: "Verified fetches per " + unit + "|Spoofed fetches per " + unit,
			Override: `{"Verified":{"n":2},"Spoofed":{"cls":"hm-viz--danger","marker":"triangle","dash":""}}`, Hero: true, Compare: cmp,
			Table: withoutTotal(vm.table("Verified and spoofed fetches per day", "Day"))},
		Verification: verificationBar(t.Verified, t.Unverifiable, t.Spoofed),
		Spoofed:      ui.BarList{Label: "Spoofed fetches by claimed agent", Quiet: true, Empty: "No spoofed fetches in this period."},
		Responses:    responsesBar(s2, s3, s4, s5),
	}
	spoofed := []agentJSON{}
	for _, ag := range list.Agents {
		if ag.Spoofed > 0 {
			spoofed = append(spoofed, ag)
		}
	}
	sort.SliceStable(spoofed, func(i, j int) bool { return spoofed[i].Spoofed > spoofed[j].Spoofed })
	for _, ag := range spoofed {
		p.Spoofed.Items = append(p.Spoofed.Items, ui.BarItem{Label: ag.Name, Value: fmtNum(ag.Spoofed), N: float64(ag.Spoofed)})
	}

	// Errors by day: one block each, coloured by the share of fetches that failed.
	em := matrix{Days: days, Cols: []string{"Failed fetches", "All fetches"}, Vals: map[string][]int64{"Failed fetches": make([]int64, len(days)), "All fetches": make([]int64, len(days))}}
	seen := map[string]bool{}
	p.Tracker = ui.Tracker{Label: "Days with crawler errors", States: true}
	if len(days) > 0 {
		p.Tracker.Start, p.Tracker.End = shortDay(days[0]), shortDay(days[len(days)-1])
	}
	names := map[string]string{healthyDay: "Healthy", "warning": "Some errors", "danger": "Many errors", "neutral": "No fetches"}
	for i, d := range days {
		h := byDay[d]
		failed := h.S4xx + h.S5xx
		em.Vals["Failed fetches"][i], em.Vals["All fetches"][i] = failed, h.Hits
		kind := trackerKind(failed, h.Hits)
		p.Tracker.Blocks = append(p.Tracker.Blocks, ui.TrackerBlock{Kind: kind, Tip: shortDay(d) + ": " + failingText(failed, h.Hits)})
		seen[kind] = true
	}
	for _, k := range []string{healthyDay, "warning", "danger", "neutral"} {
		if seen[k] {
			p.Tracker.Legend = append(p.Tracker.Legend, ui.TrackerKey{Kind: k, Label: names[k]})
		}
	}
	p.TrackerCaption = "Errors by day"
	p.TrackerTable = withoutTotal(em.table("Failed and all fetches per day", "Day"))

	// Failing paths: a redirect only helps where the page is gone.
	p.Failing = ui.AiaRows{Caption: "Pages that answered AI crawlers with an error", Columns: []string{"Page", "Status", "Fetches", "Agents", "Last seen", "Action"},
		Right: []bool{false, false, true, true, false, false}, HiddenHead: []bool{false, false, false, false, false, true}, Empty: "No data for this period.",
		Page: ui.Pagination{Page: fpage, PerPage: analyticsPerPage, Total: failTotal, BaseURL: a.adminPath + "/analytics/health", Param: "fpage", Query: analyticsViewCarry(q)}}
	for _, e := range failing {
		kind := "warning"
		if e.Status >= 500 {
			kind = "danger"
		}
		act := ui.AiaCell{}
		if href := a.createRedirectHref(e.Path, e.Status); href != "" {
			act = ui.AiaCell{Text: "Create redirect", Href: href, Button: true}
		}
		p.Failing.Rows = append(p.Failing.Rows, []ui.AiaCell{{Mono: e.Path}, {Badge: &ui.Badge{Label: strconv.Itoa(e.Status), Kind: kind}},
			{Text: fmtNum(e.Hits)}, {Text: strconv.Itoa(len(e.AgentIDs))}, {Text: shortDay(e.LastDay)}, act})
	}

	// Robots compliance: every AI agent that can fetch, ignorers first, then by fetches.
	denied := map[string]bool{}
	for _, r := range form.Rows {
		denied[r.Purpose] = r.Deny
	}
	byPurpose := a.agentsByPurpose(list.Agents)
	var rows []agentJSON
	for _, pu := range aiPurposes {
		rows = append(rows, byPurpose[string(pu)]...)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].IgnoresRobots != rows[j].IgnoresRobots {
			return rows[i].IgnoresRobots
		}
		return rows[i].Hits > rows[j].Hits
	})
	cpage := atoiMin(q.Get("cpage"), 1)
	if last := (len(rows) + analyticsPerPage - 1) / analyticsPerPage; cpage > last && last > 0 {
		cpage = last
	}
	p.Compliance.PolicyHref = sectionHref(a.adminPath+"/analytics/crawlers#w-cr-list", q)
	p.Compliance.Rows = ui.AiaRows{Caption: "Robots.txt compliance of AI agents", Columns: []string{"Agent", "Purpose", "Honours robots.txt", "Policy", "Fetches"},
		Right: []bool{false, false, false, false, true}, Empty: "No data for this period.",
		Page: ui.Pagination{Page: cpage, PerPage: analyticsPerPage, Total: len(rows), BaseURL: a.adminPath + "/analytics/health", Param: "cpage", Query: carryPage(analyticsViewCarry(q), "fpage", q)}}
	p.Failing.Page.Query = carryPage(p.Failing.Page.Query, "cpage", q)
	for _, ag := range rows[min((cpage-1)*analyticsPerPage, len(rows)):min(cpage*analyticsPerPage, len(rows))] {
		honours := ui.Badge{Label: "Yes", Kind: "success"}
		if ag.IgnoresRobots {
			honours = ui.Badge{Label: "No", Kind: "warning"}
		}
		policy := ui.Badge{Label: "Allowed", Kind: "neutral"}
		if denied[purposeID(ag.Purpose)] {
			policy = ui.Badge{Label: "Blocked", Kind: "danger"}
		}
		p.Compliance.Rows.Rows = append(p.Compliance.Rows.Rows, []ui.AiaCell{{Text: ag.Name, Href: sectionHref(a.adminPath+"/analytics/agents/"+url.PathEscape(ag.ID), q)},
			{Text: ag.Purpose}, {Badge: &honours}, {Badge: &policy}, {Text: fmtNum(ag.Hits)}})
	}
	return p, nil
}

// carryPage keeps the other table's page number in a pager's links when it is past the first.
func carryPage(carry url.Values, param string, q url.Values) url.Values {
	if atoiMin(q.Get(param), 1) > 1 {
		carry.Set(param, q.Get(param))
	}
	return carry
}
