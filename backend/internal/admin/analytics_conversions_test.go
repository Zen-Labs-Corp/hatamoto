package admin

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/admin/a11y"
	"hatamoto.dev/core/internal/agents"
	"hatamoto.dev/core/internal/analytics"
)

var fakeLeadID = uuid.MustParse("00000000-0000-4000-8000-0000000000aa")

// fakeConversions answers the leads reader with numbers that differ by touch, so a test can see
// which touch a page used. total is 12 under first touch and 9 under last touch.
type fakeConversions struct {
	empty bool
	err   error
}

func (f *fakeConversions) LeadsDaily(_ context.Context, touch analytics.TouchKind, from, _ time.Time) ([]analytics.LeadDay, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.empty {
		return []analytics.LeadDay{}, nil
	}
	ch := analytics.ChannelAI
	if touch == analytics.TouchLast {
		ch = analytics.ChannelEmail
	}
	return []analytics.LeadDay{{Day: from.Format(dayLayout), Channel: ch, Leads: 3}, {Day: from.Format(dayLayout), Channel: analytics.ChannelUnknown, Leads: 1}}, nil
}

func (f *fakeConversions) LeadsTop(_ context.Context, dim analytics.Dim, touch analytics.TouchKind, _, _ time.Time, _ int) ([]analytics.LeadRow, error) {
	if f.err != nil || f.empty {
		return nil, f.err
	}
	last := touch == analytics.TouchLast
	switch dim {
	case analytics.DimChannel:
		if last {
			return []analytics.LeadRow{{K1: analytics.ChannelEmail, Leads: 6}, {K1: analytics.ChannelUnknown, Leads: 3}}, nil
		}
		return []analytics.LeadRow{{K1: analytics.ChannelAI, Leads: 8}, {K1: analytics.ChannelOrganic, Leads: 2}, {K1: analytics.ChannelUnknown, Leads: 2}}, nil
	case analytics.DimSource:
		if last {
			return []analytics.LeadRow{{K1: "newsletter", K2: analytics.ChannelEmail, Leads: 6}}, nil
		}
		return []analytics.LeadRow{{K1: "chatgpt", K2: analytics.ChannelAI, Leads: 5}, {K1: "grok", K2: analytics.ChannelAI, Leads: 3}, {K1: "google", K2: analytics.ChannelOrganic, Leads: 2}}, nil
	}
	return nil, nil
}

func (f *fakeConversions) LeadsTotals(_ context.Context, touch analytics.TouchKind, _, _ time.Time) (int64, int64, error) {
	if f.err != nil {
		return 0, 0, f.err
	}
	if f.empty {
		return 0, 0, nil
	}
	if touch == analytics.TouchLast {
		return 9, 3, nil
	}
	return 12, 2, nil
}

func (f *fakeConversions) LeadsByForm(context.Context, time.Time, time.Time, int) ([]analytics.LeadForm, error) {
	if f.empty {
		return nil, nil
	}
	return []analytics.LeadForm{{FormID: fakeLeadID, Name: "Contact sales", Leads: 7}}, nil
}

func (f *fakeConversions) RecentLeads(context.Context, time.Time, time.Time, int) ([]analytics.Lead, error) {
	if f.empty {
		return nil, nil
	}
	at := time.Date(2026, time.January, 2, 9, 30, 0, 0, time.UTC)
	return []analytics.Lead{
		{ID: fakeLeadID, FormID: uuid.New(), FormName: "Contact sales", Email: "ada@example.com", At: at,
			First: &analytics.Touch{Channel: analytics.ChannelAI, Source: "chatgpt", Landing: "/pricing"},
			Last:  &analytics.Touch{Channel: analytics.ChannelEmail, Source: "newsletter", Campaign: "oct-news", Landing: "/blog"}},
		{ID: uuid.MustParse("00000000-0000-4000-8000-0000000000ab"), FormID: uuid.New(), FormName: "Newsletter", Email: "=cmd@example.com", At: at.Add(-time.Hour)},
	}, nil
}

