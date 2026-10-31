package admin

import (
	"context"
	"errors"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/agents"
	"hatamoto.dev/core/internal/httpx"
)

// The AI-crawler and AI-referral dashboards. Every number comes from one
// builder (overviewData, agentData, ...) that feeds both the page (its tables,
// readable without JavaScript) and the JSON endpoint the aia chart modules draw from, so a
// chart can never disagree with the table under it.

const (
	dayLayout        = "2006-01-02"
	defaultRangeDays = 30
	maxRangeDays     = 366
	analyticsPerPage = 25
	maxChartSeries   = 5  // further series are summed into "Other"
	maxDailyDays     = 31 // longer ranges are charted and tabled by ISO week
)

var purposeLabels = map[string]string{
	"training": "Training", "search_index": "Search index", "user_fetch": "User fetch", "other_ai": "Other AI",
	"search": "Search", "social": "Social", "seo": "SEO", "archive": "Archive",
}

func purposeLabel(p string) string {
	if l, ok := purposeLabels[p]; ok {
		return l
	}
	return p
}

func (a *Admin) mountAnalytics(authed *httpx.Group) {
	authed.HandleFunc("GET /analytics", a.analyticsSectionSelect(a.analyticsSection("ai-overview", a.buildOverview)))
	authed.HandleFunc("GET /analytics/traffic", a.analyticsSection("ai-traffic", a.buildTraffic))
	authed.HandleFunc("GET /analytics/crawlers", legacyTabs(a.adminPath, a.analyticsSection("ai-crawlers", a.buildCrawlers)))
	authed.HandleFunc("GET /analytics/content", a.analyticsSection("ai-content", a.buildContent))
	authed.HandleFunc("GET /analytics/google", a.analyticsSection("ai-google", a.buildGoogle))
	authed.HandleFunc("GET /analytics/referrals", a.analyticsSection("ai-referrals", a.buildReferrals))
	authed.HandleFunc("GET /analytics/health", a.analyticsSection("ai-health", a.buildHealth))
	authed.HandleFunc("GET /analytics/agents/{id}", a.handleAgentPage)
	authed.HandleFunc("GET /analytics/agents/robots", redirect301(a.adminPath+"/analytics/crawlers#w-cr-list"))
	authed.HandleFunc("GET /analytics/pageviews", redirect301(a.adminPath+"/analytics/referrals"))
	authed.HandleFunc("POST /analytics/crawlers/robots", a.handleRobotsSave)
	authed.HandleFunc("POST /analytics/traffic/settings", a.handleTrafficSettings)
	data := authed.Group("/analytics/data")
	data.HandleFunc("GET /overview.json", a.jsonEndpoint(a.overviewJSON))
	data.HandleFunc("GET /traffic.json", a.jsonEndpoint(a.trafficJSON))
	data.HandleFunc("GET /agents.json", a.jsonEndpoint(a.agentsJSON))
	data.HandleFunc("GET /agent.json", a.jsonEndpoint(a.agentJSON))
	data.HandleFunc("GET /pages.json", a.jsonEndpoint(a.pagesJSON))
	data.HandleFunc("GET /first-crawls.json", a.jsonEndpoint(a.firstCrawlsJSON))
	data.HandleFunc("GET /google.json", a.jsonEndpoint(a.googleJSON))
	data.HandleFunc("GET /referrals.json", a.jsonEndpoint(a.referralsJSON))
	data.HandleFunc("GET /errors.json", a.jsonEndpoint(a.errorsJSON))
	data.HandleFunc("GET /robots.json", a.robotsJSON)
}

func (a *Admin) registry() *agents.Registry {
	if a.deps.Growth.AgentRegistry != nil {
		return a.deps.Growth.AgentRegistry
	}
	return agents.Default()
}

func (a *Admin) agentName(id string) string {
	if ag := a.registry().ByID(id); ag != nil {
		return ag.Name
	}
	return id
}

// ---- date range ----

// analyticsNow is the clock of the dashboards (tests pin it).
var analyticsNow = time.Now

func today() time.Time { return analyticsNow().UTC().Truncate(24 * time.Hour) }

// parseRange reads ?from&to (YYYY-MM-DD, inclusive, UTC) like the agent
// analytics API: the last 30 days by default, from > to or over a year refused.
// A rejected range is never returned: on any error the caller gets the
// default range together with the message, so it is always safe to use.
func parseRange(q url.Values) (agents.Range, string) {
	rg, msg := parseRangeRaw(q)
	if msg != "" {
		t := today()
		return agents.Range{From: t.AddDate(0, 0, -(defaultRangeDays - 1)), To: t}, msg
	}
	return rg, ""
}

func parseRangeRaw(q url.Values) (agents.Range, string) {
	t := today()
	rg := agents.Range{From: t.AddDate(0, 0, -(defaultRangeDays - 1)), To: t}
	hasFrom, hasTo := q.Get("from") != "", q.Get("to") != ""
	for _, p := range []struct {
		name string
		dst  *time.Time
	}{{"from", &rg.From}, {"to", &rg.To}} {
		if v := q.Get(p.name); v != "" {
			d, err := time.Parse(dayLayout, v)
			if err != nil {
				return agents.Range{From: t.AddDate(0, 0, -(defaultRangeDays - 1)), To: t}, p.name + " must be a date like 2026-01-31"
			}
			*p.dst = d
		}
	}
	switch {
	case hasTo && !hasFrom:
		rg.From = rg.To.AddDate(0, 0, -(defaultRangeDays - 1))
	case hasFrom && !hasTo && rg.From.After(rg.To):
		rg.To = rg.From
	}
	if rg.From.After(rg.To) {
		return rg, "The start date must not be after the end date"
	}
	if rg.To.Sub(rg.From) > (maxRangeDays-1)*24*time.Hour {
		return rg, "The range must not exceed 366 days"
	}
	return rg, ""
}

func rangeDays(rg agents.Range) []string {
	var out []string
	for d := rg.From; !d.After(rg.To); d = d.AddDate(0, 0, 1) {
		out = append(out, d.Format(dayLayout))
	}
	return out
}

// rangePreset is a named range ending today: the entries of the date picker.
type rangePreset struct {
	Label    string
	From, To time.Time
}

// rangePresets are the picker's presets for the day t (UTC). Every one stays
// inside the 366 day cap.
func rangePresets(t time.Time) []rangePreset {
	return []rangePreset{
		{"Today", t, t},
		{"Last 7 days", t.AddDate(0, 0, -6), t},
		{"Last 30 days", t.AddDate(0, 0, -29), t},
		{"Last 3 months", t.AddDate(0, -3, 0), t},
		{"Last 6 months", t.AddDate(0, -6, 0), t},
		{"Month to date", time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC), t},
		{"Year to date", time.Date(t.Year(), time.January, 1, 0, 0, 0, 0, time.UTC), t},
	}
}

// span is a range as "20 Sep – 19 Oct" (day first, like every admin date), the
// dates the picker shows next to a preset name. A one day range is one date.
func span(from, to time.Time) string {
	if from.Equal(to) {
		return from.Format("2 Jan")
	}
	return from.Format("2 Jan") + " – " + to.Format("2 Jan")
}

