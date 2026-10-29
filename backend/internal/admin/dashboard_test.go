package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/admin/a11y"
	"hatamoto.dev/core/internal/agents"
	"hatamoto.dev/core/internal/analytics"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/form"
	"hatamoto.dev/core/internal/redirect"
)

func TestDashboardRunnerIsolatesFailures(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	ok := false
	start := time.Now()
	errs := g.a.runDash(ctx, []func(context.Context) error{
		func(context.Context) error { panic("boom") },
		func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() },
		func(context.Context) error { ok = true; return nil },
		func(context.Context) error { return errors.New("store down") },
		func(context.Context) error { time.Sleep(time.Second); return nil }, // ignores ctx
	})
	if took := time.Since(start); took > 600*time.Millisecond {
		t.Errorf("runner took %v, want about the 150ms timeout", took)
	}
	if errs[0] == nil || errs[1] == nil || errs[3] == nil || errs[4] == nil {
		t.Errorf("failing fns must report an error: %v", errs)
	}
	if errs[2] != nil || !ok {
		t.Errorf("the healthy fn was affected: %v ok=%v", errs[2], ok)
	}
}

func TestDashboardContentStats(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	block := c.ents.addSchema(9, "Block", entity.BlockSchemaSlug, `{"type":"object"}`, `{}`, true)
	c.ents.addEntity(90, block, "Hidden block", "hidden-block", entity.StatusPublished, `{}`, t0)
	st, err := c.a.dashContent(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(st) != 2 || st[0].Label != "Published" || st[0].Value != "2" || st[1].Label != "Drafts" || st[1].Value != "1" {
		t.Fatalf("stats %+v (blocks and archived are not counted)", st)
	}
}

func TestDashboardContentFailureShowsNA(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	c.ents.countErr = errors.New("db down")
	v := c.a.buildDash(context.Background(), []dashJob{*c.a.contentJob()})
	if len(v.Stats) != 2 || v.Stats[0].Value != "n/a" || v.Stats[1].Value != "n/a" || v.Stats[0].Label != "Published" {
		t.Fatalf("stats %+v", v.Stats)
	}
	if len(v.Sections) != 0 {
		t.Fatalf("a KPI-only job adds no card: %+v", v.Sections)
	}
}

func TestDashboardNoEntityStoreSkipsContent(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	if g.a.contentJob() != nil {
		t.Fatal("content job without an entity store")
	}
}

func TestDashboardSubmissions(t *testing.T) {
	t.Parallel()

	g, ff, _ := newFormRig(t)
	contact := ff.addForm("Contact", contactFields...)
	quote := ff.addForm("Quote", contactFields...)
	now := time.Now()
	ff.addSub(contact, form.SubmissionStatusNew, now.Add(-1*time.Hour), map[string]any{"name": "Ada"})
	ff.addSub(quote, form.SubmissionStatusNew, now.Add(-2*time.Hour), map[string]any{"name": "Lee"})
	ff.addSub(contact, form.SubmissionStatusSpam, now.Add(-30*time.Minute), map[string]any{"name": "Spammer"})
	ff.addSub(contact, form.SubmissionStatusNew, now.Add(-30*24*time.Hour), map[string]any{"name": "Old"})
	for i := 0; i < 6; i++ {
		ff.addSub(quote, form.SubmissionStatusNew, now.Add(-time.Duration(3+i)*time.Hour), map[string]any{"name": "Bulk"})
	}
	res, err := g.a.dashSubmissions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Stats[0].Label != "New submissions" || res.Stats[0].Value != "8" {
		t.Errorf("stat %+v: spam and submissions older than 14 days are not counted", res.Stats[0])
	}
	if res.Sec.State != dashOK || len(res.Sec.Rows) != dashRecent {
		t.Fatalf("rows %d state %s, want %d", len(res.Sec.Rows), res.Sec.State, dashRecent)
	}
	if r := res.Sec.Rows[0]; r.Title != "Contact" || !strings.Contains(r.Summary, "Ada") || !strings.HasPrefix(r.Href, "/admin/settings/submissions/") {
		t.Errorf("newest row %+v", r)
	}
	for _, r := range res.Sec.Rows {
		if strings.Contains(r.Summary, "Spammer") {
			t.Errorf("spam listed: %+v", r)
		}
	}
}

// The two comparing cards say what they compare: last 7 days against the 7
// before them, from one 14-day read, drawn as two periods.
func TestDashboardSubmissionsCompareTheTwoWeeks(t *testing.T) {
	t.Parallel()

	g, ff, _ := newFormRig(t)
	contact := ff.addForm("Contact", contactFields...)
	now := time.Now()
	for i := 0; i < 9; i++ {
		ff.addSub(contact, form.SubmissionStatusNew, now.Add(-48*time.Hour), map[string]any{"name": "Recent"})
	}
	for i := 0; i < 6; i++ {
		ff.addSub(contact, form.SubmissionStatusNew, now.Add(-10*24*time.Hour), map[string]any{"name": "Earlier"})
	}
	res, err := g.a.dashSubmissions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	st := res.Stats[0]
	if st.Value != "9" || st.Change == nil || st.Change.Text != "+3 (+50.0%)" || st.Change.Caption != "vs previous 7 days (6)" {
		t.Errorf("stat %+v change %+v", st, st.Change)
	}
	if st.Spark == nil || st.Spark.PrevLine == "" || st.Spark.Divider == "" {
		t.Errorf("spark %+v is not split", st.Spark)
	}
}

func TestDashChange(t *testing.T) {
	t.Parallel()

	if c := dashChange(112, 93); c == nil || c.Text != "+19 (+20.4%)" || c.Caption != "vs previous 7 days (93)" {
		t.Errorf("referrals change %+v", c)
	}
	if c := dashChange(3, 0); c == nil || c.Text != "+3" || c.Caption != "vs previous 7 days (0)" {
		t.Errorf("from nothing %+v", c)
	}
	if dashChange(0, 0) != nil {
		t.Error("nothing to compare")
	}
}

// A spam burst fills the scan: the count is a lower bound and the list never
// claims there is nothing.
func TestDashboardSubmissionsSpamFillsScan(t *testing.T) {
	t.Parallel()

	g, ff, _ := newFormRig(t)
	contact := ff.addForm("Contact", contactFields...)
	now := time.Now()
	for i := 0; i < dashSubScan+50; i++ {
		ff.addSub(contact, form.SubmissionStatusSpam, now.Add(-time.Duration(i)*time.Second), map[string]any{"name": "Bot"})
	}
	ff.addSub(contact, form.SubmissionStatusNew, now.Add(-3*24*time.Hour), map[string]any{"name": "Real"})
	res, err := g.a.dashSubmissions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Stats[0].Value != "0+" || !strings.Contains(res.Stats[0].Note, "At least") || res.Stats[0].Change != nil {
		t.Errorf("stat %+v: a cut-off scan must read as a lower bound", res.Stats[0])
	}
	if res.Sec.State != dashEmpty || !strings.Contains(res.Sec.Empty.Title, "Too many") {
		t.Errorf("empty %+v: must not claim there are no submissions", res.Sec)
	}
}

func TestDashboardSubmissionsCapsForms(t *testing.T) {
	t.Parallel()

	g, ff, _ := newFormRig(t)
	for i := 0; i < dashFormCap+5; i++ {
		ff.addForm(fmt.Sprintf("Form %d", i), contactFields...)
	}
	if _, err := g.a.dashSubmissions(context.Background()); err != nil {
		t.Fatal(err)
	}
	if ff.listCalls != dashFormCap {
		t.Errorf("scanned %d forms, want %d", ff.listCalls, dashFormCap)
	}
	res, _ := g.a.dashSubmissions(context.Background())
	if !strings.HasSuffix(res.Stats[0].Value, "+") {
		t.Errorf("value %q: forms past the cap are left out, so it is a lower bound", res.Stats[0].Value)
	}
}

func TestDashboardSubmissionsEmptyAndError(t *testing.T) {
	t.Parallel()

	g, ff, _ := newFormRig(t)
	ff.addForm("Contact", contactFields...)
	res, err := g.a.dashSubmissions(context.Background())
	if err != nil || res.Sec.State != dashEmpty || res.Sec.Empty.Title == "" || res.Stats[0].Value != "0" {
		t.Fatalf("empty: %+v %v", res, err)
	}
	failing := &failingForms{fakeForms: ff}
	g.a.deps.Growth.Forms = failing
	v := g.a.buildDash(context.Background(), []dashJob{*g.a.submissionsJob()})
	if len(v.Sections) != 1 || v.Sections[0].State != dashError || v.Sections[0].ErrMsg == "" || v.Stats[0].Value != "n/a" {
		t.Fatalf("error card: %+v %+v", v.Sections, v.Stats)
	}
}

// failingForms fails every submission read.
type failingForms struct{ *fakeForms }

func (failingForms) ListSubmissions(context.Context, uuid.UUID, *form.ListSubmissionsParams) ([]*form.FormSubmission, int, error) {
	return nil, 0, errors.New("db down")
}

func TestDashboardMissingPages(t *testing.T) {
	t.Parallel()

	fr := newFakeRedirects()
	now := time.Now()
	add := func(path string, hits int, seen time.Time, mutate func(*redirect.NotFoundEntry)) *redirect.NotFoundEntry {
		e := &redirect.NotFoundEntry{ID: uuid.New(), Path: path, HitCount: hits, LastSeenAt: seen}
		if mutate != nil {
			mutate(e)
		}
		fr.log[e.ID] = e
		return e
	}
	open := add("/old-pricing", 31, now.Add(-time.Hour), nil)
	add("/stale", 99, now.Add(-30*24*time.Hour), nil)
	add("/ignored", 40, now, func(e *redirect.NotFoundEntry) { e.Ignored = true })
	add("/handled", 40, now, func(e *redirect.NotFoundEntry) { e.RedirectID = ptr(uuid.New()) })
	g := newRig(t, func(d *Deps) { d.Growth.Redirects = fr })
	res, err := g.a.dashMissing(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Stats[0].Label != "Missing pages" || res.Stats[0].Value != "1" {
		t.Errorf("stat %+v: stale, ignored and redirected rows are not counted", res.Stats[0])
	}
	items := res.Sec.Bars.Items
	if res.Sec.State != dashOK || len(items) != 1 || items[0].Label != "/old-pricing" {
		t.Fatalf("items %+v", items)
	}
	if h := items[0].Href; !strings.Contains(h, "source=%2Fold-pricing") || !strings.Contains(h, "log="+open.ID.String()) || !strings.HasPrefix(h, "/admin/settings/redirects/new?") {
		t.Errorf("href %q", h)
	}
	if items[0].Value != "31 hits" {
		t.Errorf("value %q", items[0].Value)
	}
}

// An established site: more than a page of old, resolved, high-hit entries must
// not hide a 404 that started this week.
func TestDashboardMissingPagesEstablishedLog(t *testing.T) {
	t.Parallel()

	fr := newFakeRedirects()
	now := analyticsNow()
	for i := range 60 {
		e := &redirect.NotFoundEntry{ID: uuid.New(), Path: fmt.Sprintf("/old-%d", i), HitCount: 500 + i, LastSeenAt: now.Add(-time.Hour)}
		if i%2 == 0 {
			e.Ignored = true
		} else {
			e.RedirectID = ptr(uuid.New())
		}
		fr.log[e.ID] = e
	}
	fresh := &redirect.NotFoundEntry{ID: uuid.New(), Path: "/pricing-2026", HitCount: 3, LastSeenAt: now.Add(-time.Hour)}
	fr.log[fresh.ID] = fresh
	g := newRig(t, func(d *Deps) { d.Growth.Redirects = fr })
	res, err := g.a.dashMissing(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Sec.State != dashOK || len(res.Sec.Bars.Items) != 1 || res.Sec.Bars.Items[0].Label != "/pricing-2026" || res.Stats[0].Value != "1" {
		t.Fatalf("new 404 hidden: %+v %+v", res.Sec.Bars.Items, res.Stats)
	}
}

// More open entries than the bound show as "50+", never an exact-looking count.
func TestDashboardMissingPagesCapped(t *testing.T) {
	t.Parallel()

	fr := newFakeRedirects()
	for i := range dash404Page + 5 {
		e := &redirect.NotFoundEntry{ID: uuid.New(), Path: fmt.Sprintf("/m-%d", i), HitCount: i + 1, LastSeenAt: analyticsNow()}
		fr.log[e.ID] = e
	}
	g := newRig(t, func(d *Deps) { d.Growth.Redirects = fr })
	res, err := g.a.dashMissing(context.Background())
	if err != nil || res.Stats[0].Value != "50+" || len(res.Sec.Bars.Items) != notFoundTop || res.Sec.Bars.Items[0].Label != fmt.Sprintf("/m-%d", dash404Page+4) {
		t.Fatalf("%+v %+v %v", res.Stats, res.Sec.Bars.Items, err)
	}
}

func TestDashboardMissingPagesEmptyAndError(t *testing.T) {
	t.Parallel()

	fr := newFakeRedirects()
	g := newRig(t, func(d *Deps) { d.Growth.Redirects = fr })
	res, err := g.a.dashMissing(context.Background())
	if err != nil || res.Sec.State != dashEmpty || res.Stats[0].Value != "0" {
		t.Fatalf("empty: %+v %v", res, err)
	}
	g.a.deps.Growth.Redirects = failingRedirects{fr}
	v := g.a.buildDash(context.Background(), []dashJob{*g.a.missingJob()})
	if len(v.Sections) != 1 || v.Sections[0].State != dashError || v.Stats[0].Value != "n/a" {
		t.Fatalf("error card: %+v %+v", v.Sections, v.Stats)
	}
}

// failingRedirects fails the 404 log read.
type failingRedirects struct{ *fakeRedirects }

func (failingRedirects) ListRecent404Entries(context.Context, time.Time, int) ([]*redirect.NotFoundEntry, error) {
	return nil, errors.New("db down")
}

func TestDashboardReferrals(t *testing.T) {
	t.Parallel()

	g, _ := newAnalyticsRig(t)
	res, err := g.a.dashReferrals(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Stats) != 1 || res.Stats[0].Label != "AI-referred visits" || res.Stats[0].Value != "12" {
		t.Fatalf("stats %+v", res.Stats)
	}
	if res.Stats[0].Change == nil || res.Stats[0].Spark == nil {
		t.Errorf("AI-referred visits lacks its change or spark: %+v", res.Stats[0])
	} else if c := res.Stats[0].Change; !strings.HasPrefix(c.Caption, "vs previous 7 days (") || res.Stats[0].Spark.Divider == "" {
		t.Errorf("AI-referred visits caption %q or spark not split", c.Caption)
	}
	if res.Sec != nil {
		t.Errorf("referrals add no card")
	}
}

func TestDashboardTopPages(t *testing.T) {
	t.Parallel()

	g, _ := newAnalyticsRig(t)
	res, err := g.a.dashTopPages(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Sec.State != dashOK || len(res.Sec.Bars.Items) != 1 || res.Sec.Bars.Items[0].Label != "/about" || res.Sec.Bars.Items[0].Value != "40" {
		t.Fatalf("section %+v", res.Sec)
	}
}

func TestDashboardTopPagesEmptyAndError(t *testing.T) {
	t.Parallel()

	g := newRig(t, func(d *Deps) { d.Growth.Agents = emptyPages{&fakeAgentStats{}} })
	res, err := g.a.dashTopPages(context.Background())
	if err != nil || res.Sec.State != dashEmpty || res.Sec.Empty.Title == "" {
		t.Fatalf("empty: %+v %v", res, err)
	}
	g.a.deps.Growth.Agents = emptyPages{&fakeAgentStats{}}
	g.a.deps.Growth.Referrals = failingReferrals{}
	v := g.a.buildDash(context.Background(), []dashJob{*g.a.referralsJob()})
	if len(v.Stats) != 1 || v.Stats[0].Value != "n/a" {
		t.Fatalf("error stats %+v", v.Stats)
	}
	g.a.deps.Growth.Agents = failingPages{&fakeAgentStats{}}
	v = g.a.buildDash(context.Background(), []dashJob{*g.a.topPagesJob()})
	if len(v.Sections) != 1 || v.Sections[0].State != dashError || v.Sections[0].ErrMsg == "" {
		t.Fatalf("error card %+v", v.Sections)
	}
}

func TestDashboardAnalyticsOmittedWhenNotWired(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	if g.a.referralsJob() != nil || g.a.topPagesJob() != nil {
		t.Fatal("analytics jobs without Referrals or Agents")
	}
}

type emptyPages struct{ *fakeAgentStats }

func (emptyPages) TopPages(context.Context, agents.Range, agents.Page) ([]agents.PageRow, int, error) {
	return nil, 0, nil
}

type failingPages struct{ *fakeAgentStats }

func (failingPages) TopPages(context.Context, agents.Range, agents.Page) ([]agents.PageRow, int, error) {
	return nil, 0, errors.New("db down")
}

type failingReferrals struct{}

func (failingReferrals) AIReferrals(context.Context, time.Time, time.Time) ([]analytics.ReferralDay, error) {
	return nil, errors.New("db down")
}
func (failingReferrals) Pageviews(context.Context, time.Time, time.Time) ([]analytics.DayCount, error) {
	return nil, errors.New("db down")
}

// spyRedirects records that the 404 log was read.
type spyRedirects struct {
	*fakeRedirects
	reads *atomic.Int32
}

func (s spyRedirects) ListRecent404Entries(ctx context.Context, since time.Time, limit int) ([]*redirect.NotFoundEntry, error) {
	s.reads.Add(1)
	return s.fakeRedirects.ListRecent404Entries(ctx, since, limit)
}

type dashRig struct {
	*rig
	redirectReads *atomic.Int32
	updates       *fakeUpdates
}

// newDashRig wires every store the Overview reads, with data in each.
func newDashRig(t *testing.T) *dashRig {
	t.Helper()
	c := newContentRig(t)
	ff := newFakeForms()
	contact := ff.addForm("Contact", contactFields...)
	ff.addSub(contact, form.SubmissionStatusNew, time.Now().Add(-time.Hour), map[string]any{"name": "Ada"})
	fr := newFakeRedirects()
	fr.log[fid(1)] = &redirect.NotFoundEntry{ID: fid(1), Path: "/old-pricing", HitCount: 31, LastSeenAt: time.Now()}
	reads := &atomic.Int32{}
	fu := &fakeUpdates{state: UpdatesState{Running: "1.4.2", History: []UpdatesHistoryRow{{When: t0, To: "1.4.2", Phase: "success"}}}}
	fs := &fakeAgentStats{}
	g := newRig(t, func(d *Deps) {
		d.Content = ContentDeps{Entities: c.ents, Terms: c.terms, Media: c.media, Authors: c.authors}
		d.Growth.Forms, d.Growth.Redirects, d.Growth.Agents, d.Growth.Referrals = ff, spyRedirects{fr, reads}, fs, fakeReferrals{}
		d.Updates = fu
	})
	return &dashRig{rig: g, redirectReads: reads, updates: fu}
}

func TestDashboardOwnerPage(t *testing.T) {
	t.Parallel()

	g := newDashRig(t)
	rec := g.do("GET", "/admin", "owner-token", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"<h1>Overview</h1>", "Published", "Drafts", "New submissions", "AI-referred visits", "Missing pages",
		"Pages AI crawlers fetch most", "Recent submissions", `class="hm-colophon"`,
		`href="/admin/settings/redirects/new?log=` + fid(1).String() + `&amp;source=%2Fold-pricing"`} {
		if !strings.Contains(body, want) {
			t.Errorf("owner page lacks %q", want)
		}
	}
	if strings.Contains(body, "Pageviews") {
		t.Error("the KPI row is five cards: no Pageviews")
	}
	if strings.Contains(body, "dash-updates") || strings.Contains(body, "Update history, latest releases") || strings.Contains(body, "hm-updchip") {
		t.Error("the overview has no Updates card and the top bar no update chip")
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
}

func TestDashboardEditorSeesContentOnly(t *testing.T) {
	t.Parallel()

	g := newDashRig(t)
	rec := g.do("GET", "/admin", "editor-token", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"Published", "Drafts", "New submissions", "Recent submissions"} {
		if !strings.Contains(body, want) {
			t.Errorf("editor page lacks %q", want)
		}
	}
	for _, banned := range []string{"Missing pages", "old-pricing", "AI-referred", "Pageviews", "Pages AI crawlers fetch most", "Update history", "Updates", "1.4.2"} {
		if strings.Contains(body, banned) {
			t.Errorf("editor page contains owner data %q", banned)
		}
	}
	if n := g.redirectReads.Load(); n != 0 {
		t.Errorf("the 404 log was read %d times for an editor", n)
	}
}

func TestDashboardUnknownRoleKeepsLanding(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	r := httptest.NewRequest("GET", "/admin", nil)
	r = r.WithContext(context.WithValue(r.Context(), userKey, &CurrentUser{Role: "viewer"}))
	w := httptest.NewRecorder()
	g.a.handleDashboard(w, r)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != landingPath {
		t.Fatalf("%d %q", w.Code, w.Header().Get("Location"))
	}
}

func TestDashboardCardFailuresAreIndependent(t *testing.T) {
	t.Parallel()

	g := newDashRig(t)
	g.a.deps.Growth.Agents = failingPages{&fakeAgentStats{}}
	body := g.do("GET", "/admin", "owner-token", nil, nil).Body.String()
	if strings.Count(body, "Couldn&#39;t load this card.") != 1 || !strings.Contains(body, "The crawler statistics could not be read.") {
		t.Errorf("want exactly the crawler card in error")
	}
	if strings.Contains(body, "db down") {
		t.Error("the cause must not reach the page")
	}
	if !strings.Contains(body, "Recent submissions") || !strings.Contains(body, "old-pricing") {
		t.Error("the other cards must still render")
	}
}

func TestDashboardPanickingAndSlowCardsDoNotBreakTheRest(t *testing.T) {
	t.Parallel()

	g := newDashRig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	jobs := g.a.dashJobs("owner")
	jobs = append(jobs,
		dashJob{Name: "panics", SecPos: 9, Fallback: dashSection{ID: "p", Title: "Panics", ErrMsg: "It panicked."}, Run: func(context.Context) (dashResult, error) { panic("boom") }},
		dashJob{Name: "slow", SecPos: 10, Fallback: dashSection{ID: "s", Title: "Slow", ErrMsg: "It was slow."}, Run: func(ctx context.Context) (dashResult, error) { <-ctx.Done(); return dashResult{}, ctx.Err() }})
	v := g.a.buildDash(ctx, jobs)
	states := map[string]string{}
	for _, s := range v.Sections {
		states[s.ID] = s.State
	}
	if states["p"] != dashError || states["s"] != dashError || states["dash-submissions"] != dashOK || states["dash-missing"] != dashOK {
		t.Errorf("states %v", states)
	}
}

// A job that returns after the deadline must not race the page build (run with -race).
func TestDashboardLateJobDoesNotRaceBuild(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	finished := make(chan struct{})
	job := dashJob{Name: "late", SecPos: 1, Fallback: dashSection{ID: "late", Title: "Late", ErrMsg: "Too slow."},
		Run: func(ctx context.Context) (dashResult, error) {
			<-ctx.Done()
			time.Sleep(50 * time.Millisecond) // still running when the page is built
			defer close(finished)
			return dashResult{Sec: &dashSection{ID: "late", State: dashOK}}, nil
		}}
	v := g.a.buildDash(ctx, []dashJob{job})
	<-finished
	if len(v.Sections) != 1 || v.Sections[0].State != dashError {
		t.Fatalf("a late job must show its error card: %+v", v.Sections)
	}
}

func TestDashboardEmptyAndErrorGoldens(t *testing.T) {
	t.Parallel()

	g := newRig(t, func(d *Deps) {
		d.Growth.Forms, d.Growth.Redirects, d.Growth.Agents, d.Growth.Referrals = newFakeForms(), newFakeRedirects(), emptyPages{&fakeAgentStats{}}, emptyReferrals{}
	})
	body := g.do("GET", "/admin", "owner-token", nil, nil).Body.String()
	for _, want := range []string{"No recent submissions", "No missing pages this week", "No AI crawler fetches yet"} {
		if !strings.Contains(body, want) {
			t.Errorf("empty page lacks %q", want)
		}
	}
}

type emptyReferrals struct{}

func (emptyReferrals) AIReferrals(context.Context, time.Time, time.Time) ([]analytics.ReferralDay, error) {
	return nil, nil
}
func (emptyReferrals) Pageviews(context.Context, time.Time, time.Time) ([]analytics.DayCount, error) {
	return nil, nil
}

// The Overview's a11y check is TestDashboardOwnerPage.
func init() { areaPages["dashboard"] = []string{"dashboard/index"} }

type goldenStores struct {
	content   *contentRig
	forms     FormStore
	redirects RedirectStore
	agents    AgentStats
	referrals Referrals
	updates   UpdatesService
	audit     AuditReader
}

// dashGoldenFixture is a site with data in every card, at a pinned clock.
func dashGoldenFixture(t *testing.T) goldenStores {
	t.Helper()
	stamp := time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)
	analyticsNow = func() time.Time { return stamp }
	t.Cleanup(func() { analyticsNow = time.Now })
	ff := newFakeForms()
	contact := ff.addForm("Contact", contactFields...)
	delete(ff.forms, contact.ID)
	contact.ID = fixedID(3)
	ff.forms[contact.ID] = contact
	for i, name := range []string{"Ada", "Lee", "Kim"} {
		s := ff.addSub(contact, form.SubmissionStatusNew, stamp.Add(-time.Duration(i+1)*24*time.Hour), map[string]any{"name": name, "email": strings.ToLower(name) + "@example.com"})
		delete(ff.subs, s.ID)
		s.ID = fixedID(4 + i)
		ff.subs[s.ID] = s
	}
	fr := newFakeRedirects()
	fr.log[fid(1)] = &redirect.NotFoundEntry{ID: fid(1), Path: "/old-pricing", HitCount: 31, LastSeenAt: stamp.Add(-time.Hour)}
	fr.log[fid(2)] = &redirect.NotFoundEntry{ID: fid(2), Path: "/about-us.html", HitCount: 12, LastSeenAt: stamp.Add(-48 * time.Hour)}
	var hist []UpdatesHistoryRow
	for i, phase := range []string{"success", "success", "failed", "success"} {
		hist = append(hist, UpdatesHistoryRow{When: stamp.AddDate(0, 0, i-10), To: fmt.Sprintf("1.4.%d", i), Phase: phase})
	}
	audit := &fakeAuditLog{rows: []AuditRow{
		{ID: 3, At: stamp.Add(-2 * time.Hour), Actor: "owner@example.com", Action: "entity.publish", Target: "id-1", Detail: map[string]any{"name": "Pricing"}},
		{ID: 2, At: stamp.Add(-30 * time.Hour), Actor: "ed@example.com", Action: "entity.update", Target: "id-2", Detail: map[string]any{"name": "About"}},
		{ID: 1, At: stamp.Add(-9 * 24 * time.Hour), Actor: "owner@example.com", Action: "cache.clear", Target: "page-cache"},
	}}
	return goldenStores{content: newContentRig(t), forms: ff, redirects: fr, agents: &fakeAgentStats{}, referrals: fakeReferrals{},
		updates: &fakeUpdates{state: UpdatesState{Running: "1.4.2", History: hist}}, audit: audit}
}

func (s goldenStores) rig(t *testing.T) *rig {
	t.Helper()
	return newRig(t, func(d *Deps) {
		c := s.content
		d.Content = ContentDeps{Entities: c.ents, Terms: c.terms, Media: c.media, Authors: c.authors}
		d.Growth.Forms, d.Growth.Redirects, d.Growth.Agents, d.Growth.Referrals = s.forms, s.redirects, s.agents, s.referrals
		d.Updates = s.updates
		if s.audit != nil {
			d.AuditLog = s.audit
		}
	})
}

// TestDashboardGoldens pins the Overview for an owner, an editor, a site with
// no data, a site where every store fails, and one without analytics.
func TestDashboardGoldens(t *testing.T) {
	full := dashGoldenFixture(t)
	empty := goldenStores{content: &contentRig{ents: newFakeEntities(), terms: newFakeTerms(), media: newFakeMedia(), authors: full.content.authors},
		forms: newFakeForms(), redirects: newFakeRedirects(), agents: emptyPages{&fakeAgentStats{}}, referrals: emptyReferrals{}, updates: &fakeUpdates{}}
	failing := dashGoldenFixture(t)
	failing.content.ents.countErr = errors.New("db down")
	failing.forms, failing.redirects = failingForms{failing.forms.(*fakeForms)}, failingRedirects{failing.redirects.(*fakeRedirects)}
	failing.agents, failing.referrals = failingPages{&fakeAgentStats{}}, failingReferrals{}
	failing.updates = &fakeUpdates{viewErr: errors.New("down")}
	noAnalytics := dashGoldenFixture(t)
	noAnalytics.agents, noAnalytics.referrals = nil, nil
	for _, tc := range []struct {
		name, token string
		s           goldenStores
	}{
		{"owner", "owner-token", full},
		{"editor", "editor-token", full},
		{"empty", "owner-token", empty},
		{"error", "owner-token", failing},
		{"no-analytics", "owner-token", noAnalytics},
	} {
		g := tc.s.rig(t)
		rec := g.do("GET", "/admin", tc.token, nil, nil)
		if rec.Code != 200 {
			t.Fatalf("%s: %d", tc.name, rec.Code)
		}
		body := normalizePage(rec.Body.String())
		compareGolden(t, "pages/dashboard-"+tc.name+".html", body)
		if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
			t.Errorf("%s a11y: %s", tc.name, strings.Join(issues, "; "))
		}
		if tc.name == "no-analytics" && (strings.Contains(body, "AI-referred") || strings.Contains(body, "Pages AI crawlers fetch most")) {
			t.Error("analytics cards must be omitted, not errored")
		}
		if tc.name == "no-analytics" && strings.Contains(body, "Couldn&#39;t load") {
			t.Error("omitted cards are not errors")
		}
	}
}

// KPI notes and links render inside the stat card, and a failed card shows an
// error with a Reload link inside the KPI row.
func TestDashboardKPINotesAreVisible(t *testing.T) {
	t.Parallel()

	g := newDashRig(t)
	body := g.do("GET", "/admin", "owner-token", nil, nil).Body.String()
	for _, want := range []string{"Last 7 days, spam left out.", "Not redirected or ignored, last 7 days.", "Pages and posts that are live."} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks KPI note %q", want)
		}
	}
	v := g.a.buildDash(context.Background(), []dashJob{{Name: "x", StatPos: 1, Labels: []string{"Published"}, Run: func(context.Context) (dashResult, error) { return dashResult{}, errors.New("db down") }}})
	if len(v.Stats) != 1 || !v.Stats[0].Failed || v.Stats[0].Value != "n/a" {
		t.Fatalf("failed KPI %+v", v.Stats)
	}
	failing := newRig(t, func(d *Deps) { d.Growth.Redirects = failingRedirects{newFakeRedirects()} })
	page := failing.do("GET", "/admin", "owner-token", nil, nil).Body.String()
	kpis, _, _ := strings.Cut(page, `<div class="hm-dash hm-dash--`)
	if !strings.Contains(kpis, "Couldn&#39;t load.") || !strings.Contains(kpis, "Reload the page") {
		t.Errorf("failed KPI shows no error note with a Reload link")
	}
}

func TestDashboardContentKPILinks(t *testing.T) {
	t.Parallel()

	g := newDashRig(t)
	body := g.do("GET", "/admin", "owner-token", nil, nil).Body.String()
	for _, want := range []string{`href="/admin/entities?status=published">View pages`, `href="/admin/entities?status=draft">Review`} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks link %q", want)
		}
	}
}

