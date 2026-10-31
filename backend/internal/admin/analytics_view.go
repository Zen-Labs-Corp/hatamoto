package admin

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/agents"
)

// sectionCopy is the heading and lede of each AI analytics section, from the
// approved prototype. A lede that quotes live numbers is empty until its
// section builds one.
var sectionCopy = map[string]struct{ Title, Lede string }{
	"ai-overview":  {"Are AI systems reading your site?", ""},
	"ai-crawlers":  {"Who is reading your site?", ""},
	"ai-content":   {"What are they reading?", ""},
	"ai-google":    {"What is Google doing?", googleLede},
	"ai-referrals": {"What comes back?", "Visits from people who clicked a link inside an AI assistant. Counted per day; no visitors are tracked."},
	"ai-health":    {"Is it running cleanly?", "Whether crawlers are who they say they are and whether your site answers them cleanly."},
}

// sectionPage is the data of every AI analytics section template.
type sectionPage struct {
	Title, Lede string
	Range       ui.DateRange
	RangeErr    string
	Compare     bool         // the previous period is shown too
	Prev        agents.Range // that period, whether or not compare is on
	Err         string
	Body        any // the section view model, nil until each section builds one
}

// sectionBuilder loads the body of one section for the range; cmp is true when
// the previous period is wanted too.
type sectionBuilder func(ctx context.Context, q url.Values, rg agents.Range, cmp bool) (any, error)

// analyticsSection serves one section page. A rejected range answers 422 with
// the default range, like the JSON endpoints answer 400.
func (a *Admin) analyticsSection(id string, build sectionBuilder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		rg, msg := parseRange(q)
		cmp := compareFor(rg, parseCompare(q))
		status := http.StatusOK
		if msg != "" {
			status = http.StatusUnprocessableEntity
		}
		c := sectionCopy[id]
		d := &sectionPage{Title: c.Title, Lede: c.Lede, RangeErr: msg, Compare: cmp.On, Prev: cmp.Prev,
			Range: a.dateRangeFor(r.URL.Path, rg, map[string]string{}, &cmp)}
		body, err := build(r.Context(), q, rg, cmp.On)
		switch {
		case errors.Is(err, errNoService):
			a.unavailable(w, r, "Analytics")
			return
		case err != nil:
			a.logger().Error("analytics section failed", "section", id, "err", err)
			d.Err = "This part of the dashboard could not be loaded. Try again in a moment."
			if status == http.StatusOK {
				status = http.StatusInternalServerError
			}
		}
		d.Body = body
		if l, ok := body.(interface{ lede() string }); ok {
			d.Lede = l.lede()
		}
		v := a.NewView(w, r, sectionLabel(id))
		v.AreaCarry = analyticsViewCarry(q)
		v.Data = d
		a.Render(w, r, status, "analytics/"+id[len("ai-"):], v)
	}
}

// analyticsViewCarry is what the sub-sidebar links keep from the request: the
// range and compare=1, so moving between sections does not reset them.
func analyticsViewCarry(q url.Values) url.Values {
	carry := analyticsCarry(q)
	if !parseCompare(q) {
		carry.Del("compare")
	}
	return carry
}

func sectionLabel(id string) string {
	for _, sd := range analyticsRegistry {
		if sd.ID == id {
			return sd.Label
		}
	}
	return id
}

// ---- helpers shared by the section builders ----

// rangeWords is the range as a phrase for a lede ("in the last 30 days"): the
// preset's words when rg is one, else "in this period".
func rangeWords(rg agents.Range) string {
	words := map[string]string{"Today": "today", "Last 7 days": "in the last 7 days", "Last 30 days": "in the last 30 days",
		"Last 3 months": "in the last 3 months", "Last 6 months": "in the last 6 months", "Month to date": "so far this month", "Year to date": "so far this year"}
	for _, p := range rangePresets(today()) {
		if p.From.Equal(rg.From) && p.To.Equal(rg.To) {
			return words[p.Label]
		}
	}
	return "in this period"
}

// compareWords is what a KPI delta is measured against.
func compareWords(rg agents.Range) string {
	if n := len(rangeDays(rg)); n > 1 {
		return "vs previous " + strconv.Itoa(n) + " days"
	}
	return "vs yesterday"
}

// per is the chart bucket of a range: "day", or "week" past maxDailyDays.
func per(rg agents.Range) string {
	if len(rangeDays(rg)) > maxDailyDays {
		return "week"
	}
	return "day"
}

// fixed is v with d decimals and a percent sign ("3.8%").
func fixed(v float64, d int) string { return strconv.FormatFloat(v, 'f', d, 64) + "%" }

// ratio is a of b as a percentage; zero when b is.
func ratio(a, b int64) float64 {
	if b <= 0 {
		return 0
	}
	return float64(a) / float64(b) * 100
}

