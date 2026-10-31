package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/agents"
	"hatamoto.dev/core/internal/analytics"
	"hatamoto.dev/core/internal/middleware"
)

type fakeVerification struct {
	h   agents.VerificationHealth
	ids []string
}

func (f *fakeVerification) VerificationHealth(_ context.Context, _ agents.Range, ids []string) (agents.VerificationHealth, error) {
	f.ids = ids
	return f.h, nil
}

func TestCrawlerVerification(t *testing.T) {
	rg := agents.Range{From: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)}
	src := middleware.ClientIPSource{Header: "CF-Connecting-IP", CDN: true}
	cases := []struct {
		name  string
		h     agents.VerificationHealth
		level string
		share float64
	}{
		{"bad", agents.VerificationHealth{Verified: 20, Spoofed: 80, Unverifiable: 5, SpoofedIPs: 7}, "bad", 0.8},
		{"ok", agents.VerificationHealth{Verified: 95, Spoofed: 5}, "ok", 0.05},
		{"unknown", agents.VerificationHealth{Verified: 2, Spoofed: 1}, "unknown", 1.0 / 3},
		{"empty", agents.VerificationHealth{}, "unknown", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := &fakeVerification{h: c.h}
			out, err := crawlerVerification(context.Background(), f, rg, src)
			if err != nil {
				t.Fatal(err)
			}
			if out["level"] != c.level || out["spoofed_share"] != c.share {
				t.Errorf("level/share = %v/%v, want %v/%v", out["level"], out["spoofed_share"], c.level, c.share)
			}
			if out["decided"] != c.h.Decided() || out["spoofed_ips"] != c.h.SpoofedIPs || out["header"] != "CF-Connecting-IP" {
				t.Errorf("unexpected result %v", out)
			}
			if len(f.ids) == 0 {
				t.Error("no range-published agent ids passed")
			}
		})
	}
}

type fakeGoogle struct {
	k      agents.GoogleKPIs
	rows   []agents.GooglePageRow
	filter string
}

func (f *fakeGoogle) GoogleKPIs(context.Context, agents.Range) (agents.GoogleKPIs, error) {
	return f.k, nil
}

func (f *fakeGoogle) GooglePages(_ context.Context, _ agents.Range, filter string, _, _ int) ([]agents.GooglePageRow, int, error) {
	f.filter = filter
	return f.rows, len(f.rows), nil
}

func TestGoogleSummary(t *testing.T) {
	rg := agents.Range{From: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)}
	day := time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC)
	med := 90 * time.Minute
	f := &fakeGoogle{
		k:    agents.GoogleKPIs{Hits: 200, Verified: 150, PagesCrawled: 3, Published: 10, MedianFirstCrawl: &med},
		rows: []agents.GooglePageRow{{Path: "/a", Crawls: 5, LastDay: &day, LastStatus: 200, Stale: true}, {Path: "/b"}},
	}
	out, err := googleSummary(context.Background(), f, rg, "", 20)
	if err != nil {
		t.Fatal(err)
	}
	if f.filter != agents.GoogleFilterAll || out["verified_share"] != 0.75 || out["pages_published"] != 10 || out["total"] != 2 {
		t.Errorf("unexpected result %v (filter %q)", out, f.filter)
	}
	if m, _ := out["median_first_crawl_seconds"].(*float64); m == nil || *m != 5400 {
		t.Errorf("median = %v", out["median_first_crawl_seconds"])
	}
	b, _ := json.Marshal(out["items"])
	if want := `[{"entity_id":null,"path":"/a","name":"","crawls":5,"last_crawl_day":"2026-10-28","last_status":200,"stale":true},{"entity_id":null,"path":"/b","name":"","crawls":0,"last_crawl_day":null,"last_status":0,"stale":false}]`; string(b) != want {
		t.Errorf("items = %s", b)
	}
	if _, err := googleSummary(context.Background(), f, rg, "bogus", 20); err == nil {
		t.Error("an unknown filter was accepted")
	}
}

type fakeTraffic struct {
	dim  analytics.Dim
	rows []analytics.TrafficTop
}

func (f *fakeTraffic) TrafficTop(_ context.Context, dim analytics.Dim, _, _ time.Time, _ int) ([]analytics.TrafficTop, error) {
	f.dim = dim
	return f.rows, nil
}

