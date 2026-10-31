package mcpserver

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"hatamoto.dev/core/internal/agents"
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
