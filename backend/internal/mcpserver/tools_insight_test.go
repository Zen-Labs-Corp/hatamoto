package mcpserver

import (
	"context"
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