func conversionsRig(t *testing.T, fc *fakeConversions, mutate ...func(*Deps)) *rig {
	t.Helper()
	rg, _ := parseRange(url.Values{})
	fs := &fakeAgentStats{extraAgents: []agents.AgentTotals{{AgentID: "bytespider", Operator: "ByteDance", Purpose: "training", Hits: 7}}}
	// A source of ai_assistants visits per operator: chatgpt is OpenAI's, grok has no operator.
	ft := &fakeAIVisits{fakeTraffic: fakeTraffic{from: rg.From}}
	return newRig(t, append([]func(*Deps){func(d *Deps) { d.Growth.Agents, d.Growth.Traffic, d.Growth.Conversions = fs, ft, fc }}, mutate...)...)
}

// fakeAIVisits adds assistant sources to the traffic fake.
type fakeAIVisits struct{ fakeTraffic }

func (f *fakeAIVisits) TrafficTop(ctx context.Context, dim analytics.Dim, from, to time.Time, limit int) ([]analytics.TrafficTop, error) {
	if dim == analytics.DimSource && !f.previous(from) {
		return []analytics.TrafficTop{{K1: "chatgpt", K2: analytics.ChannelAI, Counts: analytics.Counts{Visits: 20}},
			{K1: "grok", K2: analytics.ChannelAI, Counts: analytics.Counts{Visits: 4}}, {K1: "google", K2: analytics.ChannelOrganic, Counts: analytics.Counts{Visits: 60}}}, nil
	}
	return f.fakeTraffic.TrafficTop(ctx, dim, from, to, limit)
}

