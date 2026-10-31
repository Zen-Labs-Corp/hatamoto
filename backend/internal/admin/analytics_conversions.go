package admin

import (
	"context"
	"encoding/csv"
	"math"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"time"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/agents"
	"hatamoto.dev/core/internal/analytics"
	"hatamoto.dev/core/internal/user"
)

const (
	conversionsLede = "Each form submission keeps the landing page, channel, source and campaign of the visit it came from. Stored on the submission, no cookie banner needed."
	// conversionsRecent is how many leads the Recent leads card lists; the CSV has more.
	conversionsRecent = 10
	// conversionsExportRows caps the CSV export.
	conversionsExportRows = 10000
	// unknownName is the display name of leads stored without attribution.
	unknownName = "Unknown"
)

// canViewSubmissions is whether the signed-in user may see who submitted a form: the Recent leads
// emails and the CSV export need it. Owner and editor may; no user or an unknown role may not.
func (a *Admin) canViewSubmissions(ctx context.Context) bool {
	u := userFrom(ctx)
	return u != nil && (u.Role == string(user.RoleOwner) || u.Role == string(user.RoleEditor))
}

// touchSwitch is the First touch | Last touch control of the Traffic and Conversions tabs: a GET
// form that carries the range, so the page is the same with and without script. The Apply button
// stays because the analytics pages do not load the script that auto-submits a form.
type touchSwitch struct {
	Action string
	Hidden map[string]string
	Seg    ui.Segmented
}

func (a *Admin) touchSwitchFor(path string, rg agents.Range, cmp bool, touch analytics.TouchKind) touchSwitch {
	hidden := map[string]string{"from": rg.From.Format(dayLayout), "to": rg.To.Format(dayLayout)}
	if cmp {
		hidden["compare"] = "1"
	}
	return touchSwitch{Action: a.adminPath + path, Hidden: hidden, Seg: ui.Segmented{Mode: "radio", Label: "Attribution", Name: "touch", Items: []ui.SegmentedItem{
		{Label: "First touch", Value: string(analytics.TouchFirst), Pressed: touch == analytics.TouchFirst},
		{Label: "Last touch", Value: string(analytics.TouchLast), Pressed: touch == analytics.TouchLast}}}}
}

// touchQuery adds ?touch=last to a URL that already has a query; the first touch is the default.
func touchQuery(u string, touch analytics.TouchKind) string {
	if touch == analytics.TouchLast {
		return u + "&touch=last"
	}
	return u
}

// leadChannelName is channelName plus the bucket of leads stored without attribution.
func leadChannelName(key string) string {
	if key == analytics.ChannelUnknown {
		return unknownName
	}
	return channelName(key)
}

func leadChannelNames() []string { return append(channelNames(), unknownName) }

func leadPoints(days []analytics.LeadDay) []agents.SeriesPoint {
	out := make([]agents.SeriesPoint, len(days))
	for i, d := range days {
		out[i] = agents.SeriesPoint{Day: d.Day, Key: d.Channel, Hits: d.Leads}
	}
	return out
}

// conversionsView is what conversions.json serves: the stacked leads-by-channel chart.
type conversionsView struct {
	From   string               `json:"from"`
	To     string               `json:"to"`
	Touch  string               `json:"touch"`
	Charts map[string]chartSpec `json:"charts"`

	leadsM matrix
}

// conversionsData builds the leads chart and, when cmp, the previous period as its previous line.
func (a *Admin) conversionsData(ctx context.Context, rg agents.Range, cmp bool, touch analytics.TouchKind) (*conversionsView, error) {
	cv := a.deps.Growth.Conversions
	if cv == nil {
		return nil, errNoService
	}
	pts, err := cv.LeadsDaily(ctx, touch, rg.From, rg.To)
	if err != nil {
		return nil, err
	}
	v := &conversionsView{From: rg.From.Format(dayLayout), To: rg.To.Format(dayLayout), Touch: string(touch), Charts: map[string]chartSpec{}}
	v.leadsM = pivotFixed(rangeDays(rg), leadPoints(pts), leadChannelName, leadChannelNames())
	chart := v.leadsM.chart("bar", true)
	if cmp {
		prg := previousRange(rg)
		ppts, err := cv.LeadsDaily(ctx, touch, prg.From, prg.To)
		if err != nil {
			a.logger().Error("conversions previous period failed", "err", err)
		} else {
			chart = chart.withPrevious(prg, pivotFixed(rangeDays(prg), leadPoints(ppts), leadChannelName, chart.seriesLabels()).chart("bar", true))
		}
	}
	v.Charts["leads"] = chart
	return v, nil
}

