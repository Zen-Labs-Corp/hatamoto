package admin

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/agents"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/form"
)

// The Overview page (GET /admin): a bounded, one-request summary built from
// the stores the other screens already use. Every card is built on its own, so
// a failing store turns only its card into an error card.
//
// Bounds (nothing here is an unbounded read):
//   - entity counts: one grouped query;
//   - forms: at most dashFormCap, each scanned for at most dashSubScan rows of
//     the last 14 days (worst case dashFormCap*dashSubScan = 4,000 rows);
//   - missing pages: one query, at most dash404Page+1 rows seen this week;
//   - top pages: dashTopPages rows; referrals and pageviews: a 14-day window;
//   - recent submissions: dashRecent.
const (
	dashTimeout    = 4 * time.Second
	dashWindowDays = 7
	dashFormCap    = 20
	dashSubScan    = 200
	dash404Page    = 50
	dashTopPages   = 8
	dashRecent     = 5
)

// Card states.
const (
	dashOK    = "ok"
	dashEmpty = "empty"
	dashError = "error"
)

// dashRow is one line of the recent submissions list.
type dashRow struct {
	Title, Summary, When, Href string
	Unread                     bool // still "new": shows the dot
}

// dashContact is what identifies the sender on one line: the email answer, or
// the first answer when the form has no email field.
func dashContact(f *form.Form, sub *form.FormSubmission) string {
	first := ""
	for _, ff := range f.Fields {
		v := formatAnswer(sub.Data[ff.Name])
		if v == "" {
			continue
		}
		if ff.Type == form.FieldTypeEmail {
			return truncate(v, 40)
		}
		if first == "" {
			first = truncate(v, 40)
		}
	}
	return first
}

// dashSection is one card below the KPI row.
type dashSection struct {
	ID, Title, Description string
	State                  string // ok | empty | error
	Empty                  ui.Empty
	Bars                   ui.BarList
	Tracker                *ui.Tracker
	Rows                   []dashRow // recent submissions
	Health                 []healthGroup
	Activity               []activityRow
	More                   *ui.Button // link to the full screen, last in the card (only Label and Href are used)
	ErrMsg                 string
}

// dashKPI is one KPI card. Failed marks a card that could not load: the page
// then shows an error note with a Reload link under it.
type dashKPI struct {
	ui.Stat
	Failed bool
}

// dashView is the page model: the KPI row and the cards in display order.
type dashView struct {
	Stats    []dashKPI
	Sections []*dashSection
}

// Left and Right are the two independent columns of the card grid, each in
// display order: Site health and Activity on the left, the analytics and
// content cards on the right. A column with no card is not rendered, so an
// editor (no health, no activity) gets one full-width column.
func (v *dashView) Left() []*dashSection { return v.pick("dash-health", "dash-activity") }

func (v *dashView) Right() []*dashSection {
	return v.pick("dash-pages", "dash-submissions", "dash-missing")
}

// OneColumn reports that only one of the two columns has cards.
func (v *dashView) OneColumn() bool { return (len(v.Left()) == 0) != (len(v.Right()) == 0) }

func (v *dashView) pick(ids ...string) []*dashSection {
	var out []*dashSection
	for _, id := range ids {
		for _, s := range v.Sections {
			if s.ID == id {
				out = append(out, s)
			}
		}
	}
	return out
}

// runDash runs fns in parallel under ctx and returns one error per fn: nil on
// success, the returned error, a recovered panic, or ctx's error when the fn
// had not finished by the deadline. A fn that returns late is ignored, so the
// caller must read what an fn produced only when its error is nil.
func (a *Admin) runDash(ctx context.Context, fns []func(context.Context) error) []error {
	errs := make([]error, len(fns))
	done := make([]chan struct{}, len(fns))
	for i, fn := range fns {
		done[i] = make(chan struct{})
		go func() {
			defer close(done[i])
			defer func() {
				if p := recover(); p != nil {
					errs[i] = fmt.Errorf("panic: %v", p)
				}
			}()
			errs[i] = fn(ctx)
		}()
	}
	out := make([]error, len(fns))
	for i := range fns {
		select {
		case <-done[i]:
			out[i] = errs[i]
		case <-ctx.Done():
			select {
			case <-done[i]:
				out[i] = errs[i]
			default:
				out[i] = ctx.Err()
			}
		}
	}
	return out
}

// dashResult is what one job produced: KPI cards, a card, or both.
type dashResult struct {
	Stats []ui.Stat
	Sec   *dashSection
}