func TestConversionsPageSections(t *testing.T) {
	t.Parallel()

	g := conversionsRig(t, &fakeConversions{})
	rec := g.do("GET", "/admin/analytics/conversions", "owner-token", nil, nil)
	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	for _, want := range []string{"What turns into leads?", "Stored on the submission", "Leads", "Conv. rate", "Leads from AI assistants", "Best channel", "Top form", "Contact sales",
		"w-c-leads", "w-c-funnel", "w-c-givegets", "w-c-recent", `data-chart="leads"`, "conversions.json",
		"Leads by first channel", "Viewed 2&#43; pages", "Saw a form", "Started the form", "Submitted", "of previous",
		"OpenAI", "Other assistants", "takes only", "Block them per purpose on the Crawlers tab",
		"2 of these leads have no attribution", "ada@example.com", "/settings/submissions/" + fakeLeadID.String(), "Export CSV", "conversions/export.csv"} {
		if !strings.Contains(body, want) {
			t.Errorf("conversions page missing %q", want)
		}
	}
	if strings.Contains(body, "—") {
		t.Error("copy must not use em dashes")
	}
	if !strings.Contains(body, `name="touch" value="first" checked`) || !strings.Contains(body, `name="touch" value="last"`) {
		t.Error("the touch switch must offer both touches with the first checked")
	}
	if strings.Contains(body, "disabled aria-disabled") {
		t.Error("the touch switch must not be disabled")
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
}

func TestConversionsTouchLastSwitchesTheNumbers(t *testing.T) {
	t.Parallel()

	g := conversionsRig(t, &fakeConversions{})
	first := g.do("GET", "/admin/analytics/conversions", "owner-token", nil, nil).Body.String()
	last := g.do("GET", "/admin/analytics/conversions?touch=last", "owner-token", nil, nil).Body.String()
	if !strings.Contains(first, ">12<") || strings.Contains(first, "Leads by last channel") {
		t.Error("the first touch is the default: 12 leads")
	}
	for _, want := range []string{">9<", "Leads by last channel", `name="touch" value="last" checked`, "touch=last", "newsletter / oct-news"} {
		if !strings.Contains(last, want) {
			t.Errorf("touch=last page missing %q", want)
		}
	}
	if strings.Contains(last, ">12<") {
		t.Error("touch=last still shows the first touch total")
	}
	// The chart JSON and the nav carry the choice.
	rec := g.do("GET", "/admin/analytics/data/conversions.json?touch=last", "owner-token", nil, nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"touch":"last"`) || !strings.Contains(rec.Body.String(), "Email") {
		t.Errorf("conversions.json = %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(last, "analytics/traffic?") || !strings.Contains(last, "touch=last") {
		t.Error("the sub-sidebar must carry touch=last")
	}
}

// ISC-8 and ISC-9: pages fetched is the Crawlers tab's AI hits of the operator, visitors sent the
// Traffic tab's ai_assistants source rows mapped to it.
func TestConversionsGiveVsGetReconcilesWithCrawlersAndTraffic(t *testing.T) {
	t.Parallel()

	g := conversionsRig(t, &fakeConversions{})
	rg := trafficRange()
	cr, err := g.a.buildCrawlers(context.Background(), url.Values{}, rg, false)
	if err != nil {
		t.Fatal(err)
	}
	var crawlerHits int64
	for _, grp := range cr.(*crawlersPage).Agents.Groups {
		for _, r := range grp.Rows {
			if r.Operator == "OpenAI" {
				n, _ := strconv.ParseInt(strings.ReplaceAll(r.Fetches, ",", ""), 10, 64)
				crawlerHits += n
			}
		}
	}
	if crawlerHits == 0 {
		t.Fatal("the crawlers fake has no OpenAI hits")
	}
	body, err := g.a.buildConversions(context.Background(), url.Values{}, rg, false)
	if err != nil {
		t.Fatal(err)
	}
	var found, other bool
	for _, row := range body.(*conversionsPage).GiveGet.Rows {
		switch row[0].Text {
		case "OpenAI":
			found = true
			if row[1].Text != fmtNum(crawlerHits) {
				t.Errorf("OpenAI pages fetched = %s, the crawlers tab has %d", row[1].Text, crawlerHits)
			}
			if row[2].Text != "20" || row[4].Text != "5" || row[3].Text != "45.5" || row[5].Text != "25.0%" {
				t.Errorf("OpenAI visitors/leads = %v", row)
			}
			if row[0].Badge != nil {
				t.Error("an operator with visitors is not takes only")
			}
		case "Other assistants":
			other = true
			if row[1].Text != "0" || row[2].Text != "4" || row[4].Text != "3" || row[0].Badge != nil {
				t.Errorf("other assistants = %v", row)
			}
		}
	}
	if !found || !other {
		t.Errorf("rows missing: OpenAI %v, other assistants %v", found, other)
	}
}

// ISC-12: a lead without attribution shows as Unknown, in Recent leads and as the chart's last series.
func TestConversionsUnattributedLeadsAreUnknown(t *testing.T) {
	t.Parallel()

	g := conversionsRig(t, &fakeConversions{})
	body := g.do("GET", "/admin/analytics/conversions", "owner-token", nil, nil).Body.String()
	if !strings.Contains(body, ">Unknown<") {
		t.Error("the lead without attribution must read Unknown")
	}
	v, err := g.a.conversionsData(context.Background(), trafficRange(), false, analytics.TouchFirst)
	if err != nil {
		t.Fatal(err)
	}
	labels := v.Charts["leads"].seriesLabels()
	if len(labels) != 8 || labels[7] != "Unknown" {
		t.Errorf("series = %v, want 7 channels and Unknown", labels)
	}
}

// ISC-10: the submitter's address and the CSV need canViewSubmissions.
func TestConversionsEmailAndCSVNeedThePermission(t *testing.T) {
	t.Parallel()

	g := conversionsRig(t, &fakeConversions{})
	for _, token := range []string{"owner-token", "editor-token"} {
		rec := g.do("GET", "/admin/analytics/conversions/export.csv?from=2026-01-01&to=2026-01-03", token, nil, nil)
		csv := rec.Body.String()
		if rec.Code != 200 || !strings.Contains(csv, "ada@example.com") || !strings.Contains(csv, "'=cmd@example.com") || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/csv") {
			t.Errorf("%s export = %d %q", token, rec.Code, csv)
		}
		if !strings.Contains(csv, "When (UTC),Form,Channel,Source,Campaign,Landing page,Email") || !strings.Contains(csv, "AI assistants,chatgpt,,/pricing") {
			t.Errorf("%s export has the wrong columns: %q", token, csv)
		}
	}

	// No user, or a role the predicate does not know: no email, no export.
	for name, u := range map[string]*CurrentUser{"no user": nil, "unknown role": {Role: "viewer"}} {
		ctx := context.Background()
		if u != nil {
			ctx = context.WithValue(ctx, userKey, u)
		}
		if g.a.canViewSubmissions(ctx) {
			t.Errorf("%s: canViewSubmissions must be false", name)
		}
		r := httptest.NewRequest(http.MethodGet, "/admin/analytics/conversions/export.csv", nil).WithContext(ctx)
		w := httptest.NewRecorder()
		g.a.handleConversionsExport(w, r)
		if w.Code != http.StatusForbidden || strings.Contains(w.Body.String(), "ada@example.com") {
			t.Errorf("%s: export = %d", name, w.Code)
		}
		body, err := g.a.buildConversions(ctx, url.Values{}, trafficRange(), false)
		if err != nil {
			t.Fatal(err)
		}
		p := body.(*conversionsPage)
		if p.ExportHref != "" || len(p.Recent.Rows) != 2 || p.Recent.Rows[0][1].Text != "Hidden" || p.Recent.Rows[0][1].Href != "" {
			t.Errorf("%s: recent leads = %+v, export %q", name, p.Recent.Rows, p.ExportHref)
		}
		for _, row := range p.Recent.Rows {
			for _, c := range row {
				if strings.Contains(c.Text, "@") || strings.Contains(c.Href, "submissions") {
					t.Errorf("%s: row leaks the submitter: %+v", name, c)
				}
			}
		}
		if p.Recent.Rows[0][2].Text != "Contact sales" {
			t.Errorf("%s: the row must still show the form", name)
		}
	}
}

func TestConversionsWithoutLeadsOrReaders(t *testing.T) {
	t.Parallel()

	g := conversionsRig(t, &fakeConversions{empty: true})
	rec := g.do("GET", "/admin/analytics/conversions", "owner-token", nil, nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "No leads in this period.") {
		t.Errorf("empty = %d", rec.Code)
	}
	if rec := newRig(t).do("GET", "/admin/analytics/conversions", "owner-token", nil, nil); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("no reader = %d, want 503", rec.Code)
	}
	if rec := conversionsRig(t, &fakeConversions{err: errors.New("boom")}).do("GET", "/admin/analytics/conversions", "owner-token", nil, nil); rec.Code != http.StatusInternalServerError {
		t.Errorf("failing reader = %d, want 500", rec.Code)
	}
}

func TestFunnelListScalesBySquareRoot(t *testing.T) {
	t.Parallel()

	f := funnelList(analytics.Counts{Visits: 100, Multi: 25, FormSeen: 4, FormStarted: 1}, 1)
	if len(f.Items) != 5 {
		t.Fatalf("%d steps", len(f.Items))
	}
	if f.Items[0].N != 10 || f.Items[1].N != 5 || f.Items[1].Share != "25% of previous" || f.Items[2].Share != "16% of previous" {
		t.Errorf("funnel = %+v", f.Items)
	}
	if got := funnelList(analytics.Counts{}, 0); len(got.Items) != 0 {
		t.Error("no visits, no funnel")
	}
}
