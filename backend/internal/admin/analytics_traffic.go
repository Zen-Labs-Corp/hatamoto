package admin

import (
	"context"
	"net/url"
	"strconv"
	"strings"

	"golang.org/x/text/language"
	"golang.org/x/text/language/display"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/agents"
	"hatamoto.dev/core/internal/analytics"
)

const (
	trafficLede = "Every visit is grouped by channel, source and campaign. UTM tags win over the referrer. AI assistants are detected by referrer and utm_source."
	// trafficChannelHelp defines a visit and explains why this count and the Referrals tab differ.
	trafficChannelHelp = analytics.VisitDefinition + " AI assistants here counts visits that arrive with a page load. The Referrals tab counts the server-side referral signal, so the two can differ."
	// leadsLater is the note under every control or cell that Phase 4 fills.
	leadsLater = "Leads arrive with lead attribution (coming next)."
	// placeholder stands in for a metric whose data arrives in a later phase.
	placeholder = "--"
	// trafficTopRows is how many rows the source, campaign and landing page lists show.
	trafficTopRows = 10
)

// trafficChannels are the channels in chart and table order, with their display names. The chart
// always has all seven series so the previous period lines up by index.
var trafficChannels = []struct{ Key, Name string }{
	{analytics.ChannelOrganic, "Organic search"}, {analytics.ChannelAI, "AI assistants"}, {analytics.ChannelDirect, "Direct"},
	{analytics.ChannelReferral, "Referral"}, {analytics.ChannelSocial, "Social"}, {analytics.ChannelEmail, "Email"}, {analytics.ChannelPaid, "Paid"},
}

func channelName(key string) string {
	for _, c := range trafficChannels {
		if c.Key == key {
			return c.Name
		}
	}
	return key
}

// sourceChannel is the Channel cell of a source row; the capped (other) row
// mixes channels and has no key.
func sourceChannel(key string) string {
	if key == "" {
		return "Mixed"
	}
	return channelName(key)
}

func channelNames() []string {
	out := make([]string, len(trafficChannels))
	for i, c := range trafficChannels {
		out[i] = c.Name
	}
	return out
}

// trafficView is what traffic.json serves and the page draws its channel figures from: one
// builder for the chart, the table under it and the JSON.
type trafficView struct {
	From   string               `json:"from"`
	To     string               `json:"to"`
	Charts map[string]chartSpec `json:"charts"`

	channelM matrix
}

// trafficPoints are daily rows as the visit series of a pivot.
func trafficPoints(days []analytics.TrafficDay) []agents.SeriesPoint {
	out := make([]agents.SeriesPoint, len(days))
	for i, d := range days {
		out[i] = agents.SeriesPoint{Day: d.Day, Key: d.K1, Hits: d.Visits}
	}
	return out
}

// trafficData builds the stacked visits-by-channel chart and, when cmp, the same chart over the
// previous period as its previous line. A failing previous period only leaves the line out.
func (a *Admin) trafficData(ctx context.Context, rg agents.Range, cmp bool) (*trafficView, error) {
	tr := a.deps.Growth.Traffic
	if tr == nil {
		return nil, errNoService
	}
	pts, err := tr.TrafficDaily(ctx, analytics.DimChannel, rg.From, rg.To)
	if err != nil {
		return nil, err
	}
	v := &trafficView{From: rg.From.Format(dayLayout), To: rg.To.Format(dayLayout), Charts: map[string]chartSpec{}}
	v.channelM = pivotFixed(rangeDays(rg), trafficPoints(pts), channelName, channelNames())
	chart := v.channelM.chart("bar", true)
	if cmp {
		prg := previousRange(rg)
		ppts, err := tr.TrafficDaily(ctx, analytics.DimChannel, prg.From, prg.To)
		if err != nil {
			a.logger().Error("traffic previous period failed", "err", err)
		} else {
			chart = chart.withPrevious(prg, pivotFixed(rangeDays(prg), trafficPoints(ppts), channelName, chart.seriesLabels()).chart("bar", true))
		}
	}
	v.Charts["channels"] = chart
	return v, nil
}