// shareText is a share for a bar list: one decimal below 10%, whole above.
func shareText(v float64) string {
	if v < 10 {
		return fixed(v, 1)
	}
	return fixed(v, 0)
}

// kpiDelta is the change of cur against prev as a percentage, or in points
// when pts. It is nil unless compare is on. A change under 0.05 is flat. A
// previous value of 0 with a current above it is "New", not a percentage.
// lowerBetter flips the tone.
// liveSpark is the sparkline of a KPI card: the page script draws it from the chart of a data
// endpoint, in series colour tone (1 to 6). id names the svg gradient and is unique on the page.
func liveSpark(id string, tone int, l ui.SparkLive) *ui.Spark {
	return &ui.Spark{ID: id, Kind: "series-" + strconv.Itoa(tone), Live: &l}
}

func kpiDelta(on bool, rg agents.Range, cur, prev float64, pts, lowerBetter bool) *ui.StatChange {
	if !on {
		return nil
	}
	if !pts && prev == 0 && cur > 0 {
		return &ui.StatChange{Dir: "flat", Text: "New", Tone: "neutral", Caption: compareWords(rg)}
	}
	d := cur - prev
	if !pts {
		d = 0
		if prev != 0 {
			d = (cur - prev) / prev * 100
		}
	}
	c := &ui.StatChange{Caption: compareWords(rg)}
	switch {
	case d > -0.05 && d < 0.05:
		c.Dir, c.Text, c.Tone = "flat", "Unchanged", "neutral"
		return c
	case d > 0:
		c.Dir, c.Tone = "up", "success"
	default:
		c.Dir, c.Tone = "down", "danger"
	}
	sign := ""
	if d > 0 {
		sign = "+"
	}
	c.Text = sign + strconv.FormatFloat(d, 'f', 1, 64) + "%"
	if pts {
		c.Text = sign + strconv.FormatFloat(d, 'f', 1, 64) + " pts"
	}
	if lowerBetter {
		return c.LowerIsBetter()
	}
	return c
}

// dataURL is a JSON endpoint of the analytics data group for rg, with compare=1
// when the page compares (the charts draw the previous period from it).
func (a *Admin) dataURL(endpoint string, rg agents.Range, cmp bool) string {
	q := url.Values{"from": {rg.From.Format(dayLayout)}, "to": {rg.To.Format(dayLayout)}}
	if cmp {
		q.Set("compare", "1")
	}
	return a.adminPath + "/analytics/data/" + endpoint + "?" + q.Encode()
}

// assistantNames are the display names of the assistant keys the referral
// rollup stores.
var assistantNames = map[string]string{"chatgpt": "ChatGPT", "perplexity": "Perplexity", "claude": "Claude", "gemini": "Gemini",
	"copilot": "Copilot", "grok": "Grok", "meta_ai": "Meta AI", "deepseek": "DeepSeek", "mistral": "Mistral"}

// assistantName is the display name of an assistant key; an unknown key that
// is already written with capitals stays as it is, else it is title-cased.
func assistantName(key string) string {
	if n, ok := assistantNames[strings.ToLower(key)]; ok {
		return n
	}
	if key != strings.ToLower(key) {
		return key
	}
	words := strings.Fields(strings.NewReplacer("_", " ", "-", " ").Replace(key))
	for i, w := range words {
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}

// sectionHref is a section path with the range and compare switch carried.
func sectionHref(target string, q url.Values) string { return withCarry(target, analyticsViewCarry(q)) }

// publishedCoverage is how many published pages have been fetched at least
// once, and how many are published; the state today, whatever the range.
func (a *Admin) publishedCoverage(ctx context.Context) (never, published int, err error) {
	stats, ok := a.agentStats()
	if !ok {
		return 0, 0, errNoService
	}
	if published, err = stats.PublishedPages(ctx); err != nil {
		return 0, 0, err
	}
	_, never, err = stats.NeverFetched(ctx, agents.Page{Limit: 1})
	return never, published, err
}

// dailySums adds the columns of a daily matrix into one count per day.
func dailySums(m matrix) []int64 {
	out := make([]int64, len(m.Days))
	for _, c := range m.Cols {
		for i, v := range m.Vals[c] {
			out[i] += v
		}
	}
	return out
}

// withoutTotal is a time series table without its Total column and total cell,
// for columns that measure different things and must not be added up.
func withoutTotal(t ui.DataTable) ui.DataTable {
	if len(t.Columns) < 2 {
		return t
	}
	t.Columns = t.Columns[:len(t.Columns)-1]
	rows := make([]ui.DataRow, len(t.Rows))
	for i, r := range t.Rows {
		r.Cells = r.Cells[:len(r.Cells)-1]
		rows[i] = r
	}
	t.Rows = rows
	if t.Total != nil {
		tot := *t.Total
		tot.Cells = tot.Cells[:len(tot.Cells)-1]
		t.Total = &tot
	}
	return t
}