// compareState is whether a page compares with the previous period.
type compareState struct {
	On       bool
	Prev     agents.Range
	Disabled bool // the previous period starts before the first day with data
}

// compareFor is the compare state of a request for rg: on when asked for and
// the previous period reaches no earlier than the picker's minimum date.
func compareFor(rg agents.Range, asked bool) compareState {
	prev := previousRange(rg)
	min, _ := time.Parse(dayLayout, analyticsMinDate)
	disabled := prev.From.Before(min)
	return compareState{On: asked && !disabled, Prev: prev, Disabled: disabled}
}

// analyticsMinDate is the earliest day the picker offers.
const analyticsMinDate = "2020-01-01"

// dateRangeFor is the picker for rg. With cmp it carries compare=1 on every
// preset and shows the compare switch; the switch itself posts compare with
// Apply, so hidden never holds it.
func (a *Admin) dateRangeFor(base string, rg agents.Range, hidden map[string]string, cmp *compareState) ui.DateRange {
	t := today()
	from, to := rg.From.Format(dayLayout), rg.To.Format(dayLayout)
	href := func(f, e string) string {
		q := url.Values{}
		for k, v := range hidden {
			q.Set(k, v)
		}
		q.Set("from", f)
		q.Set("to", e)
		if cmp != nil && cmp.On {
			q.Set("compare", "1")
		}
		return base + "?" + q.Encode()
	}
	d := ui.DateRange{Action: base, From: from, To: to, Hidden: hidden, Min: analyticsMinDate, Max: t.Format(dayLayout)}
	d.Label = fmtDate(rg.From) + " - " + fmtDate(rg.To)
	matched := false
	for _, p := range rangePresets(t) {
		pf, pt := p.From.Format(dayLayout), p.To.Format(dayLayout)
		// Presets can span the same days (Last 30 days and Month to date on the
		// 30th of a 30 day month): the first, listed order, names the range.
		on := pf == from && pt == to && !matched
		if on {
			matched = true
			d.Label = p.Label + " · " + span(rg.From, rg.To)
		}
		d.Quick = append(d.Quick, ui.DatePreset{Label: p.Label, Href: href(pf, pt), Active: on})
	}
	if cmp != nil {
		prev := span(cmp.Prev.From, cmp.Prev.To)
		c := &ui.DateCompare{Field: "compare", On: cmp.On, Disabled: cmp.Disabled, Label2: "vs " + prev}
		switch {
		case cmp.Disabled:
			c.Help = "There is no earlier data to compare with this range."
		case cmp.On:
			c.Help = "Compared with " + prev + "."
		default:
			c.Help = "Compare with " + prev + "."
		}
		d.Compare = c
	}
	return d
}

// ---- chart and table builders ----

type chartSpec struct {
	Kind       string        `json:"kind"` // bar | line | doughnut
	Stacked    bool          `json:"stacked"`
	Horizontal bool          `json:"horizontal,omitempty"`
	Bucket     string        `json:"bucket,omitempty"` // "week" when Labels are week starts
	Center     string        `json:"center,omitempty"` // doughnut: the noun under the total in the hole
	Format     string        `json:"format,omitempty"` // number | compact | percent: how values print on axes and tooltips
	Labels     []string      `json:"labels"`
	Datasets   []chartSeries `json:"datasets"`
	// Previous is the same chart over the previous period; set only when compare is on.
	Previous *chartPrevious `json:"previous,omitempty"`
	// daily is the matrix the chart was built from, before bucketing, so a
	// previous period can be re-bucketed by this chart's weeks.
	daily *matrix
}

// chartPrevious is a chart's previous period: Labels and Datasets are in the
// chart's own bucket (day or ISO week), its series in the same order as the
// chart's, so the client lines them up by index.
type chartPrevious struct {
	From     string        `json:"from"`
	To       string        `json:"to"`
	Labels   []string      `json:"labels"`
	Datasets []chartSeries `json:"datasets"`
}

// withPrevious is the chart with prev, the same chart built over previous, as its previous period.
// When both are weekly, the previous period is lined up day by day with this
// chart and bucketed by this chart's ISO weeks, so each bucket compares the
// same number of days.
func (c chartSpec) withPrevious(previous agents.Range, prev chartSpec) chartSpec {
	if c.Bucket == "week" && c.daily != nil && prev.daily != nil && len(c.daily.Days) == len(prev.daily.Days) {
		m := *prev.daily
		m.Ref = c.daily.Days
		prev = m.chart(prev.Kind, prev.Stacked)
	}
	c.Previous = &chartPrevious{From: previous.From.Format(dayLayout), To: previous.To.Format(dayLayout), Labels: prev.Labels, Datasets: prev.Datasets}
	return c
}

// only is the chart with just the series called label (and the same one of its
// previous period), so a page can draw the series of one chart in panels of
// their own scales.
func (c chartSpec) only(label string) chartSpec {
	keep := func(in []chartSeries) []chartSeries {
		out := []chartSeries{}
		for _, d := range in {
			if d.Label == label {
				out = append(out, d)
			}
		}
		return out
	}
	c.Datasets = keep(c.Datasets)
	if c.Previous != nil {
		p := *c.Previous
		p.Datasets = keep(p.Datasets)
		c.Previous = &p
	}
	return c
}

// seriesLabels are the chart's series names in order: the columns to pivot the
// previous period by.
func (c chartSpec) seriesLabels() []string {
	out := make([]string, len(c.Datasets))
	for i, d := range c.Datasets {
		out[i] = d.Label
	}
	return out
}

// previousRange is the period just before rg, of the same number of days: the
// one definition of "previous" for compare.
func previousRange(rg agents.Range) agents.Range {
	n := len(rangeDays(rg))
	return agents.Range{From: rg.From.AddDate(0, 0, -n), To: rg.From.AddDate(0, 0, -1)}
}

// parseCompare is whether the request asks for the previous period too.
func parseCompare(q url.Values) bool { return q.Get("compare") == "1" }

type chartSeries struct {
	Label string  `json:"label"`
	Data  []int64 `json:"data"`
	// Type draws this series as "line" or "bar" inside a chart of another kind
	// (a combo chart); Axis "right" gives it its own scale on the right.
	Type string `json:"type,omitempty"`
	Axis string `json:"axis,omitempty"`
}

// matrix is long (day, key, value) rows pivoted to one series per key over every
// day of the range. Keys beyond maxChartSeries by total are summed into "Other".
type matrix struct {
	Days []string
	Cols []string
	Vals map[string][]int64
	// Ref, when set, holds for each day of Days the day of the current range it
	// is compared with; weekly buckets follow Ref's weeks and are named by the
	// Monday of the first Days entry in them.
	Ref []string
}

func pivot(days []string, pts []agents.SeriesPoint, name func(string) string) matrix {
	return pivotTop(days, pts, name, maxChartSeries)
}