// manyPages has 30 pages and honours Page.Limit like the store, so a dropped bound shows.
type manyPages struct{ *fakeAgentStats }

func (manyPages) TopPages(_ context.Context, _ agents.Range, p agents.Page) ([]agents.PageRow, int, error) {
	var rows []agents.PageRow
	for i := range 30 {
		rows = append(rows, agents.PageRow{Path: fmt.Sprintf("/p-%d", i), Hits: int64(100 - i)})
	}
	if p.Limit > 0 {
		rows = rows[:min(p.Limit, len(rows))]
	}
	return rows, 30, nil
}

func TestDashboardTopPagesAreBounded(t *testing.T) {
	t.Parallel()

	g := newRig(t, func(d *Deps) { d.Growth.Agents = manyPages{&fakeAgentStats{}} })
	res, err := g.a.dashTopPages(context.Background())
	if err != nil || len(res.Sec.Bars.Items) != dashTopPages {
		t.Fatalf("%d bars, want %d (%v)", len(res.Sec.Bars.Items), dashTopPages, err)
	}
}

func TestDashboardGridLayout(t *testing.T) {
	t.Parallel()

	g := newDashRig(t)
	owner := g.do("GET", "/admin", "owner-token", nil, nil).Body.String()
	last := -1
	for _, id := range []string{"dash-health", "dash-pages", "dash-missing", "dash-submissions", "dash-activity"} {
		i := strings.Index(owner, `id="`+id+`"`)
		if i < 0 || i < last {
			t.Errorf("owner section %s is missing or out of order (at %d, previous %d)", id, i, last)
		}
		last = i
	}
	editor := g.do("GET", "/admin", "editor-token", nil, nil).Body.String()
	for _, id := range []string{"dash-health", "dash-activity"} {
		if strings.Contains(editor, id) {
			t.Errorf("editor page has owner card %s", id)
		}
	}
	css, err := os.ReadFile("../../web/admin/css/pages/dashboard.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"repeat(4, minmax(0, 1fr))", "#dash-health { grid-row: span 2; }", ".hm-card:only-child { grid-column: 1 / -1; }", "max-width: 1279px", "max-width: 1099px"} {
		if !strings.Contains(string(css), want) {
			t.Errorf("dashboard.css lacks %q", want)
		}
	}
}