// dashJob builds one part of the page. StatPos and SecPos order its KPI cards
// and its card (0 means it has none). Labels and Fallback describe what shows
// when the job fails: its KPI cards as "n/a", its card as an error card.
type dashJob struct {
	Name     string
	StatPos  int
	SecPos   int
	Labels   []string
	Fallback dashSection
	Run      func(ctx context.Context) (dashResult, error)
}

// failedStats are the KPI cards of a job that could not load.
func failedStats(labels []string) []ui.Stat {
	out := make([]ui.Stat, len(labels))
	for i, l := range labels {
		out[i] = ui.Stat{Label: l, Value: "n/a"}
	}
	return out
}

// buildDash runs the jobs in parallel and assembles the page. A failed job
// shows n/a KPI cards and an error card; the cause is only logged.
func (a *Admin) buildDash(ctx context.Context, jobs []dashJob) *dashView {
	ctx = context.WithValue(ctx, dashScanKey{}, &dashScanMemo{ctx: ctx})
	res := make([]dashResult, len(jobs))
	fns := make([]func(context.Context) error, len(jobs))
	for i, j := range jobs {
		fns[i] = func(ctx context.Context) error {
			r, err := j.Run(ctx)
			res[i] = r
			return err
		}
	}
	errs := a.runDash(ctx, fns)
	type placed struct {
		pos int
		sec *dashSection
	}
	type placedStats struct {
		pos   int
		stats []dashKPI
	}
	var secs []placed
	var stats []placedStats
	for i, j := range jobs {
		// res[i] is only safe to read when the job finished without error:
		// a timed-out job may still be writing it.
		var r dashResult
		failed := false
		if errs[i] == nil {
			r = res[i]
		} else {
			a.logger().Error("admin: dashboard card failed", "card", j.Name, "err", errs[i])
			fb := j.Fallback
			fb.State = dashError
			r = dashResult{Sec: &fb}
			failed = true
			if j.SecPos == 0 {
				r.Sec = nil
			}
		}
		if j.StatPos > 0 {
			ks := make([]dashKPI, 0, len(r.Stats))
			if failed {
				for _, s := range failedStats(j.Labels) {
					ks = append(ks, dashKPI{Stat: s, Failed: true})
				}
			} else {
				for _, s := range r.Stats {
					ks = append(ks, dashKPI{Stat: s})
				}
			}
			stats = append(stats, placedStats{j.StatPos, ks})
		}
		if j.SecPos > 0 && r.Sec != nil {
			secs = append(secs, placed{j.SecPos, r.Sec})
		}
	}
	sort.SliceStable(stats, func(i, k int) bool { return stats[i].pos < stats[k].pos })
	sort.SliceStable(secs, func(i, k int) bool { return secs[i].pos < secs[k].pos })
	v := &dashView{}
	for _, s := range stats {
		v.Stats = append(v.Stats, s.stats...)
	}
	for _, s := range secs {
		v.Sections = append(v.Sections, s.sec)
	}
	return v
}