func (a *Admin) trafficJSON(ctx context.Context, q url.Values, rg agents.Range) (any, int, string) {
	v, err := a.trafficData(ctx, rg, parseCompare(q))
	return a.result(v, err, "traffic")
}

// trafficPage is the body of the Traffic section.
type trafficPage struct {
	Lede string
	KPIs []ui.Stat
	// Attribution is the First touch | Last touch switch, disabled until lead attribution lands;
	// LeadsNote says so under it.
	Attribution ui.Segmented
	LeadsNote   string
	// ChannelsHelp is the card description (the visit definition and the Referrals note).
	ChannelsHelp string
	Channels     ui.AiaRows
	Sources      ui.AiaRows
	Campaigns    ui.AiaRows
	Landing      ui.BarList
	Devices      ui.BarList
	Countries    ui.BarList
	// CountriesSetup is the empty-state body when no country database is loaded: the setting to make.
	CountriesSetup string
	Engagement     ui.BarList
	// EngagementShown is whether the engagement setting is wired; EngagementOn its value.
	EngagementShown, EngagementOn bool
	// Geo is the read-only state of the country database (set with HATAMOTO_GEOIP_DB).
	Geo   ui.Badge
	Owner bool
	// Return is where the settings form sends the user back (the range and compare carried).
	Return string
}

func (p *trafficPage) lede() string { return p.Lede }

// countryName is the English name of an ISO 3166-1 alpha-2 code; an unknown code stays as it is.
func countryName(code string) string {
	if code == analytics.CountryUnknown {
		return "Unknown"
	}
	r, err := language.ParseRegion(code)
	if err != nil {
		return code
	}
	if n := display.English.Regions().Name(r); n != "" {
		return n
	}
	return code
}

// sourceName is a source as the list shows it: a visit without one is "(direct)".
func sourceName(key string) string {
	if key == analytics.SourceNone {
		return "(direct)"
	}
	return key
}