// pivotTop is pivot keeping the top n keys; the rest become "Other". n <= 0 keeps all.
func pivotTop(days []string, pts []agents.SeriesPoint, name func(string) string, n int) matrix {
	idx := map[string]int{}
	for i, d := range days {
		idx[d] = i
	}
	byKey := map[string][]int64{}
	totals := map[string]int64{}
	for _, p := range pts {
		i, ok := idx[p.Day]
		if !ok {
			continue
		}
		k := name(p.Key)
		if byKey[k] == nil {
			byKey[k] = make([]int64, len(days))
		}
		byKey[k][i] += p.Hits
		totals[k] += p.Hits
	}
	keys := make([]string, 0, len(byKey))
	for k := range byKey {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if totals[keys[i]] != totals[keys[j]] {
			return totals[keys[i]] > totals[keys[j]]
		}
		return keys[i] < keys[j]
	})
	m := matrix{Days: days, Vals: map[string][]int64{}}
	for i, k := range keys {
		if n <= 0 || i < n {
			m.Cols = append(m.Cols, k)
			m.Vals[k] = byKey[k]
			continue
		}
		if m.Vals["Other"] == nil {
			m.Cols = append(m.Cols, "Other")
			m.Vals["Other"] = make([]int64, len(days))
		}
		for d, v := range byKey[k] {
			m.Vals["Other"][d] += v
		}
	}
	return m
}

// purposeOrder is the fixed order of the fetches-by-purpose series, as the
// prototype draws them, so each purpose keeps its colour whatever the totals.
var purposeOrder = []string{"Training", "Search index", "User fetch", "Other AI"}

// searchSeriesName is the one series that sums every search engine in the hero chart.
const searchSeriesName = "Search engines"

// allPurposeOrder is purposeOrder with the search engines last (drawn grey).
var allPurposeOrder = append(append([]string{}, purposeOrder...), searchSeriesName)

// allPurposeName names a purpose series of the AI + search chart.
func allPurposeName(k string) string {
	if k == "search" {
		return searchSeriesName
	}
	return purposeLabel(k)
}

// pivotOrdered is pivot with the given columns first, in that order and all
// present even when empty; any other key is summed into a trailing "Other",
// which exists only when something landed there.
func pivotOrdered(days []string, pts []agents.SeriesPoint, name func(string) string, order []string) matrix {
	m := pivotFixed(days, pts, name, append(append([]string{}, order...), "Other"))
	if dailySumsOf(m.Vals["Other"]) == 0 {
		delete(m.Vals, "Other")
		m.Cols = m.Cols[:len(m.Cols)-1]
	}
	return m
}

func dailySumsOf(v []int64) (n int64) {
	for _, x := range v {
		n += x
	}
	return n
}

// pivotFixed is pivot over the given columns, for a previous period that must
// match the current chart's series. Keys not in cols are summed into "Other"
// when cols has it and dropped otherwise. It never changes cols.
func pivotFixed(days []string, pts []agents.SeriesPoint, name func(string) string, cols []string) matrix {
	idx := map[string]int{}
	for i, d := range days {
		idx[d] = i
	}
	m := matrix{Days: days, Cols: cols, Vals: map[string][]int64{}}
	for _, c := range cols {
		m.Vals[c] = make([]int64, len(days))
	}
	_, hasOther := m.Vals["Other"]
	for _, p := range pts {
		i, ok := idx[p.Day]
		if !ok {
			continue
		}
		k := name(p.Key)
		if _, known := m.Vals[k]; !known {
			if !hasOther {
				continue
			}
			k = "Other"
		}
		m.Vals[k][i] += p.Hits
	}
	return m
}

// weekly sums a matrix into ISO weeks (UTC, Monday to Sunday). A week is named
// by its Monday, also when the range starts or ends part way through it.
func (m matrix) weekly() matrix {
	monday := func(d string) (string, bool) {
		t, err := time.Parse(dayLayout, d)
		if err != nil {
			return "", false
		}
		return t.AddDate(0, 0, -((int(t.Weekday()) + 6) % 7)).Format(dayLayout), true
	}
	out := matrix{Cols: m.Cols, Vals: map[string][]int64{}}
	at := map[string]int{}
	for i, d := range m.Days {
		mon, ok := monday(d)
		if !ok {
			continue
		}
		key := mon
		if len(m.Ref) == len(m.Days) {
			if key, ok = monday(m.Ref[i]); !ok {
				continue
			}
		}
		w, ok := at[key]
		if !ok {
			w = len(out.Days)
			at[key] = w
			out.Days = append(out.Days, mon)
			for _, c := range m.Cols {
				out.Vals[c] = append(out.Vals[c], 0)
			}
		}
		for _, c := range m.Cols {
			out.Vals[c][w] += m.Vals[c][i]
		}
	}
	return out
}

// bucketed is the matrix as charts and tables show it: per day up to
// maxDailyDays, per ISO week beyond. The unit is "day" or "week".
func (m matrix) bucketed() (matrix, string) {
	if len(m.Days) <= maxDailyDays {
		return m, "day"
	}
	return m.weekly(), "week"
}

func (m matrix) chart(kind string, stacked bool) chartSpec {
	daily := m
	m, unit := m.bucketed()
	c := chartSpec{Kind: kind, Stacked: stacked, Format: "compact", Labels: m.Days, Datasets: []chartSeries{}, daily: &daily}
	if unit == "week" {
		c.Bucket = unit
	}
	for _, col := range m.Cols {
		c.Datasets = append(c.Datasets, chartSeries{Label: col, Data: m.Vals[col]})
	}
	return c
}

// colTotals is the total of each column over the whole range, in column order.
func (m matrix) colTotals() []int64 {
	out := make([]int64, len(m.Cols))
	for i, c := range m.Cols {
		for _, v := range m.Vals[c] {
			out[i] += v
		}
	}
	return out
}

// share is the matrix as a doughnut of column totals, and the ranked table
// under it (name, count, share of the whole).
func (m matrix) share(caption, first, noun string) (chartSpec, ui.DataTable) {
	tot := m.colTotals()
	c := chartSpec{Kind: "doughnut", Format: "compact", Center: noun, Labels: []string{}, Datasets: []chartSeries{{Label: first, Data: []int64{}}}}
	t := ui.DataTable{Caption: caption, Columns: []string{first, "Fetches", "Share"}, Empty: "No fetches in this range.", Flat: true}
	var all int64
	for _, n := range tot {
		all += n
	}
	if all == 0 {
		return c, t
	}
	for i, col := range m.Cols {
		c.Labels = append(c.Labels, col)
		c.Datasets[0].Data = append(c.Datasets[0].Data, tot[i])
		t.Rows = append(t.Rows, ui.DataRow{Label: col, Cells: []string{fmtNum(tot[i]), share(tot[i], all)}})
	}
	return c, t
}

// share is n of all as a whole percentage ("38%").
func share(n, all int64) string {
	if all <= 0 {
		return "n/a"
	}
	// half up, like the ring (int(x + 0.5)); FormatFloat would round 12.5 to 12
	return strconv.Itoa(int(math.Round(float64(n)/float64(all)*100))) + "%"
}

// longDay turns 2026-09-15 into "15 Sep 2026".
func longDay(d string) string {
	t, err := time.Parse(dayLayout, d)
	if err != nil {
		return d
	}
	return t.Format("2 Jan 2006")
}