// The shared scan runs under the page build's context, not under the deadline
// of whichever job (the 3.5s health card) asks first.
func TestDashboardSharedScanIgnoresTheFirstCallersDeadline(t *testing.T) {
	t.Parallel()

	g, ff, _ := newFormRig(t)
	ff.addForm("Contact", contactFields...)
	g.a.deps.Growth.Forms = ctxForms{ff}
	var scanErr error
	job := dashJob{Name: "x", StatPos: 1, Labels: []string{"Published"}, Run: func(ctx context.Context) (dashResult, error) {
		short, cancel := context.WithCancel(ctx)
		cancel()
		_, scanErr = g.a.scanSubmissions(short)
		return dashResult{}, nil
	}}
	g.a.buildDash(context.Background(), []dashJob{job})
	if scanErr != nil {
		t.Fatalf("scan under a cancelled caller context: %v", scanErr)
	}
}

// ctxForms honours cancellation of the listing context.
type ctxForms struct{ *fakeForms }

func (c ctxForms) ListForms(ctx context.Context, q string) ([]*form.Form, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return c.fakeForms.ListForms(ctx, q)
}

// A cut-off scan draws the plain spark: the split one would set a truncated
// earlier week against this one, the comparison the card refuses to state.
func TestDashboardPartialScanUsesThePlainSpark(t *testing.T) {
	t.Parallel()

	g, ff, _ := newFormRig(t)
	contact := ff.addForm("Contact", contactFields...)
	now := time.Now()
	ff.addSub(contact, form.SubmissionStatusNew, now.Add(-24*time.Hour), map[string]any{"name": "Ada"})
	ff.addSub(contact, form.SubmissionStatusNew, now.Add(-10*24*time.Hour), map[string]any{"name": "Lee"})
	for i := 0; i < dashFormCap; i++ {
		ff.addForm(fmt.Sprintf("Form %d", i), contactFields...)
	}
	res, err := g.a.dashSubmissions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	st := res.Stats[0]
	if !strings.HasSuffix(st.Value, "+") || st.Spark == nil {
		t.Fatalf("stat %+v: want a partial scan with a spark", st)
	}
	if st.Spark.PrevLine != "" || st.Spark.Divider != "" {
		t.Errorf("partial scan drew the split spark: %+v", st.Spark)
	}
}