func (a *Admin) buildTraffic(ctx context.Context, q url.Values, rg agents.Range, cmp bool) (any, error) {
	tr := a.deps.Growth.Traffic
	if tr == nil {
		return nil, errNoService
	}
	totals, err := tr.TrafficTotals(ctx, rg.From, rg.To)
	if err != nil {
		return nil, err
	}
	channels, err := tr.TrafficTop(ctx, analytics.DimChannel, rg.From, rg.To, 0)
	if err != nil {
		return nil, err
	}
	var prevTotals analytics.Counts
	prevChannels := map[string]analytics.Counts{}
	if cmp {
		prg := previousRange(rg)
		pt, err1 := tr.TrafficTotals(ctx, prg.From, prg.To)
		pc, err2 := tr.TrafficTop(ctx, analytics.DimChannel, prg.From, prg.To, 0)
		if err1 != nil || err2 != nil {
			a.logger().Error("traffic previous period failed", "err", firstErr(err1, err2))
			cmp = false
		} else {
			prevTotals = pt
			for _, c := range pc {
				prevChannels[c.K1] = c.Counts
			}
		}
	}
	top := func(dim analytics.Dim, limit int) ([]analytics.TrafficTop, error) {
		return tr.TrafficTop(ctx, dim, rg.From, rg.To, limit)
	}
	sources, err := top(analytics.DimSource, trafficTopRows)
	if err != nil {
		return nil, err
	}
	campaigns, err := top(analytics.DimCampaign, trafficTopRows)
	if err != nil {
		return nil, err
	}
	landing, err := top(analytics.DimLanding, trafficTopRows)
	if err != nil {
		return nil, err
	}
	devices, err := top(analytics.DimDevice, 0)
	if err != nil {
		return nil, err
	}
	countries, err := top(analytics.DimCountry, trafficTopRows)
	if err != nil {
		return nil, err
	}

	byChannel := map[string]analytics.Counts{}
	for _, c := range channels {
		byChannel[c.K1] = c.Counts
	}
	leading, leadingVisits := "", int64(0)
	for _, c := range trafficChannels {
		if n := byChannel[c.Key].Visits; n > leadingVisits {
			leading, leadingVisits = c.Name, n
		}
	}
	topChannel := ui.Stat{Label: "Top channel", Value: placeholder, Note: "No visits in this period."}
	if leading != "" {
		topChannel = ui.Stat{Label: "Top channel", Badge: &ui.Badge{Label: leading, Kind: "neutral"}, Note: share(leadingVisits, totals.Visits) + " of visits"}
	}
	perVisit := "No visits in this period."
	if totals.Visits > 0 {
		perVisit = strconv.FormatFloat(float64(totals.Pageviews)/float64(totals.Visits), 'f', 1, 64) + " per visit"
	}
	src := a.dataURL("traffic.json", rg, false)
	p := &trafficPage{
		Lede: trafficLede,
		KPIs: []ui.Stat{
			{Label: "Visits", Value: fmtNum(totals.Visits), Note: analytics.VisitDefinition,
				Change: kpiDelta(cmp, rg, float64(totals.Visits), float64(prevTotals.Visits), false, false),
				Spark:  liveSpark("aia-spark-t-visits", 1, ui.SparkLive{Src: src, Chart: "channels"})},
			{Label: "Pageviews", Value: fmtNum(totals.Pageviews), Note: perVisit,
				Change: kpiDelta(cmp, rg, float64(totals.Pageviews), float64(prevTotals.Pageviews), false, false)},
			{Label: "Leads", Value: placeholder, Note: leadsLater},
			{Label: "Conv. rate", Value: placeholder, Note: leadsLater},
			topChannel,
		},
		Attribution: ui.Segmented{Mode: "buttons", Label: "Attribution", Items: []ui.SegmentedItem{
			{Label: "First touch", Pressed: true, Disabled: true}, {Label: "Last touch", Disabled: true}}},
		LeadsNote:    leadsLater,
		ChannelsHelp: trafficChannelHelp,
	}

	cols := []string{"Channel", "Visits", "Share"}
	right := []bool{false, true, false}
	cols, right = append(cols, "Leads", "Conv. rate"), append(right, true, true)
	if cmp {
		cols, right = append(cols, "vs previous"), append(right, true)
	}
	cols, right = append(cols, "Trend"), append(right, false)
	p.Channels = ui.AiaRows{Caption: "Visits by channel", Columns: cols, Right: right, Empty: "No visits in this period."}
	for i, c := range trafficChannels {
		n := byChannel[c.Key]
		pct := ratio(n.Visits, totals.Visits)
		name := ui.AiaCell{Text: c.Name}
		if c.Key == analytics.ChannelAI {
			name.Badge = &ui.Badge{Label: "new", Kind: "info"}
		}
		row := []ui.AiaCell{name, {Text: fmtNum(n.Visits)},
			{Text: fixed(pct, 1), Bar: &ui.Bar{Label: c.Name + " share of visits", Pct: pct, Kind: "series-" + strconv.Itoa(min(i+1, 6))}},
			{Text: placeholder, Muted: true}, {Text: placeholder, Muted: true}}
		if cmp {
			row = append(row, ui.AiaCell{Text: kpiDelta(true, rg, float64(n.Visits), float64(prevChannels[c.Key].Visits), false, false).Text})
		}
		row = append(row, ui.AiaCell{Spark: liveSpark("aia-spark-t-"+c.Key, min(i+1, 6), ui.SparkLive{Src: src, Chart: "channels", Dataset: c.Name})})
		p.Channels.Rows = append(p.Channels.Rows, row)
	}
	if totals.Visits == 0 && totals.Pageviews == 0 {
		p.Channels.Rows = nil
	}

	p.Sources = ui.AiaRows{Caption: "Top sources", Columns: []string{"Source", "Channel", "Visits", "Leads"}, Right: []bool{false, false, true, true}, Empty: "No sources in this period."}
	for _, s := range sources {
		p.Sources.Rows = append(p.Sources.Rows, []ui.AiaCell{{Text: sourceName(s.K1)}, {Text: sourceChannel(s.K2), Muted: true}, {Text: fmtNum(s.Visits)}, {Text: placeholder, Muted: true}})
	}
	p.Campaigns = ui.AiaRows{Caption: "Top campaigns", Columns: []string{"Campaign", "Medium", "Visits", "Leads", "Rate"}, Right: []bool{false, false, true, true, true}, Empty: "No campaigns in this period. Tag links with utm_campaign."}
	for _, c := range campaigns {
		p.Campaigns.Rows = append(p.Campaigns.Rows, []ui.AiaCell{{Text: c.K1}, {Text: c.K2, Muted: true}, {Text: fmtNum(c.Visits)}, {Text: placeholder, Muted: true}, {Text: placeholder, Muted: true}})
	}

	p.Landing = ui.BarList{Label: "Landing pages", NameHeading: "Page", ValueHeading: "Visits", Mono: true, Empty: "No landing pages in this period."}
	for _, l := range landing {
		p.Landing.Items = append(p.Landing.Items, ui.BarItem{Label: l.K1, Value: fmtNum(l.Visits), N: float64(l.Visits)})
	}
	var deviceVisits int64
	for _, d := range devices {
		deviceVisits += d.Visits
	}
	p.Devices = ui.BarList{Label: "Devices", NameHeading: "Device", ValueHeading: "Visits", Empty: "No visits in this period."}
	for _, d := range devices {
		p.Devices.Items = append(p.Devices.Items, ui.BarItem{Label: titleWord(d.K1), Value: fmtNum(d.Visits), Share: share(d.Visits, deviceVisits), N: float64(d.Visits)})
	}

	configured, loaded := false, false
	if gs := a.deps.Growth.GeoStatus; gs != nil {
		configured, loaded = gs()
	}
	p.Geo = ui.Badge{Label: "Not configured", Kind: "neutral"}
	switch {
	case loaded:
		p.Geo = ui.Badge{Label: "Loaded", Kind: "success"}
	case configured:
		p.Geo = ui.Badge{Label: "Could not be read", Kind: "warning"}
	}
	switch {
	case !configured:
		p.CountriesSetup = "Set HATAMOTO_GEOIP_DB to a DB-IP Lite or GeoLite2 country .mmdb file."
	case !loaded:
		p.CountriesSetup = "The country file set in HATAMOTO_GEOIP_DB could not be read. Check the path, then restart."
	}
	p.Countries = ui.BarList{Label: "Countries", NameHeading: "Country", ValueHeading: "Visits", Empty: "No visits with a known country in this period."}
	for _, c := range countries {
		p.Countries.Items = append(p.Countries.Items, ui.BarItem{Label: countryName(c.K1), Value: fmtNum(c.Visits), N: float64(c.Visits)})
	}

	p.EngagementOn = true
	if es := a.deps.Growth.EngagementSetting; es != nil {
		p.EngagementShown, p.EngagementOn = true, es.Enabled(ctx)
	}
	bounced := max(totals.Visits-totals.Engaged, 0)
	p.Engagement = ui.BarList{Label: "Engagement", NameHeading: "Visits that", ValueHeading: "Visits", Empty: "No visits in this period."}
	if !p.EngagementOn {
		p.Engagement.Empty = "Engagement counting is off (see Measurement below)."
	} else if totals.Visits > 0 {
		for _, e := range []struct {
			Label string
			N     int64
		}{{"Bounced", bounced}, {"2+ pages", totals.Multi}, {"Scrolled 75%", totals.Scrolled}} {
			p.Engagement.Items = append(p.Engagement.Items, ui.BarItem{Label: e.Label, Value: fmtNum(e.N), Share: share(e.N, totals.Visits), N: float64(e.N)})
		}
	}
	if u := userFrom(ctx); u != nil {
		p.Owner = u.Role == "owner"
	}
	p.Return = sectionHref(a.adminPath+"/analytics/traffic", q)
	return p, nil
}

func firstErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}

// titleWord upper-cases the first letter of a key ("desktop" to "Desktop").
func titleWord(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