// shortDay turns 2026-09-15 into "Sep 15".
func shortDay(d string) string {
	t, err := time.Parse(dayLayout, d)
	if err != nil {
		return d
	}
	return t.Format("2 Jan")
}

func fmtNum(n int64) string {
	s := strconv.FormatInt(n, 10)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	if neg {
		s = "-" + s
	}
	return s
}

func (m matrix) table(caption, first string) ui.DataTable {
	m, unit := m.bucketed()
	t := ui.DataTable{Caption: caption, Columns: append([]string{first}, m.Cols...), Flat: true}
	if unit == "week" {
		t.Columns[0] = "Week"
		t.Caption = strings.ReplaceAll(caption, " per day", " per week")
	}
	t.Columns = append(t.Columns, "Total")
	sums := make([]int64, len(m.Cols)+1)
	var any bool
	for i, d := range m.Days {
		row := ui.DataRow{Label: longDay(d)}
		if unit == "week" {
			row.Label = "Week of " + shortDay(d)
		}
		var rowTotal int64
		for c, col := range m.Cols {
			v := m.Vals[col][i]
			row.Cells = append(row.Cells, fmtNum(v))
			sums[c] += v
			rowTotal += v
		}
		row.Cells = append(row.Cells, fmtNum(rowTotal))
		sums[len(m.Cols)] += rowTotal
		any = any || rowTotal > 0
		t.Rows = append(t.Rows, row)
	}
	if !any {
		t.Rows = nil
		return t
	}
	total := ui.DataRow{Label: "Total"}
	for _, s := range sums {
		total.Cells = append(total.Cells, fmtNum(s))
	}
	t.Total = &total
	return t
}

// dayCounts fills every day of the range for a single series.
func dayCounts(days []string, in map[string]int64) []int64 {
	out := make([]int64, len(days))
	for i, d := range days {
		out[i] = in[d]
	}
	return out
}

func pct(verified, spoofed, unverifiable int64) string {
	decided := verified + spoofed + unverifiable
	if decided == 0 {
		return "n/a"
	}
	return strconv.FormatFloat(float64(verified)/float64(decided)*100, 'f', 0, 64) + "%"
}

// ---- JSON endpoints ----

type jsonHandler func(ctx context.Context, q url.Values, rg agents.Range) (any, int, string)

// jsonEndpoint wraps a builder as a session-protected JSON endpoint: a bad
// range is 400, a missing service 503, everything else 500, all as {"error"}.
func (a *Admin) jsonEndpoint(h jsonHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rg, msg := parseRange(r.URL.Query())
		if msg != "" {
			a.writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg})
			return
		}
		body, status, errMsg := h(r.Context(), r.URL.Query(), rg)
		if errMsg != "" {
			a.writeJSON(w, status, map[string]string{"error": errMsg})
			return
		}
		a.writeJSON(w, http.StatusOK, body)
	}
}

func (a *Admin) agentStats() (AgentStats, bool) {
	s := a.deps.Growth.Agents
	return s, s != nil
}

type totalsJSON struct {
	AIFetches    int64  `json:"ai_fetches"`
	Verified     int64  `json:"verified"`
	Spoofed      int64  `json:"spoofed"`
	Unverifiable int64  `json:"unverifiable"`
	VerifiedPct  string `json:"verified_percent"`
	AIVisits     int64  `json:"ai_referred_visits"`
	Pageviews    int64  `json:"pageviews"`
	// Status4xx and Status5xx count responses to AI crawlers in the range, the
	// population AIFetches counts.
	Status4xx int64 `json:"status_4xx"`
	Status5xx int64 `json:"status_5xx"`
}

type agentJSON struct {
	ID           string `json:"agent_id"`
	Name         string `json:"name"`
	Operator     string `json:"operator"`
	Purpose      string `json:"purpose"`
	Hits         int64  `json:"hits"`
	Verified     int64  `json:"verified_hits"`
	Spoofed      int64  `json:"spoofed_hits"`
	Unverifiable int64  `json:"unverifiable_hits"`
	VerifiedPct  string `json:"verified_percent"`
	LastSeen     string `json:"last_seen_day"`
	// IgnoresRobots is the registry's note that the agent fetches what robots.txt forbids.
	IgnoresRobots bool `json:"ignores_robots"`
}

func (a *Admin) agentRow(t agents.AgentTotals) agentJSON {
	op, pu, ignores := t.Operator, t.Purpose, false
	if ag := a.registry().ByID(t.AgentID); ag != nil {
		op, pu, ignores = ag.Operator, string(ag.Purpose), ag.IgnoresRobots
	}
	return agentJSON{IgnoresRobots: ignores, ID: t.AgentID, Name: a.agentName(t.AgentID), Operator: op, Purpose: purposeLabel(pu), Hits: t.Hits, Verified: t.Verified, Spoofed: t.Spoofed,
		Unverifiable: t.Unverifiable, VerifiedPct: pct(t.Verified, t.Spoofed, t.Unverifiable), LastSeen: t.LastSeenDay}
}

type overviewView struct {
	From   string               `json:"from"`
	To     string               `json:"to"`
	Group  string               `json:"group"`
	Totals totalsJSON           `json:"totals"`
	Charts map[string]chartSpec `json:"charts"`
	Top    []agentJSON          `json:"top_agents"`
	// what the section pages read: the fetches matrix and the per-day health rows
	fetchM matrix
	// fetchAllM is fetchM plus the search engines (group "purpose" only).
	fetchAllM matrix
	health    []agents.HealthDay
	// Previous is the headline totals of the previous period; set only when compare is on.
	Previous *overviewPrevious `json:"previous,omitempty"`
}

type overviewPrevious struct {
	From   string     `json:"from"`
	To     string     `json:"to"`
	Totals totalsJSON `json:"totals"`
}

func groupTitle(g string) string {
	if g == "purpose" {
		return "Purpose"
	}
	return "Operator"
}

func groupName(g string) func(string) string {
	if g == "purpose" {
		return purposeLabel
	}
	return func(s string) string { return s }
}