// dashContent is the Published and Drafts cards, from the one grouped count
// the entity list uses. Block and layout part types are left out, as there.
func (a *Admin) dashContent(ctx context.Context) ([]ui.Stat, error) {
	es := a.deps.Content.Entities
	schemas, err := es.ListSchemas(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := es.CountEntitiesBySchemaStatus(ctx)
	if err != nil {
		return nil, err
	}
	hidden := map[uuid.UUID]bool{}
	for _, id := range reuseSchemaIDs(schemas) {
		hidden[id] = true
	}
	var published, drafts int64
	for _, r := range rows {
		if hidden[r.SchemaID] {
			continue
		}
		switch r.Status {
		case entity.StatusPublished:
			published += int64(r.Count)
		case entity.StatusDraft:
			drafts += int64(r.Count)
		}
	}
	return []ui.Stat{
		{Label: "Published", Value: fmtNum(published), Note: "Pages and posts that are live.", Link: a.adminPath + "/entities?status=published", LinkLabel: "View pages"},
		{Label: "Drafts", Value: fmtNum(drafts), Note: "Waiting to be published.", Link: a.adminPath + "/entities?status=draft", LinkLabel: "Review"},
	}, nil
}

// contentJob is the content KPI job; nil when the site has no entity store.
func (a *Admin) contentJob() *dashJob {
	if a.deps.Content.Entities == nil {
		return nil
	}
	return &dashJob{Name: "content", StatPos: 1, Labels: []string{"Published", "Drafts"},
		Run: func(ctx context.Context) (dashResult, error) {
			st, err := a.dashContent(ctx)
			return dashResult{Stats: st}, err
		}}
}

// The clock is analyticsNow, so tests and goldens can pin it.

// dashSubmissionDays is the window the submissions card reads: this week and
// the one before it, for the change figure.
const dashSubmissionDays = 2 * dashWindowDays

// dashScan is what one bounded read of the forms' recent submissions found.
// The submissions card and the health card both need it, so a page build reads
// it once (see dashScanMemo).
type dashScan struct {
	forms []*form.Form
	subs  []*form.FormSubmission // spam left out
	// spam is the spam of the last 7 days that the scan passed.
	spam []*form.FormSubmission
	// partial is true when the figures are a lower bound: forms past the cap
	// are not read, and a form's scan stops at dashSubScan rows (spam counts).
	partial bool
	now     time.Time
	loc     *time.Location
}

// dashScanMemo shares one dashScan between the jobs of one page build.
type dashScanMemo struct {
	ctx  context.Context // the page build's context: the read outlives the first caller's deadline
	once sync.Once
	scan dashScan
	err  error
}

type dashScanKey struct{}

// scanSubmissions is the scan of the last 14 days, read once per page build
// when ctx carries a dashScanMemo (buildDash adds one).
func (a *Admin) scanSubmissions(ctx context.Context) (dashScan, error) {
	if m, ok := ctx.Value(dashScanKey{}).(*dashScanMemo); ok {
		m.once.Do(func() { m.scan, m.err = a.readSubmissions(m.ctx) })
		return m.scan, m.err
	}
	return a.readSubmissions(ctx)
}

func (a *Admin) readSubmissions(ctx context.Context) (dashScan, error) {
	fs := a.deps.Growth.Forms
	forms, err := fs.ListForms(ctx, "")
	if err != nil {
		return dashScan{}, err
	}
	sc := dashScan{partial: len(forms) > dashFormCap}
	if sc.partial {
		forms = forms[:dashFormCap]
	}
	sc.forms = forms
	sc.loc = a.siteLocation(ctx)
	sc.now = analyticsNow().In(sc.loc)
	since := sc.now.AddDate(0, 0, -(dashSubmissionDays - 1))
	since = time.Date(since.Year(), since.Month(), since.Day(), 0, 0, 0, 0, sc.loc)
	week := since.AddDate(0, 0, dashWindowDays)
	for _, f := range forms {
		subs, spam, _, c, err := scanRecentSubmissions(ctx, fs, f.ID, since, dashSubScan)
		if err != nil {
			return dashScan{}, err
		}
		sc.subs = append(sc.subs, subs...)
		for _, s := range spam {
			if !s.CreatedAt.Before(week) {
				sc.spam = append(sc.spam, s)
			}
		}
		sc.partial = sc.partial || c
	}
	return sc, nil
}

// dashSubmissions is the New submissions card and the Recent submissions list,
// from one bounded scan per form (spam left out).
func (a *Admin) dashSubmissions(ctx context.Context) (dashResult, error) {
	sc, err := a.scanSubmissions(ctx)
	if err != nil {
		return dashResult{}, err
	}
	all, partial, now, loc := sc.subs, sc.partial, sc.now, sc.loc
	byForm := map[uuid.UUID]*form.Form{}
	for _, f := range sc.forms {
		byForm[f.ID] = f
	}
	days := submissionDays(all, now, dashSubmissionDays, loc)
	vals := make([]int64, len(days))
	var last7, prev7 int64
	for i, n := range days {
		vals[i] = int64(n)
		if i >= dashWindowDays {
			last7 += int64(n)
		} else {
			prev7 += int64(n)
		}
	}
	stat := ui.Stat{Label: "New submissions", Value: fmtNum(last7), Note: "Last 7 days, spam left out.", Spark: ui.SparkSplitOf(vals, dashWindowDays)}
	if partial {
		stat.Spark = ui.SparkOf(vals) // a cut-off older week is not drawn against this one
		stat.Value += "+"
		stat.Note = "At least this many in the last 7 days, spam left out."
	} else { // a cut-off older week would overstate the rise
		stat.Change = dashChange(last7, prev7)
	}
	sort.SliceStable(all, func(i, k int) bool { return all[i].CreatedAt.After(all[k].CreatedAt) })
	sec := &dashSection{ID: "dash-submissions", Title: "Recent submissions", Description: "The last 14 days, newest first.",
		State: dashOK, More: &ui.Button{Label: "All submissions", Href: a.adminPath + "/settings/submissions", Variant: "ghost"}}
	for _, s := range all {
		if len(sec.Rows) == dashRecent {
			break
		}
		f := byForm[s.FormID]
		sec.Rows = append(sec.Rows, dashRow{Title: f.Name, Summary: dashContact(f, s), Unread: s.Status == form.SubmissionStatusNew, When: fmtDateTime(s.CreatedAt.In(loc)),
			Href: a.adminPath + "/settings/submissions/" + s.ID.String()})
	}
	switch {
	case len(sec.Rows) == 0 && partial: // never claim "none" when the scan was cut off
		sec.State = dashEmpty
		sec.Empty = ui.Empty{Icon: "inbox", Title: "Too many recent submissions to list", Body: "Open All submissions to see them.", Compact: true}
	case len(sec.Rows) == 0:
		sec.State = dashEmpty
		sec.Empty = ui.Empty{Icon: "inbox", Title: "No recent submissions", Body: "Messages sent through your forms will show here.", Compact: true}
	}
	return dashResult{Stats: []ui.Stat{stat}, Sec: sec}, nil
}

// submissionsJob is the submissions job; nil when the site has no form store.
func (a *Admin) submissionsJob() *dashJob {
	if a.deps.Growth.Forms == nil {
		return nil
	}
	return &dashJob{Name: "submissions", StatPos: 2, SecPos: 4, Labels: []string{"New submissions"}, Run: a.dashSubmissions,
		Fallback: dashSection{ID: "dash-submissions", Title: "Recent submissions", Description: "The last 14 days, newest first.", ErrMsg: "The submissions could not be read."}}
}

// dashMissing is the Missing pages card and list: the open entries (not
// ignored, not redirected) last seen in the last 7 days, from one bounded
// query, so a new broken link is never hidden behind old high-hit rows.
// The bars and the count use that same window. One row past dash404Page is
// read to tell a full list ("50+") from an exact count.
func (a *Admin) dashMissing(ctx context.Context) (dashResult, error) {
	since := analyticsNow().AddDate(0, 0, -dashWindowDays)
	list, err := a.deps.Growth.Redirects.ListRecent404Entries(ctx, since, dash404Page+1)
	if err != nil {
		return dashResult{}, err
	}
	sort.SliceStable(list, func(i, k int) bool { return list[i].HitCount > list[k].HitCount })
	sec := &dashSection{ID: "dash-missing", Title: "Missing pages", Description: "Addresses visitors asked for that do not exist, seen in the last 7 days.", State: dashOK,
		Bars: ui.BarList{Label: "Missing pages seen in the last 7 days, most requested first, with all-time hit totals. Select an address to create a redirect for it", NameHeading: "Address (select to create a redirect)", ValueHeading: "All-time hits"},
		More: &ui.Button{Label: "All missing pages", Href: a.adminPath + "/settings/redirects/404", Variant: "ghost"}}
	for _, e := range list {
		if len(sec.Bars.Items) == notFoundTop {
			break
		}
		sec.Bars.Items = append(sec.Bars.Items, ui.BarItem{Label: e.Path, N: float64(e.HitCount), Value: fmt.Sprintf("%d %s", e.HitCount, plural(e.HitCount, "hit", "hits")),
			Href: a.adminPath + "/settings/redirects/new?" + url.Values{"source": {e.Path}, "log": {e.ID.String()}}.Encode()})
	}
	if len(list) == 0 {
		sec.State = dashEmpty
		sec.Empty = ui.Empty{Icon: "check", Title: "No missing pages this week", Body: "Addresses visitors asked for that did not exist will show here.", Compact: true}
	}
	value := fmtNum(int64(len(list)))
	if len(list) > dash404Page {
		value = fmtNum(dash404Page) + "+"
	}
	stat := ui.Stat{Label: "Missing pages", Value: value, Note: "Not redirected or ignored, last 7 days."}
	return dashResult{Stats: []ui.Stat{stat}, Sec: sec}, nil
}

// missingJob is the 404 job; nil when the site has no redirect store.
func (a *Admin) missingJob() *dashJob {
	if a.deps.Growth.Redirects == nil {
		return nil
	}
	return &dashJob{Name: "missing pages", StatPos: 4, SecPos: 3, Labels: []string{"Missing pages"}, Run: a.dashMissing,
		Fallback: dashSection{ID: "dash-missing", Title: "Missing pages", Description: "Addresses visitors asked for that do not exist, seen in the last 7 days.", ErrMsg: "The missing pages could not be read."}}
}

// dashReferrals is the AI-referred visits card: the last 7 days against the 7
// before, from one 14-day read of the daily rollup. (No Pageviews card: the
// KPI row is five cards, as in the mockup.)
func (a *Admin) dashReferrals(ctx context.Context) (dashResult, error) {
	ref := a.deps.Growth.Referrals
	end := today()
	rg := agents.Range{From: end.AddDate(0, 0, -(dashSubmissionDays - 1)), To: end}
	visits, err := ref.AIReferrals(ctx, rg.From, rg.To)
	if err != nil {
		return dashResult{}, err
	}
	ai := map[string]int64{}
	for _, d := range visits {
		ai[d.Day] += d.Visits
	}
	days := rangeDays(rg)
	card := func(label, detail string, vals []int64) ui.Stat {
		var cur, prev int64
		for i, n := range vals {
			if i >= dashWindowDays {
				cur += n
			} else {
				prev += n
			}
		}
		return ui.Stat{Label: label, Value: fmtNum(cur), Note: detail, Change: dashChange(cur, prev), Spark: ui.SparkSplitOf(vals, dashWindowDays)}
	}
	return dashResult{Stats: []ui.Stat{
		card("AI-referred visits", "Visits from AI assistants, last 7 days.", dayCounts(days, ai)),
	}}, nil
}

// referralsJob is the referrals job; nil when analytics is not wired.
func (a *Admin) referralsJob() *dashJob {
	if a.deps.Growth.Referrals == nil {
		return nil
	}
	return &dashJob{Name: "referrals", StatPos: 3, Labels: []string{"AI-referred visits"}, Run: a.dashReferrals}
}

// dashTopPages is the pages AI crawlers fetch most, over 30 days: the only
// per-page analytics read there is, so it is labelled as what it counts.
func (a *Admin) dashTopPages(ctx context.Context) (dashResult, error) {
	end := today()
	rows, _, err := a.deps.Growth.Agents.TopPages(ctx, agents.Range{From: end.AddDate(0, 0, -29), To: end}, agents.Page{Limit: dashTopPages})
	if err != nil {
		return dashResult{}, err
	}
	sec := &dashSection{ID: "dash-pages", Title: "Pages AI crawlers fetch most", Description: "Last 30 days.", State: dashOK,
		Bars: ui.BarList{Label: "Pages AI crawlers fetched most in the last 30 days", NameHeading: "Page", ValueHeading: "Fetches"},
		More: &ui.Button{Label: "All pages", Href: a.adminPath + "/analytics/crawlers", Variant: "ghost"}}
	for _, p := range rows {
		sec.Bars.Items = append(sec.Bars.Items, ui.BarItem{Label: p.Path, N: float64(p.Hits), Value: fmtNum(p.Hits)})
	}
	if len(rows) == 0 {
		sec.State = dashEmpty
		sec.Empty = ui.Empty{Icon: "bot", Title: "No AI crawler fetches yet", Body: "Pages that AI crawlers read will show here.", Compact: true}
	}
	return dashResult{Sec: sec}, nil
}

// topPagesJob is the top pages job; nil when crawler analytics is not wired.
func (a *Admin) topPagesJob() *dashJob {
	if a.deps.Growth.Agents == nil {
		return nil
	}
	return &dashJob{Name: "top pages", SecPos: 2, Run: a.dashTopPages,
		Fallback: dashSection{ID: "dash-pages", Title: "Pages AI crawlers fetch most", Description: "Last 30 days.", ErrMsg: "The crawler statistics could not be read."}}
}

// dashChange is the KPI delta of the last 7 days against the 7 before them,
// and says so: the caption names the period and its count.
func dashChange(cur, prev int64) *ui.StatChange {
	ch := ui.StatChangeOf(cur, prev, fmtNum)
	if ch != nil {
		ch.Caption = fmt.Sprintf("vs previous %d days (%s)", dashWindowDays, fmtNum(prev))
	}
	return ch
}

// dashJobs are the cards a role sees. Editors get content and submissions
// only: the owner-only stores (404 log, analytics) are never called
// for them.
func (a *Admin) dashJobs(role string) []dashJob {
	candidates := []*dashJob{a.contentJob(), a.submissionsJob()}
	if role == "owner" {
		candidates = append(candidates, a.healthJob(), a.referralsJob(), a.missingJob(), a.topPagesJob(), a.activityJob())
	}
	var jobs []dashJob
	for _, j := range candidates {
		if j != nil {
			jobs = append(jobs, *j)
		}
	}
	return jobs
}

// handleDashboard serves GET /admin. A role that is neither owner nor editor
// (not possible today) keeps the old behaviour: the landing screen.
func (a *Admin) handleDashboard(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	if u == nil || (u.Role != "owner" && u.Role != "editor") {
		http.Redirect(w, r, a.landing(), http.StatusSeeOther)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), dashTimeout)
	defer cancel()
	v := a.NewView(w, r, "Overview")
	v.Data = a.buildDash(ctx, a.dashJobs(u.Role))
	a.Render(w, r, http.StatusOK, "dashboard/index", v)
}