func TestTrafficSummary(t *testing.T) {
	rg := agents.Range{From: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 1, 7, 0, 0, 0, 0, time.UTC)}
	f := &fakeTraffic{rows: []analytics.TrafficTop{
		{K1: "google.com", K2: "organic search", Counts: analytics.Counts{Visits: 8, Pageviews: 12, Engaged: 3}},
		{K1: "(other)", Counts: analytics.Counts{}},
	}}
	out, err := trafficSummary(context.Background(), f, rg, "page", 10)
	if err != nil {
		t.Fatal(err)
	}
	if f.dim != analytics.DimLanding || out["by"] != "page" || out["from"] != "2026-01-01" || out["to"] != "2026-01-07" {
		t.Errorf("unexpected result %v (dim %s)", out, f.dim)
	}
	raw, _ := json.Marshal(out["rows"])
	want := `[{"key":"google.com","detail":"organic search","visits":8,"pageviews":12,"engaged_pct":37.5},{"key":"(other)","visits":0,"pageviews":0,"engaged_pct":0}]`
	if string(raw) != want {
		t.Errorf("rows = %s", raw)
	}
	if _, err := trafficSummary(context.Background(), f, rg, "ip", 10); !errors.As(err, new(userError)) {
		t.Errorf("invalid by: %v", err)
	}
	if out, err := trafficSummary(context.Background(), f, rg, "", 10); err != nil || out["by"] != "channel" {
		t.Errorf("default by: %v %v", out, err)
	}
}

// The traffic rows carry counts and keys only: no field may name a visitor, IP or user agent.
func TestTrafficRowsHaveNoVisitorFields(t *testing.T) {
	out, _ := trafficSummary(context.Background(), &fakeTraffic{rows: []analytics.TrafficTop{{K1: "x"}}}, agents.Range{}, "channel", 5)
	raw, _ := json.Marshal(out)
	for _, bad := range []string{"ip", "user_agent", "ua", "visitor", "referrer"} {
		if strings.Contains(string(raw), `"`+bad+`"`) {
			t.Errorf("result has a %q field: %s", bad, raw)
		}
	}
}

type fakeConversions struct {
	dim   analytics.Dim
	touch analytics.TouchKind
	limit int
}

func (f *fakeConversions) LeadsTop(_ context.Context, dim analytics.Dim, touch analytics.TouchKind, _, _ time.Time, limit int) ([]analytics.LeadRow, error) {
	f.dim, f.touch, f.limit = dim, touch, limit
	return []analytics.LeadRow{{K1: "ai_assistants", Leads: 3}, {K1: "unknown", Leads: 1}}, nil
}

func (f *fakeConversions) LeadsTotals(_ context.Context, _ analytics.TouchKind, _, _ time.Time) (int64, int64, error) {
	return 4, 1, nil
}

func (f *fakeConversions) LeadsByForm(_ context.Context, _, _ time.Time, limit int) ([]analytics.LeadForm, error) {
	f.limit = limit
	return []analytics.LeadForm{{Name: "Contact", Leads: 4}}, nil
}

func TestConversionsSummary(t *testing.T) {
	rg := agents.Range{From: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 1, 7, 0, 0, 0, 0, time.UTC)}
	f := &fakeConversions{}
	out, err := conversionsSummary(context.Background(), f, rg, "", "", 20)
	if err != nil {
		t.Fatal(err)
	}
	if f.touch != analytics.TouchFirst || f.dim != analytics.DimChannel || out["touch"] != "first" || out["by"] != "channel" {
		t.Errorf("defaults: %v touch=%s dim=%s", out, f.touch, f.dim)
	}
	raw, _ := json.Marshal(out["rows"])
	if want := `[{"key":"ai_assistants","leads":3},{"key":"unknown","leads":1}]`; string(raw) != want || out["total_leads"] != int64(4) || out["unattributed_leads"] != int64(1) {
		t.Errorf("result = %v", out)
	}
	if _, err := conversionsSummary(context.Background(), f, rg, "last", "page", 7); err != nil || f.touch != analytics.TouchLast || f.dim != analytics.DimLanding || f.limit != 7 {
		t.Errorf("last/page: %v touch=%s dim=%s limit=%d", err, f.touch, f.dim, f.limit)
	}
	if out, err := conversionsSummary(context.Background(), f, rg, "", "form", 5); err != nil || out["by"] != "form" {
		t.Errorf("form: %v %v", out, err)
	}
	for _, bad := range [][2]string{{"middle", "channel"}, {"first", "email"}} {
		if _, err := conversionsSummary(context.Background(), f, rg, bad[0], bad[1], 5); !errors.As(err, new(userError)) {
			t.Errorf("touch=%q by=%q accepted: %v", bad[0], bad[1], err)
		}
	}
}

// The result names counts and keys only: no submitter email or field values.
func TestConversionsHaveNoSubmitterFields(t *testing.T) {
	out, _ := conversionsSummary(context.Background(), &fakeConversions{}, agents.Range{}, "", "channel", 5)
	raw, _ := json.Marshal(out)
	for _, bad := range []string{"email", "data", "ip", "user_agent", "visitor"} {
		if strings.Contains(string(raw), `"`+bad+`"`) {
			t.Errorf("result has a %q field: %s", bad, raw)
		}
	}
}