func (a *Admin) overviewData(ctx context.Context, rg agents.Range, group string) (*overviewView, error) {
	stats, ok := a.agentStats()
	if !ok {
		return nil, errNoService
	}
	if group != "purpose" {
		group = "operator"
	}
	totals, err := stats.Overview(ctx, rg)
	if err != nil {
		return nil, err
	}
	series, err := stats.Series(ctx, rg, group)
	if err != nil {
		return nil, err
	}
	top, err := stats.TopAgents(ctx, rg, 10)
	if err != nil {
		return nil, err
	}
	health, err := stats.HealthSeries(ctx, rg)
	if err != nil {
		return nil, err
	}
	days := rangeDays(rg)
	m := pivot(days, series, groupName(group))
	if group == "purpose" {
		m = pivotOrdered(days, series, groupName(group), purposeOrder)
	}
	v := &overviewView{From: rg.From.Format(dayLayout), To: rg.To.Format(dayLayout), Group: group, Charts: map[string]chartSpec{"fetches": m.chart("bar", true)}}
	if group == "purpose" {
		all, err := stats.SeriesWithSearch(ctx, rg, group)
		if err != nil {
			return nil, err
		}
		v.fetchAllM = pivotOrdered(days, all, allPurposeName, allPurposeOrder)
		v.Charts["fetches_all"] = v.fetchAllM.chart("bar", true)
	}
	v.Totals = totalsJSON{AIFetches: totals.AIFetches, Verified: totals.Verified, Spoofed: totals.Spoofed, Unverifiable: totals.Unverifiable,
		VerifiedPct: pct(totals.Verified, totals.Spoofed, totals.Unverifiable), AIVisits: totals.AIVisits, Pageviews: totals.Pageviews}
	for _, h := range health {
		v.Totals.Status4xx += h.AI4xx
		v.Totals.Status5xx += h.AI5xx
	}
	v.Charts["verification"], v.Charts["status_classes"], v.Charts["errors_per_day"] = healthCharts(days, health)
	v.Charts["verified"], v.Charts["spoofed"] = v.Charts["verification"].only("Verified"), v.Charts["verification"].only("Spoofed")
	for _, t := range top {
		v.Top = append(v.Top, a.agentRow(t))
	}
	v.Charts["share"], _ = m.share("AI crawler fetches by "+group+" in this range", groupTitle(group), "fetches")
	v.fetchM = m
	v.health = health
	// Pageviews and AI-referred visits per day need the rollup reader.
	if vm, ok := a.visitsMatrix(ctx, rg); ok {
		v.Charts["visits"] = visitsChart(vm)
	}
	return v, nil
}

// visitsMatrix is pageviews and AI-referred visits per day; false when the
// rollup reader is missing or fails.
func (a *Admin) visitsMatrix(ctx context.Context, rg agents.Range) (matrix, bool) {
	ref := a.deps.Growth.Referrals
	if ref == nil {
		return matrix{}, false
	}
	views, err1 := ref.Pageviews(ctx, rg.From, rg.To)
	visits, err2 := ref.AIReferrals(ctx, rg.From, rg.To)
	if err1 != nil || err2 != nil {
		return matrix{}, false
	}
	days := rangeDays(rg)
	pv, ai := map[string]int64{}, map[string]int64{}
	for _, d := range views {
		pv[d.Day] = d.Views
	}
	for _, d := range visits {
		ai[d.Day] += d.Visits
	}
	return matrix{Days: days, Cols: []string{"Pageviews", "AI-referred visits"}, Vals: map[string][]int64{"Pageviews": dayCounts(days, pv), "AI-referred visits": dayCounts(days, ai)}}, true
}

// visitsChart is a combo chart: pageviews as bars, AI-referred visits (about 30
// times fewer) as a line on their own right-hand scale, so neither is flat.
func visitsChart(vm matrix) chartSpec {
	combo := vm.chart("bar", false)
	combo.Datasets[1].Type, combo.Datasets[1].Axis = "line", "right"
	return combo
}

// healthCharts are the Health section's three series over the range's days:
// verified and spoofed fetches, response classes (stacked), and failed against
// all fetches per day (the tracker takes the share from the pair).
func healthCharts(days []string, health []agents.HealthDay) (verification, classes, failing chartSpec) {
	cols := map[string]map[string]int64{}
	for _, c := range []string{"Verified", "Spoofed", "2xx success", "3xx redirect", "4xx client error", "5xx server error", "Failed fetches", "All fetches"} {
		cols[c] = map[string]int64{}
	}
	for _, h := range health {
		cols["Verified"][h.Day], cols["Spoofed"][h.Day] = h.Verified, h.Spoofed
		cols["2xx success"][h.Day], cols["3xx redirect"][h.Day] = h.S2xx, h.S3xx
		cols["4xx client error"][h.Day], cols["5xx server error"][h.Day] = h.S4xx, h.S5xx
		cols["Failed fetches"][h.Day], cols["All fetches"][h.Day] = h.S4xx+h.S5xx, h.Hits
	}
	build := func(names ...string) matrix {
		m := matrix{Days: days, Cols: names, Vals: map[string][]int64{}}
		for _, n := range names {
			m.Vals[n] = dayCounts(days, cols[n])
		}
		return m
	}
	return build("Verified", "Spoofed").chart("bar", false),
		build("2xx success", "3xx redirect", "4xx client error", "5xx server error").chart("bar", true),
		build("Failed fetches", "All fetches").chart("bar", false)
}

// previousTotals is the headline totals of the period just before rg, of the
// same number of days; nil when the store cannot answer, so no change shows.
func (a *Admin) previousTotals(ctx context.Context, stats AgentStats, rg agents.Range) *agents.OverviewTotals {
	t, err := stats.Overview(ctx, previousRange(rg))
	if err != nil {
		return nil
	}
	return &t
}

var errNoService = errors.New("service not available")

func (a *Admin) overviewJSON(ctx context.Context, q url.Values, rg agents.Range) (any, int, string) {
	v, err := a.overviewData(ctx, rg, q.Get("group"))
	if err == nil && parseCompare(q) {
		err = a.overviewCompare(ctx, rg, v)
	}
	return a.result(v, err, "overview")
}

// overviewCompare adds the previous period to v: its totals, and to each chart
// the same series (the current chart's own, by name) over the previous days.
func (a *Admin) overviewCompare(ctx context.Context, rg agents.Range, v *overviewView) error {
	stats, ok := a.agentStats()
	if !ok {
		return errNoService
	}
	prg := previousRange(rg)
	pdays := rangeDays(prg)
	totals, err := stats.Overview(ctx, prg)
	if err != nil {
		return err
	}
	series, err := stats.Series(ctx, prg, v.Group)
	if err != nil {
		return err
	}
	health, err := stats.HealthSeries(ctx, prg)
	if err != nil {
		return err
	}
	pt := totalsJSON{AIFetches: totals.AIFetches, Verified: totals.Verified, Spoofed: totals.Spoofed, Unverifiable: totals.Unverifiable,
		VerifiedPct: pct(totals.Verified, totals.Spoofed, totals.Unverifiable), AIVisits: totals.AIVisits, Pageviews: totals.Pageviews}
	for _, h := range health {
		pt.Status4xx += h.AI4xx
		pt.Status5xx += h.AI5xx
	}
	v.Previous = &overviewPrevious{From: prg.From.Format(dayLayout), To: prg.To.Format(dayLayout), Totals: pt}
	fetches := v.Charts["fetches"]
	v.Charts["fetches"] = fetches.withPrevious(prg, pivotFixed(pdays, series, groupName(v.Group), fetches.seriesLabels()).chart("bar", true))
	if all, ok := v.Charts["fetches_all"]; ok {
		pts, err := stats.SeriesWithSearch(ctx, prg, v.Group)
		if err != nil {
			return err
		}
		v.Charts["fetches_all"] = all.withPrevious(prg, pivotFixed(pdays, pts, allPurposeName, all.seriesLabels()).chart("bar", true))
	}
	if c, ok := v.Charts["visits"]; ok {
		if vm, ok := a.visitsMatrix(ctx, prg); ok {
			v.Charts["visits"] = c.withPrevious(prg, visitsChart(vm))
		}
	}
	pv, pc, pf := healthCharts(pdays, health)
	v.Charts["verification"] = v.Charts["verification"].withPrevious(prg, pv)
	v.Charts["verified"], v.Charts["spoofed"] = v.Charts["verification"].only("Verified"), v.Charts["verification"].only("Spoofed")
	v.Charts["status_classes"] = v.Charts["status_classes"].withPrevious(prg, pc)
	v.Charts["errors_per_day"] = v.Charts["errors_per_day"].withPrevious(prg, pf)
	return nil
}