func (a *Admin) conversionsJSON(ctx context.Context, q url.Values, rg agents.Range) (any, int, string) {
	v, err := a.conversionsData(ctx, rg, parseCompare(q), analytics.ParseTouch(q.Get("touch")))
	return a.result(v, err, "conversions")
}

// conversionsPage is the body of the Conversions section.
type conversionsPage struct {
	KPIs  []ui.Stat
	Touch touchSwitch
	// Chart is the leads-by-channel card, drawn from conversions.json; UnknownNote says how many
	// leads sit in its Unknown series.
	Chart       ui.AiaChart
	UnknownNote string
	Funnel      ui.BarList
	GiveGet     ui.AiaRows
	// CrawlersHref is the Crawlers tab, where each AI purpose is allowed or blocked.
	CrawlersHref string
	Recent       ui.AiaRows
	// ExportHref is the CSV of the range; empty when the user may not see submitters.
	ExportHref string
	// RecentNote is the muted line under Recent leads.
	RecentNote string
}

// operatorRow is one operator of the give-vs-get table.
type operatorRow struct {
	name                   string
	pages, visitors, leads int64
}

func (a *Admin) buildConversions(ctx context.Context, q url.Values, rg agents.Range, cmp bool) (any, error) {
	cv, tr := a.deps.Growth.Conversions, a.deps.Growth.Traffic
	if cv == nil || tr == nil {
		return nil, errNoService
	}
	touch := analytics.ParseTouch(q.Get("touch"))
	asked := cmp
	chart, err := a.conversionsData(ctx, rg, cmp, touch)
	if err != nil {
		return nil, err
	}
	totals, err := tr.TrafficTotals(ctx, rg.From, rg.To)
	if err != nil {
		return nil, err
	}
	leads, unknown, err := cv.LeadsTotals(ctx, touch, rg.From, rg.To)
	if err != nil {
		return nil, err
	}
	byChannel, err := cv.LeadsTop(ctx, analytics.DimChannel, touch, rg.From, rg.To, 0)
	if err != nil {
		return nil, err
	}
	bySource, err := cv.LeadsTop(ctx, analytics.DimSource, touch, rg.From, rg.To, 0)
	if err != nil {
		return nil, err
	}
	byForm, err := cv.LeadsByForm(ctx, rg.From, rg.To, 1)
	if err != nil {
		return nil, err
	}
	channels, err := tr.TrafficTop(ctx, analytics.DimChannel, rg.From, rg.To, 0)
	if err != nil {
		return nil, err
	}
	sources, err := tr.TrafficTop(ctx, analytics.DimSource, rg.From, rg.To, 0)
	if err != nil {
		return nil, err
	}
	list, err := a.agentsData(ctx, rg)
	if err != nil {
		return nil, err
	}
	var prevLeads, prevVisits int64
	if cmp {
		prg := previousRange(rg)
		pl, _, err1 := cv.LeadsTotals(ctx, touch, prg.From, prg.To)
		pt, err2 := tr.TrafficTotals(ctx, prg.From, prg.To)
		if err1 != nil || err2 != nil {
			a.logger().Error("conversions previous period failed", "err", firstErr(err1, err2))
			cmp = false
		} else {
			prevLeads, prevVisits = pl, pt.Visits
		}
	}

	leadsOf := map[string]int64{}
	for _, c := range byChannel {
		leadsOf[c.K1] = c.Leads
	}
	visitsOf := map[string]int64{}
	for _, c := range channels {
		visitsOf[c.K1] = c.Visits
	}
	best, bestRate := "", 0.0
	for _, c := range trafficChannels {
		if r := ratio(leadsOf[c.Key], visitsOf[c.Key]); leadsOf[c.Key] > 0 && r > bestRate {
			best, bestRate = c.Name, r
		}
	}

	p := &conversionsPage{Touch: a.touchSwitchFor("/analytics/conversions", rg, asked, touch)}
	rate := placeholder
	if totals.Visits > 0 {
		rate = fixed(ratio(leads, totals.Visits), 1)
	}
	aiLeads := leadsOf[analytics.ChannelAI]
	bestStat := ui.Stat{Label: "Best channel", Value: placeholder, Note: "No leads in this period."}
	if best != "" {
		bestStat = ui.Stat{Label: "Best channel", Badge: &ui.Badge{Label: best, Kind: "neutral"}, Note: fixed(bestRate, 1) + " conversion"}
	}
	topForm := ui.Stat{Label: "Top form", Value: placeholder, Note: "No leads in this period."}
	if len(byForm) > 0 {
		topForm = ui.Stat{Label: "Top form", Value: byForm[0].Name, Note: fmtNum(byForm[0].Leads) + " leads"}
	}
	aiNote := "No leads in this period."
	if leads > 0 {
		aiNote = share(aiLeads, leads) + " of leads"
	}
	p.KPIs = []ui.Stat{
		{Label: "Leads", Value: fmtNum(leads), Note: "Every submission that is not spam.",
			Change: kpiDelta(cmp, rg, float64(leads), float64(prevLeads), false, false)},
		{Label: "Conv. rate", Value: rate, Note: "Leads per visit. Can pass 100% on a short range.",
			Change: kpiDelta(cmp, rg, ratio(leads, totals.Visits), ratio(prevLeads, prevVisits), true, false)},
		{Label: "Leads from AI assistants", Value: fmtNum(aiLeads), Note: aiNote},
		bestStat, topForm,
	}

	p.Chart = ui.AiaChart{ID: "w-c-leads", Title: "Leads by " + string(touch) + " channel", Description: "Per day, stacked.",
		Src: touchQuery(a.dataURL("conversions.json", rg, cmp), touch), Chart: "leads", Compare: cmp,
		Table: chart.leadsM.table("Leads by channel per day", "Day")}
	if unknown > 0 {
		p.UnknownNote = fmtNum(unknown) + " of these leads have no attribution: they were stored before it was recorded, or posted without it."
	}

	p.Funnel = funnelList(totals, leads)

	// Give vs get: every operator that fetched pages, or whose assistant sent visitors or leads.
	ops := map[string]*operatorRow{}
	row := func(name string) *operatorRow {
		if ops[name] == nil {
			ops[name] = &operatorRow{name: name}
		}
		return ops[name]
	}
	for _, ag := range list.Agents {
		if slices.Contains(aiPurposeIDs, purposeID(ag.Purpose)) {
			row(ag.Operator).pages += ag.Hits
		}
	}
	const other = "Other assistants"
	assistant := func(source, channel string) *operatorRow {
		if channel != analytics.ChannelAI {
			return nil
		}
		if op, ok := agents.AssistantOperator(source); ok {
			return row(op)
		}
		return row(other)
	}
	for _, s := range sources {
		if r := assistant(s.K1, s.K2); r != nil {
			r.visitors += s.Visits
		}
	}
	for _, s := range bySource {
		if r := assistant(s.K1, s.K2); r != nil {
			r.leads += s.Leads
		}
	}
	rows := make([]*operatorRow, 0, len(ops))
	for _, r := range ops {
		if r.pages > 0 || r.visitors > 0 || r.leads > 0 {
			rows = append(rows, r)
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if (rows[i].name == other) != (rows[j].name == other) {
			return rows[j].name == other
		}
		if rows[i].pages != rows[j].pages {
			return rows[i].pages > rows[j].pages
		}
		return rows[i].name < rows[j].name
	})
	p.GiveGet = ui.AiaRows{Caption: "Pages fetched compared with visitors and leads sent back per AI company",
		Columns: []string{"Operator", "Pages fetched", "Visitors sent", "Fetches per visit", "Leads", "Lead rate"},
		Right:   []bool{false, true, true, true, true, true}, Empty: "No AI fetches or assistant visits in this period."}
	takesOnly := false
	for _, r := range rows {
		name := ui.AiaCell{Text: r.name}
		perVisit, leadRate := ui.AiaCell{Text: placeholder, Muted: true}, ui.AiaCell{Text: placeholder, Muted: true}
		if r.pages > 0 && r.visitors == 0 && agents.OperatorFeedsSearch(r.name) {
			// It also runs a search crawler, so zero assistant visitors proves nothing.
			name.Note = "Only AI assistant visits count here; search visits count under other channels"
		} else if r.pages > 0 && r.visitors == 0 {
			name.Badge, takesOnly = &ui.Badge{Label: "takes only", Kind: "warning"}, true
		}
		if r.visitors > 0 {
			perVisit = ui.AiaCell{Text: strconv.FormatFloat(float64(r.pages)/float64(r.visitors), 'f', 1, 64)}
			leadRate = ui.AiaCell{Text: fixed(ratio(r.leads, r.visitors), 1)}
		}
		p.GiveGet.Rows = append(p.GiveGet.Rows, []ui.AiaCell{name, {Text: fmtNum(r.pages)}, {Text: fmtNum(r.visitors)}, perVisit, {Text: fmtNum(r.leads)}, leadRate})
	}
	if takesOnly {
		p.CrawlersHref = sectionHref(a.adminPath+"/analytics/crawlers", q)
	}

	// Recent leads: the submitter's address only for users who may see submissions.
	canSee := a.canViewSubmissions(ctx)
	p.Recent = ui.AiaRows{Caption: "Recent leads", Columns: []string{"When", "Lead", "Form", "Channel", "Source / campaign", "Landing page"}, Empty: "No leads in this period."}
	recent, err := cv.RecentLeads(ctx, rg.From, rg.To, conversionsRecent)
	if err != nil {
		return nil, err
	}
	for _, l := range recent {
		t := pickTouch(l, touch)
		lead := ui.AiaCell{Text: "Hidden", Muted: true}
		if canSee {
			lead = ui.AiaCell{Text: l.Email, Href: a.adminPath + "/settings/submissions/" + l.ID.String()}
			if l.Email == "" {
				lead = ui.AiaCell{Text: "No email", Muted: true, Href: lead.Href}
			}
		}
		p.Recent.Rows = append(p.Recent.Rows, []ui.AiaCell{{Text: fmtDateTime(l.At.UTC())}, lead, {Text: l.FormName},
			{Text: touchChannel(t)}, {Text: touchSource(t), Muted: true}, {Text: touchLanding(t)}})
	}
	if canSee {
		p.ExportHref = touchQuery(a.adminPath+"/analytics/conversions/export.csv?from="+rg.From.Format(dayLayout)+"&to="+rg.To.Format(dayLayout), touch)
	} else {
		p.RecentNote = "Submitter addresses are shown to users who can view submissions."
	}
	return p, nil
}

