package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/agents"
	"hatamoto.dev/core/internal/analytics"
)

// fakeTraffic serves fixed rollups; prevFail makes every read of an earlier period fail.
type fakeTraffic struct {
	from     time.Time // the current period starts here; earlier reads are the previous period
	prevFail bool
	empty    bool
}

func (f *fakeTraffic) previous(from time.Time) bool { return from.Before(f.from) }

func (f *fakeTraffic) TrafficDaily(_ context.Context, dim analytics.Dim, from, _ time.Time) ([]analytics.TrafficDay, error) {
	if f.previous(from) {
		if f.prevFail {
			return nil, errors.New("boom")
		}
		return []analytics.TrafficDay{{Day: from.Format(dayLayout), K1: analytics.ChannelOrganic, Counts: analytics.Counts{Visits: 4}}}, nil
	}
	if f.empty {
		return []analytics.TrafficDay{}, nil
	}
	return []analytics.TrafficDay{
		{Day: from.Format(dayLayout), K1: analytics.ChannelOrganic, Counts: analytics.Counts{Visits: 6}},
		{Day: from.Format(dayLayout), K1: analytics.ChannelAI, Counts: analytics.Counts{Visits: 2}},
		{Day: from.Format(dayLayout), K1: "mystery", Counts: analytics.Counts{Visits: 9}},
	}, nil
}

func (f *fakeTraffic) TrafficTop(_ context.Context, dim analytics.Dim, from, _ time.Time, _ int) ([]analytics.TrafficTop, error) {
	if f.empty && !f.previous(from) {
		return []analytics.TrafficTop{}, nil
	}
	if f.previous(from) {
		if f.prevFail {
			return nil, errors.New("boom")
		}
		return []analytics.TrafficTop{{K1: analytics.ChannelOrganic, Counts: analytics.Counts{Visits: 4}}}, nil
	}
	switch dim {
	case analytics.DimChannel:
		return []analytics.TrafficTop{{K1: analytics.ChannelOrganic, Counts: analytics.Counts{Visits: 60, Pageviews: 100, Engaged: 30}},
			{K1: analytics.ChannelAI, Counts: analytics.Counts{Visits: 20, Pageviews: 30}}, {K1: analytics.ChannelDirect, Counts: analytics.Counts{Visits: 20, Pageviews: 20}}}, nil
	case analytics.DimSource:
		return []analytics.TrafficTop{{K1: "google", K2: analytics.ChannelOrganic, Counts: analytics.Counts{Visits: 60}}, {K1: analytics.SourceNone, K2: analytics.ChannelDirect, Counts: analytics.Counts{Visits: 20}},
			{K1: analytics.KeyOther, Counts: analytics.Counts{Visits: 3}}}, nil
	case analytics.DimCampaign:
		return []analytics.TrafficTop{{K1: "oct-news", K2: "email", Counts: analytics.Counts{Visits: 7}}}, nil
	case analytics.DimLanding:
		return []analytics.TrafficTop{{K1: "/", Counts: analytics.Counts{Visits: 50}}}, nil
	case analytics.DimDevice:
		return []analytics.TrafficTop{{K1: "desktop", Counts: analytics.Counts{Visits: 75}}, {K1: "mobile", Counts: analytics.Counts{Visits: 25}}}, nil
	case analytics.DimCountry:
		return []analytics.TrafficTop{{K1: "AU", Counts: analytics.Counts{Visits: 5}}, {K1: analytics.CountryUnknown, Counts: analytics.Counts{Visits: 1}}}, nil
	}
	return nil, nil
}

func (f *fakeTraffic) TrafficTotals(_ context.Context, from, _ time.Time) (analytics.Counts, error) {
	if f.previous(from) {
		if f.prevFail {
			return analytics.Counts{}, errors.New("boom")
		}
		return analytics.Counts{Visits: 50, Pageviews: 80}, nil
	}
	if f.empty {
		return analytics.Counts{}, nil
	}
	return analytics.Counts{Visits: 100, Pageviews: 150, Engaged: 54, Multi: 38, Scrolled: 26}, nil
}

func trafficRange() agents.Range {
	to := today()
	return agents.Range{From: to.AddDate(0, 0, -6), To: to}
}

func trafficAdmin(t *testing.T, f *fakeTraffic) *Admin {
	t.Helper()
	g := newRig(t, func(d *Deps) { d.Growth.Traffic = f })
	return g.a
}