// result converts a builder outcome into the jsonHandler return.
func (a *Admin) result(v any, err error, what string) (any, int, string) {
	switch {
	case errors.Is(err, errNoService):
		return nil, http.StatusServiceUnavailable, "analytics are not available on this server"
	case err != nil:
		a.logger().Error("analytics "+what+" failed", "err", err)
		return nil, http.StatusInternalServerError, "failed to load " + what
	}
	return v, http.StatusOK, ""
}

type agentsView struct {
	From   string      `json:"from"`
	To     string      `json:"to"`
	Agents []agentJSON `json:"agents"`
}

func (a *Admin) agentsData(ctx context.Context, rg agents.Range) (*agentsView, error) {
	stats, ok := a.agentStats()
	if !ok {
		return nil, errNoService
	}
	list, err := stats.Agents(ctx, rg)
	if err != nil {
		return nil, err
	}
	v := &agentsView{From: rg.From.Format(dayLayout), To: rg.To.Format(dayLayout), Agents: []agentJSON{}}
	for _, t := range list {
		v.Agents = append(v.Agents, a.agentRow(t))
	}
	return v, nil
}

func (a *Admin) agentsJSON(ctx context.Context, _ url.Values, rg agents.Range) (any, int, string) {
	v, err := a.agentsData(ctx, rg)
	return a.result(v, err, "agents")
}

type agentDetail struct {
	agentJSON
	From        string               `json:"from"`
	To          string               `json:"to"`
	RobotsToken string               `json:"robots_token,omitempty"`
	PagesRead   int                  `json:"pages_read"`
	LastSeenAt  string               `json:"last_seen_at,omitempty"`
	Charts      map[string]chartSpec `json:"charts"`
	TopPaths    []agents.PathCount   `json:"top_paths"`
	ByStatus    []agents.StatusCount `json:"by_status"`
	daily       ui.DataTable
	dailyM      matrix
	Found       bool `json:"-"`
}

func (a *Admin) agentData(ctx context.Context, rg agents.Range, id string) (*agentDetail, error) {
	stats, ok := a.agentStats()
	if !ok {
		return nil, errNoService
	}
	d, err := stats.AgentDetail(ctx, rg, id)
	if err != nil {
		return nil, err
	}
	reg := a.registry().ByID(id)
	if !d.Found && reg == nil {
		return &agentDetail{}, nil
	}
	v := &agentDetail{agentJSON: a.agentRow(agents.AgentTotals{AgentID: id, Operator: d.Totals.Operator, Purpose: d.Totals.Purpose, Hits: d.Totals.Hits, Verified: d.Totals.Verified,
		Spoofed: d.Totals.Spoofed, Unverifiable: d.Totals.Unverifiable, LastSeenDay: d.Totals.LastSeenDay}),
		From: rg.From.Format(dayLayout), To: rg.To.Format(dayLayout), TopPaths: d.TopPaths, ByStatus: d.ByStatus, PagesRead: d.PagesRead, Found: true}
	if reg != nil {
		v.RobotsToken = reg.RobotsToken
	}
	if d.LastSeenAt != nil {
		v.LastSeenAt = fmtDateTime(d.LastSeenAt.UTC()) + " UTC"
	}
	if v.TopPaths == nil {
		v.TopPaths = []agents.PathCount{}
	}
	if v.ByStatus == nil {
		v.ByStatus = []agents.StatusCount{}
	}
	days := rangeDays(rg)
	per := map[string]int64{}
	for _, p := range d.Series {
		per[p.Day] = p.Hits
	}
	m := matrix{Days: days, Cols: []string{"Fetches"}, Vals: map[string][]int64{"Fetches": dayCounts(days, per)}}
	v.Charts = map[string]chartSpec{"daily": m.chart("bar", false)}
	v.daily = m.table("Fetches per day by "+v.Name, "Day")
	v.dailyM = m
	return v, nil
}

func (a *Admin) agentJSON(ctx context.Context, q url.Values, rg agents.Range) (any, int, string) {
	v, err := a.agentData(ctx, rg, q.Get("id"))
	if err == nil && !v.Found {
		return nil, http.StatusNotFound, "agent not found"
	}
	if err == nil && parseCompare(q) {
		// the same chart over the previous period, for the dashed line
		prg := previousRange(rg)
		if prev, perr := a.agentData(ctx, prg, q.Get("id")); perr != nil {
			err = perr
		} else if prev.Found {
			v.Charts["daily"] = v.Charts["daily"].withPrevious(prg, prev.Charts["daily"])
		}
	}
	return a.result(v, err, "agent")
}

type pagesView struct {
	From   string                   `json:"from"`
	To     string                   `json:"to"`
	Top    []agents.PageRow         `json:"top"`
	Total  int                      `json:"top_total"`
	Never  []agents.NeverFetchedRow `json:"never_fetched"`
	NeverN int                      `json:"never_fetched_total"`
	// PublishedTotal is every published page, so coverage is PublishedTotal - NeverN.
	PublishedTotal int                  `json:"published_pages_total"`
	NeverByType    []typeCount          `json:"never_fetched_by_type"`
	Charts         map[string]chartSpec `json:"charts"`
	typeM          matrix               // fetches per day by page type, for the page's table
}

// typeCount is a page count for one page type; label is its display name.
type typeCount struct {
	SchemaSlug *string `json:"schema_slug"`
	Label      string  `json:"label"`
	Count      int     `json:"count"`
}

// otherPages labels fetches and pages that have no page type. It is not "Other",
// which pivotTop uses for the series beyond the top five.
const otherPages = "Other pages"

// schemaLabel is a page type's display name: its slug, spaced and capitalised
// ("blog-post" is "Blog post"). The empty slug, which no page type has, is
// "Other pages". A page type whose name would read as "Other pages" or as the
// overflow "Other" gets a suffix so the three stay apart.
func schemaLabel(slug string) string {
	if slug == "" {
		return otherPages
	}
	s := strings.ReplaceAll(strings.ReplaceAll(slug, "-", " "), "_", " ")
	s = strings.ToUpper(s[:1]) + s[1:]
	if s == otherPages || s == "Other" {
		s += " (page type)"
	}
	return s
}