// funnelList is the five steps of a visit to a lead. Bars use a square-root scale so the small
// steps stay visible; each value says how much of the previous step is left.
func funnelList(t analytics.Counts, leads int64) ui.BarList {
	b := ui.BarList{Label: "Funnel from visit to lead", NameHeading: "Step", ValueHeading: "Count", Empty: "No visits in this period."}
	if t.Visits == 0 {
		return b
	}
	steps := []struct {
		Label string
		N     int64
	}{{"Visits", t.Visits}, {"Viewed 2+ pages", t.Multi}, {"Saw a form", t.FormSeen}, {"Started the form", t.FormStarted}, {"Submitted", leads}}
	prev := int64(0)
	for i, s := range steps {
		it := ui.BarItem{Label: s.Label, Value: fmtNum(s.N), N: math.Sqrt(float64(s.N))}
		if i > 0 && prev > 0 {
			it.Share = shareText(ratio(s.N, prev)) + " of previous"
		}
		prev = s.N
		b.Items = append(b.Items, it)
	}
	return b
}

func pickTouch(l analytics.Lead, touch analytics.TouchKind) *analytics.Touch {
	if touch == analytics.TouchLast {
		return l.Last
	}
	return l.First
}

func touchChannel(t *analytics.Touch) string {
	if t == nil || t.Channel == "" {
		return unknownName
	}
	return channelName(t.Channel)
}