func TestTrafficDataChartHasAllChannelsInOrder(t *testing.T) {
	t.Parallel()

	rg := trafficRange()
	a := trafficAdmin(t, &fakeTraffic{from: rg.From})
	v, err := a.trafficData(context.Background(), rg, false)
	if err != nil {
		t.Fatal(err)
	}
	c := v.Charts["channels"]
	if got := strings.Join(c.seriesLabels(), ","); got != "Organic search,AI assistants,Direct,Referral,Social,Email,Paid" {
		t.Errorf("series = %s", got)
	}
	if len(c.Labels) != 7 || !c.Stacked || c.Previous != nil {
		t.Errorf("chart = %d labels, stacked %v, previous %v", len(c.Labels), c.Stacked, c.Previous)
	}
	if c.Datasets[0].Data[0] != 6 || c.Datasets[1].Data[0] != 2 {
		t.Errorf("first day = %v, %v", c.Datasets[0].Data, c.Datasets[1].Data)
	}
	var sum int64
	for _, d := range c.Datasets {
		for _, n := range d.Data {
			sum += n
		}
	}
	if sum != 8 {
		t.Errorf("an unknown channel key must not be counted in a series, sum = %d", sum)
	}
}

func TestTrafficDataPreviousPeriodAlignsByIndex(t *testing.T) {
	t.Parallel()

	rg := trafficRange()
	a := trafficAdmin(t, &fakeTraffic{from: rg.From})
	v, err := a.trafficData(context.Background(), rg, true)
	if err != nil {
		t.Fatal(err)
	}
	c := v.Charts["channels"]
	if c.Previous == nil || len(c.Previous.Datasets) != len(c.Datasets) {
		t.Fatalf("previous = %+v", c.Previous)
	}
	for i, d := range c.Datasets {
		if c.Previous.Datasets[i].Label != d.Label {
			t.Errorf("series %d: previous %q, current %q", i, c.Previous.Datasets[i].Label, d.Label)
		}
	}
	if c.Previous.Datasets[0].Data[0] != 4 {
		t.Errorf("previous organic = %v", c.Previous.Datasets[0].Data)
	}

	bad := trafficAdmin(t, &fakeTraffic{from: rg.From, prevFail: true})
	v, err = bad.trafficData(context.Background(), rg, true)
	if err != nil || v.Charts["channels"].Previous != nil {
		t.Errorf("a failing previous period must only drop the line: %v, %+v", err, v.Charts["channels"].Previous)
	}
}

func TestTrafficWithoutReaderIsNoService(t *testing.T) {
	t.Parallel()

	a := newRig(t).a
	if _, err := a.trafficData(context.Background(), trafficRange(), false); !errors.Is(err, errNoService) {
		t.Errorf("trafficData err = %v", err)
	}
	if _, err := a.buildTraffic(context.Background(), url.Values{}, trafficRange(), false); !errors.Is(err, errNoService) {
		t.Errorf("buildTraffic err = %v", err)
	}
	if _, status, _ := a.trafficJSON(context.Background(), url.Values{}, trafficRange()); status != 503 {
		t.Errorf("json status = %d", status)
	}
}