func (a *Admin) pagesData(ctx context.Context, rg agents.Range, page, npage int) (*pagesView, error) {
	stats, ok := a.agentStats()
	if !ok {
		return nil, errNoService
	}
	top, total, err := stats.TopPages(ctx, rg, agents.Page{Limit: analyticsPerPage, Offset: (page - 1) * analyticsPerPage})
	if err != nil {
		return nil, err
	}
	never, nn, err := stats.NeverFetched(ctx, agents.Page{Limit: analyticsPerPage, Offset: (npage - 1) * analyticsPerPage})
	if err != nil {
		return nil, err
	}
	published, err := stats.PublishedPages(ctx)
	if err != nil {
		return nil, err
	}
	byType, err := stats.FetchesBySchema(ctx, rg)
	if err != nil {
		return nil, err
	}
	neverTypes, err := stats.NeverFetchedBySchema(ctx)
	if err != nil {
		return nil, err
	}
	v := &pagesView{From: rg.From.Format(dayLayout), To: rg.To.Format(dayLayout), Top: top, Total: total, Never: never, NeverN: nn, PublishedTotal: published}
	if v.Top == nil {
		v.Top = []agents.PageRow{}
	}
	if v.Never == nil {
		v.Never = []agents.NeverFetchedRow{}
	}
	c := chartSpec{Kind: "bar", Horizontal: true, Format: "compact", Datasets: []chartSeries{{Label: "Fetches"}}}
	for i, p := range top {
		if i == 10 {
			break
		}
		c.Labels = append(c.Labels, p.Path)
		c.Datasets[0].Data = append(c.Datasets[0].Data, p.Hits)
	}
	if c.Labels == nil {
		c.Labels = []string{}
	}
	v.typeM = pivot(rangeDays(rg), byType, schemaLabel)
	v.Charts = map[string]chartSpec{
		"top_pages": c,
		"by_type":   v.typeM.chart("bar", true),
	}
	v.NeverByType = []typeCount{}
	for _, n := range neverTypes {
		label := otherPages
		if n.SchemaSlug != nil {
			label = schemaLabel(*n.SchemaSlug)
		}
		v.NeverByType = append(v.NeverByType, typeCount{SchemaSlug: n.SchemaSlug, Label: label, Count: n.Count})
	}
	return v, nil
}

// pagesCompare adds the previous period's fetches by page type to v, by the
// current chart's page types.
func (a *Admin) pagesCompare(ctx context.Context, rg agents.Range, v *pagesView) error {
	stats, ok := a.agentStats()
	if !ok {
		return errNoService
	}
	prg := previousRange(rg)
	pts, err := stats.FetchesBySchema(ctx, prg)
	if err != nil {
		return err
	}
	c := v.Charts["by_type"]
	v.Charts["by_type"] = c.withPrevious(prg, pivotFixed(rangeDays(prg), pts, schemaLabel, c.seriesLabels()).chart("bar", true))
	return nil
}

func (a *Admin) pagesJSON(ctx context.Context, q url.Values, rg agents.Range) (any, int, string) {
	v, err := a.pagesData(ctx, rg, atoiMin(q.Get("page"), 1), atoiMin(q.Get("npage"), 1))
	if err == nil && parseCompare(q) {
		err = a.pagesCompare(ctx, rg, v)
	}
	return a.result(v, err, "pages")
}

func atoiMin(s string, min int) int {
	n, err := strconv.Atoi(s)
	if err != nil || n < min {
		return min
	}
	if n > 10000 {
		return 10000
	}
	return n
}

type firstCrawlJSON struct {
	EntityID    string `json:"entity_id"`
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Agent       string `json:"agent"`
	FirstAt     string `json:"first_crawled_at"`
	PublishedAt string `json:"published_at,omitempty"`
	After       string `json:"after_publish,omitempty"`
	// AfterSeconds is After as a number; null when the publish time is unknown or after the crawl.
	AfterSeconds *int64 `json:"after_publish_seconds"`
}

type firstCrawlBucket struct {
	Label string `json:"label"`
	Count int    `json:"count"`
}

type firstCrawlAgent struct {
	ID    string `json:"agent_id"`
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type firstCrawlsView struct {
	From  string           `json:"from"`
	To    string           `json:"to"`
	Items []firstCrawlJSON `json:"items"`
	Total int              `json:"total"`
	// Buckets and ByAgent cover every first crawl in the range, not the page of Items.
	Buckets []firstCrawlBucket   `json:"buckets"`
	ByAgent []firstCrawlAgent    `json:"by_agent"`
	Charts  map[string]chartSpec `json:"charts"`
	table   ui.DataTable
}

// firstCrawlsChunk is the page size the first-crawls chart reads the whole range with.
const firstCrawlsChunk = 5000

func humanDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "under a minute"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + " min"
	case d < 48*time.Hour:
		return strconv.Itoa(int(d.Hours())) + " h"
	}
	return strconv.Itoa(int(d.Hours()/24)) + " days"
}

func (a *Admin) firstCrawlsData(ctx context.Context, rg agents.Range, page int) (*firstCrawlsView, error) {
	stats, ok := a.agentStats()
	if !ok {
		return nil, errNoService
	}
	rows, total, err := stats.FirstCrawls(ctx, rg, agents.Page{Limit: analyticsPerPage, Offset: (page - 1) * analyticsPerPage})
	if err != nil {
		return nil, err
	}
	v := &firstCrawlsView{From: rg.From.Format(dayLayout), To: rg.To.Format(dayLayout), Total: total, Items: []firstCrawlJSON{}}
	for _, f := range rows {
		it := firstCrawlJSON{EntityID: f.EntityID.String(), Name: f.Name, Slug: f.Slug, Agent: a.agentName(f.AgentID), FirstAt: fmtDateTime(f.FirstAt.UTC()) + " UTC"}
		if f.PublishedAt != nil {
			it.PublishedAt = fmtDateTime(f.PublishedAt.UTC()) + " UTC"
			if !f.FirstAt.Before(*f.PublishedAt) {
				gap := f.FirstAt.Sub(*f.PublishedAt)
				it.After = humanDuration(gap)
				secs := int64(gap / time.Second)
				it.AfterSeconds = &secs
			}
		}
		v.Items = append(v.Items, it)
	}
	agg, err := stats.FirstCrawlAggregates(ctx, rg)
	if err != nil {
		return nil, err
	}
	v.Buckets = []firstCrawlBucket{{"Same day", agg.SameDay}, {"1 to 3 days", agg.D1to3}, {"4 to 7 days", agg.D4to7}, {"8 days or more", agg.D8Plus}}
	if agg.NotRecorded > 0 {
		v.Buckets = append(v.Buckets, firstCrawlBucket{"Not recorded", agg.NotRecorded})
	}
	v.ByAgent = []firstCrawlAgent{}
	for _, c := range agg.ByAgent {
		v.ByAgent = append(v.ByAgent, firstCrawlAgent{ID: c.AgentID, Name: a.agentName(c.AgentID), Count: c.Count})
	}
	sort.SliceStable(v.ByAgent, func(i, j int) bool {
		if v.ByAgent[i].Count != v.ByAgent[j].Count {
			return v.ByAgent[i].Count > v.ByAgent[j].Count
		}
		return v.ByAgent[i].Name < v.ByAgent[j].Name
	})
	// The chart counts first crawls per day over the whole range, not just the page shown.
	days := rangeDays(rg)
	per := map[string]int64{}
	for off := 0; off < total; off += firstCrawlsChunk {
		chunk, _, err := stats.FirstCrawls(ctx, rg, agents.Page{Limit: firstCrawlsChunk, Offset: off})
		if err != nil {
			return nil, err
		}
		for _, f := range chunk {
			per[f.FirstAt.UTC().Format(dayLayout)]++
		}
		if len(chunk) < firstCrawlsChunk {
			break
		}
	}
	m := matrix{Days: days, Cols: []string{"Pages first crawled"}, Vals: map[string][]int64{"Pages first crawled": dayCounts(days, per)}}
	v.Charts = map[string]chartSpec{"per_day": m.chart("bar", false)}
	v.table = m.table("Pages first crawled by an AI agent, per day", "Day")
	return v, nil
}