// touchSource is "source / campaign", whichever parts the touch has.
func touchSource(t *analytics.Touch) string {
	switch {
	case t == nil:
		return ""
	case t.Source != "" && t.Campaign != "":
		return sourceName(t.Source) + " / " + t.Campaign
	case t.Source != "":
		return sourceName(t.Source)
	}
	return t.Campaign
}

func touchLanding(t *analytics.Touch) string {
	if t == nil {
		return ""
	}
	return t.Landing
}

// handleConversionsExport streams the leads of the range as CSV. It carries submitter addresses,
// so it needs the permission to view submissions.
func (a *Admin) handleConversionsExport(w http.ResponseWriter, r *http.Request) {
	if !a.canViewSubmissions(r.Context()) {
		a.Error(w, r, http.StatusForbidden, "Not allowed", "Your role cannot export submissions.")
		return
	}
	cv := a.deps.Growth.Conversions
	if cv == nil {
		a.unavailable(w, r, "Analytics")
		return
	}
	q := r.URL.Query()
	rg, msg := parseRange(q)
	if msg != "" {
		a.Error(w, r, http.StatusUnprocessableEntity, "Range not accepted", msg)
		return
	}
	touch := analytics.ParseTouch(q.Get("touch"))
	leads, err := cv.RecentLeads(r.Context(), rg.From, rg.To, conversionsExportRows)
	if err != nil {
		a.logger().Error("export leads failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The leads could not be exported.")
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/csv; charset=utf-8")
	h.Set("Content-Disposition", `attachment; filename="leads-`+rg.From.Format("20060102")+"-"+rg.To.Format("20060102")+`.csv"`)
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("\xef\xbb\xbf"))
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"When (UTC)", "Form", "Channel", "Source", "Campaign", "Landing page", "Email"})
	for _, l := range leads {
		t := pickTouch(l, touch)
		var src, camp string
		if t != nil {
			src, camp = t.Source, t.Campaign
		}
		_ = cw.Write([]string{l.At.UTC().Format(time.RFC3339), csvSafe(l.FormName), csvSafe(touchChannel(t)), csvSafe(src), csvSafe(camp), csvSafe(touchLanding(t)), csvSafe(l.Email)})
	}
	cw.Flush()
}