func TestTrafficJSONShape(t *testing.T) {
	t.Parallel()

	rg := trafficRange()
	a := trafficAdmin(t, &fakeTraffic{from: rg.From})
	v, status, msg := a.trafficJSON(context.Background(), url.Values{"compare": {"1"}}, rg)
	if status != 200 || msg != "" {
		t.Fatalf("status %d %q", status, msg)
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		From, To string
		Charts   map[string]struct {
			Kind     string
			Stacked  bool
			Labels   []string
			Datasets []struct{ Label string }
			Previous *struct{ Datasets []struct{ Label string } }
		}
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	c, ok := got.Charts["channels"]
	if !ok || c.Kind != "bar" || !c.Stacked || len(c.Datasets) != 7 || c.Previous == nil || len(c.Previous.Datasets) != 7 {
		t.Errorf("charts.channels = %+v", c)
	}
	if got.From != rg.From.Format(dayLayout) || got.To != rg.To.Format(dayLayout) {
		t.Errorf("range %s..%s", got.From, got.To)
	}
}

func TestBuildTrafficFigures(t *testing.T) {
	t.Parallel()

	rg := trafficRange()
	a := trafficAdmin(t, &fakeTraffic{from: rg.From})
	body, err := a.buildTraffic(context.Background(), url.Values{}, rg, true)
	if err != nil {
		t.Fatal(err)
	}
	p := body.(*trafficPage)
	byLabel := map[string]string{}
	for _, k := range p.KPIs {
		byLabel[k.Label] = k.Value
	}
	if byLabel["Visits"] != "100" || byLabel["Pageviews"] != "150" || byLabel["Leads"] != "--" || byLabel["Conv. rate"] != "--" {
		t.Errorf("kpis = %v", byLabel)
	}
	if top := p.KPIs[4]; top.Badge == nil || top.Badge.Label != "Organic search" || top.Note != "60% of visits" {
		t.Errorf("top channel = %+v", top)
	}
	if d := p.KPIs[0].Change; d == nil || d.Text != "+100.0%" {
		t.Errorf("visits delta = %+v", d)
	}
	if len(p.Channels.Rows) != 7 || p.Channels.Rows[0][1].Text != "60" || p.Channels.Rows[0][2].Text != "60.0%" || p.Channels.Rows[0][2].Bar == nil {
		t.Errorf("organic row = %+v", p.Channels.Rows[0])
	}
	if p.Channels.Rows[0][3].Text != "--" || p.Channels.Rows[0][4].Text != "--" || p.Channels.Rows[0][5].Text != "+1400.0%" || p.Channels.Rows[0][6].Spark == nil {
		t.Errorf("organic row tail = %+v", p.Channels.Rows[0][3:])
	}
	if p.Channels.Rows[4][1].Text != "0" {
		t.Errorf("an empty channel keeps its row: %+v", p.Channels.Rows[4])
	}
	if len(p.Attribution.Items) != 2 || !p.Attribution.Items[0].Disabled || !p.Attribution.Items[1].Disabled {
		t.Errorf("attribution = %+v", p.Attribution)
	}
	if s := p.Sources.Rows[1]; s[0].Text != "(direct)" || s[1].Text != "Direct" || s[3].Text != "--" {
		t.Errorf("source row = %+v", s)
	}
	if s := p.Sources.Rows[2]; s[1].Text != "Mixed" {
		t.Errorf("the (other) source row must name its channel Mixed: %+v", s)
	}
	if c := p.Campaigns.Rows[0]; c[0].Text != "oct-news" || c[1].Text != "email" {
		t.Errorf("campaign row = %+v", c)
	}
	if got := p.Devices.Items[0]; got.Label != "Desktop" || got.Share != "75%" {
		t.Errorf("device = %+v", got)
	}
	if got := p.Countries.Items; got[0].Label != "Australia" || got[1].Label != "Unknown" {
		t.Errorf("countries = %+v", got)
	}
	if !strings.Contains(p.CountriesSetup, "HATAMOTO_GEOIP_DB") {
		t.Errorf("setup = %q", p.CountriesSetup)
	}
	want := []string{"46", "38", "26"}
	for i, it := range p.Engagement.Items {
		if it.Value != want[i] {
			t.Errorf("engagement %d = %+v", i, it)
		}
	}
	if len(p.Channels.Columns) != 7 || p.Channels.Columns[5] != "vs previous" {
		t.Errorf("columns = %v", p.Channels.Columns)
	}
}

func TestBuildTrafficGeoStatusAndCompareFallback(t *testing.T) {
	t.Parallel()

	rg := trafficRange()
	f := &fakeTraffic{from: rg.From, prevFail: true}
	g := newRig(t, func(d *Deps) {
		d.Growth.Traffic = f
		d.Growth.GeoStatus = func() (bool, bool) { return true, true }
	})
	body, err := g.a.buildTraffic(context.Background(), url.Values{}, rg, true)
	if err != nil {
		t.Fatal(err)
	}
	p := body.(*trafficPage)
	if p.CountriesSetup != "" {
		t.Errorf("a loaded database has no setup state: %q", p.CountriesSetup)
	}
	if p.KPIs[0].Change != nil || len(p.Channels.Columns) != 6 {
		t.Error("a failing previous period turns compare off")
	}
	g = newRig(t, func(d *Deps) {
		d.Growth.Traffic = f
		d.Growth.GeoStatus = func() (bool, bool) { return true, false }
	})
	body, _ = g.a.buildTraffic(context.Background(), url.Values{}, rg, false)
	if !strings.Contains(body.(*trafficPage).CountriesSetup, "could not be read") {
		t.Errorf("configured but unloaded: %q", body.(*trafficPage).CountriesSetup)
	}
}

func TestBuildTrafficEmpty(t *testing.T) {
	t.Parallel()

	rg := trafficRange()
	a := trafficAdmin(t, &fakeTraffic{from: rg.From, empty: true})
	body, err := a.buildTraffic(context.Background(), url.Values{}, rg, false)
	if err != nil {
		t.Fatal(err)
	}
	p := body.(*trafficPage)
	if len(p.Channels.Rows) != 0 || len(p.Sources.Rows) != 0 || len(p.Engagement.Items) != 0 || p.KPIs[0].Value != "0" || p.KPIs[4].Badge != nil || p.KPIs[4].Value != "--" {
		t.Errorf("empty view = %+v", p.KPIs)
	}
}