func (a *Admin) firstCrawlsJSON(ctx context.Context, q url.Values, rg agents.Range) (any, int, string) {
	v, err := a.firstCrawlsData(ctx, rg, atoiMin(q.Get("page"), 1))
	return a.result(v, err, "first crawls")
}

type referralsView struct {
	From       string               `json:"from"`
	To         string               `json:"to"`
	Charts     map[string]chartSpec `json:"charts"`
	Assistants []assistantTotal     `json:"assistants"`
	Pageviews  int64                `json:"pageviews"`
	Visits     int64                `json:"ai_referred_visits"`
	// Previous is the previous period's totals; set only when compare is on.
	Previous  *referralsPrevious `json:"previous,omitempty"`
	refs, pvs ui.DataTable
	refM, pvM matrix
	points    []agents.SeriesPoint
}

type referralsPrevious struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Totals struct {
		AIVisits  int64 `json:"ai_referred_visits"`
		Pageviews int64 `json:"pageviews"`
	} `json:"totals"`
}

type assistantTotal struct {
	Assistant string `json:"assistant"`
	Visits    int64  `json:"visits"`
}

func (a *Admin) referralsData(ctx context.Context, rg agents.Range) (*referralsView, error) {
	ref := a.deps.Growth.Referrals
	if ref == nil {
		return nil, errNoService
	}
	refs, err := ref.AIReferrals(ctx, rg.From, rg.To)
	if err != nil {
		return nil, err
	}
	views, err := ref.Pageviews(ctx, rg.From, rg.To)
	if err != nil {
		return nil, err
	}
	days := rangeDays(rg)
	pts := make([]agents.SeriesPoint, 0, len(refs))
	byAssistant := map[string]int64{}
	var visits int64
	for _, r := range refs {
		pts = append(pts, agents.SeriesPoint{Day: r.Day, Key: r.Assistant, Hits: r.Visits})
		byAssistant[r.Assistant] += r.Visits
		visits += r.Visits
	}
	rm := pivot(days, pts, assistantName)
	pv := map[string]int64{}
	var viewsTotal int64
	for _, d := range views {
		pv[d.Day] = d.Views
		viewsTotal += d.Views
	}
	vm := matrix{Days: days, Cols: []string{"Pageviews"}, Vals: map[string][]int64{"Pageviews": dayCounts(days, pv)}}
	v := &referralsView{From: rg.From.Format(dayLayout), To: rg.To.Format(dayLayout), Pageviews: viewsTotal, Visits: visits,
		Charts: map[string]chartSpec{"referrals": rm.chart("bar", true), "pageviews": vm.chart("area", false)},
		refs:   rm.table("AI-referred visits per day by assistant", "Day"), pvs: vm.table("Pageviews per day", "Day"), refM: rm, pvM: vm, points: pts, Assistants: []assistantTotal{}}
	for k, n := range byAssistant {
		v.Assistants = append(v.Assistants, assistantTotal{k, n})
	}
	sort.Slice(v.Assistants, func(i, j int) bool {
		if v.Assistants[i].Visits != v.Assistants[j].Visits {
			return v.Assistants[i].Visits > v.Assistants[j].Visits
		}
		return v.Assistants[i].Assistant < v.Assistants[j].Assistant
	})
	return v, nil
}

func (a *Admin) referralsJSON(ctx context.Context, q url.Values, rg agents.Range) (any, int, string) {
	v, err := a.referralsData(ctx, rg)
	if err == nil && parseCompare(q) {
		err = a.referralsCompare(ctx, rg, v)
	}
	return a.result(v, err, "referrals")
}

// referralsCompare adds the previous period to v: its totals, and to each chart
// the same series over the previous days.
func (a *Admin) referralsCompare(ctx context.Context, rg agents.Range, v *referralsView) error {
	prg := previousRange(rg)
	prev, err := a.referralsData(ctx, prg)
	if err != nil {
		return err
	}
	pdays := rangeDays(prg)
	refs := v.Charts["referrals"]
	v.Charts["referrals"] = refs.withPrevious(prg, pivotFixed(pdays, prev.points, assistantName, refs.seriesLabels()).chart("bar", true))
	v.Charts["pageviews"] = v.Charts["pageviews"].withPrevious(prg, prev.Charts["pageviews"])
	v.Previous = &referralsPrevious{From: prg.From.Format(dayLayout), To: prg.To.Format(dayLayout)}
	v.Previous.Totals.AIVisits, v.Previous.Totals.Pageviews = prev.Visits, prev.Pageviews
	return nil
}

// ---- pages ----

// createRedirectHref is the new-redirect form prefilled with a missing page; empty
// unless the page is gone (404 or 410), the only failures a redirect fixes.
func (a *Admin) createRedirectHref(path string, status int) string {
	if status != http.StatusNotFound && status != http.StatusGone {
		return ""
	}
	return a.adminPath + "/settings/redirects/new?" + url.Values{"source": {path}}.Encode()
}

type errorJSON struct {
	Path           string   `json:"path"`
	Status         int      `json:"status"`
	Hits           int64    `json:"hits"`
	Agents         []string `json:"agents"`
	LastSeenDay    string   `json:"last_seen_day"`
	CreateRedirect string   `json:"create_redirect,omitempty"`
}

type errorsView struct {
	From  string      `json:"from"`
	To    string      `json:"to"`
	Items []errorJSON `json:"items"`
	Total int         `json:"total"`
	Page  int         `json:"page"`
}

// errorsJSON lists failing paths (status 400 and above, every crawler), 25 a page.
func (a *Admin) errorsJSON(ctx context.Context, q url.Values, rg agents.Range) (any, int, string) {
	stats, ok := a.agentStats()
	if !ok {
		return a.result(nil, errNoService, "errors")
	}
	page := atoiMin(q.Get("page"), 1)
	rows, total, err := stats.Errors(ctx, rg, agents.Page{Limit: analyticsPerPage, Offset: (page - 1) * analyticsPerPage})
	if err != nil {
		return a.result(nil, err, "errors")
	}
	v := &errorsView{From: rg.From.Format(dayLayout), To: rg.To.Format(dayLayout), Items: []errorJSON{}, Total: total, Page: page}
	for _, e := range rows {
		names := []string{}
		for _, id := range e.AgentIDs {
			names = append(names, a.agentName(id))
		}
		v.Items = append(v.Items, errorJSON{Path: e.Path, Status: e.Status, Hits: e.Hits, Agents: names, LastSeenDay: e.LastDay, CreateRedirect: a.createRedirectHref(e.Path, e.Status)})
	}
	return v, http.StatusOK, ""
}
