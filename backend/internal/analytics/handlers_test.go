package analytics

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// The sandboxed-page pageview is a fetch with a string body, which the browser
// labels text/plain. Track reads the body whatever its Content-Type.
func TestTrackAcceptsTextPlainBody(t *testing.T) {
	repo := &fakeRepo{}
	svc := newTestService(repo)
	req := httptest.NewRequest(http.MethodPost, "/track", strings.NewReader(`{"events":[{"type":"page_view"}]}`))
	req.Header.Set("Content-Type", "text/plain;charset=UTF-8")
	req.Header.Set("User-Agent", "Mozilla/5.0")
	rec := httptest.NewRecorder()
	NewHandlers(svc).Track(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d", rec.Code)
	}
	if calls := flushed(t, svc, repo); len(calls) != 1 || calls[0].kind != KindPageView || calls[0].n != 1 {
		t.Fatalf("calls = %+v", calls)
	}
}

type fakeGeo struct{ gotIP *string }

func (g fakeGeo) Country(ip string) string { *g.gotIP = ip; return "DE" }

func trafficTrack(t *testing.T, ua, host, remote, body string, geo Geo, engage bool) (map[string]Counts, []addCall) {
	t.Helper()
	repo, trepo := &fakeRepo{}, &fakeTrafficRepo{}
	svc := newTestService(repo)
	svc.SetTrafficRepo(trepo)
	svc.SetEngagement(engage)
	h := NewHandlers(svc)
	h.SetGeo(geo)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/track", strings.NewReader(body))
	req.Host = host
	req.RemoteAddr = remote
	req.Header.Set("User-Agent", ua)
	rec := httptest.NewRecorder()
	h.Track(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d", rec.Code)
	}
	if err := svc.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	return rowMap(trepo.rows), repo.calls
}

const browserUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) Safari/605.1.15"

func TestTrackUTMEntryCountsOneEmailVisit(t *testing.T) {
	m, calls := trafficTrack(t, browserUA, "site.test", "203.0.113.9:1",
		`{"v":2,"events":[{"type":"page_view","entry":true,"utm_source":"Newsletter","utm_medium":"email","utm_campaign":"x","path":"/landing?a=1#f"}]}`, nil, true)
	if got := m["channel|email|"]; got.Visits != 1 || got.Pageviews != 1 {
		t.Fatalf("channel = %+v (%v)", got, m)
	}
	if m["source|newsletter|email"].Visits != 1 || m["campaign|x|email"].Visits != 1 || m["landing|/landing|"].Visits != 1 {
		t.Fatalf("rows = %v", m)
	}
	if m["device|desktop|"].Visits != 1 || m["country|(unknown)|"].Visits != 1 {
		t.Fatalf("rows = %v", m)
	}
	if len(calls) != 1 || calls[0].kind != KindPageView {
		t.Fatalf("legacy calls = %+v", calls)
	}
}

func TestTrackAIAssistantReferrer(t *testing.T) {
	m, _ := trafficTrack(t, browserUA, "site.test", "203.0.113.9:1",
		`{"v":2,"events":[{"type":"page_view","entry":true,"ref":"chatgpt.com","path":"/"}]}`, nil, true)
	if m["channel|ai_assistants|"].Visits != 1 || m["source|chatgpt|ai_assistants"].Visits != 1 {
		t.Fatalf("rows = %v", m)
	}
}

func TestTrackOwnHostReferrerCountsPageviewOnly(t *testing.T) {
	m, calls := trafficTrack(t, browserUA, "www.Site.test:8090", "203.0.113.9:1",
		`{"v":2,"events":[{"type":"page_view","entry":true,"ref":"site.test","path":"/"}]}`, nil, true)
	if len(m) != 0 {
		t.Fatalf("traffic rows = %v", m)
	}
	if len(calls) != 1 || calls[0].kind != KindPageView {
		t.Fatalf("legacy calls = %+v", calls)
	}
}

func TestTrackDropsBotsAndEmptyUA(t *testing.T) {
	body := `{"v":2,"events":[{"type":"page_view","entry":true,"path":"/"}]}`
	for _, ua := range []string{"Mozilla/5.0 (compatible; Googlebot/2.1)", "SomeCrawler-bot/1.0", ""} {
		m, calls := trafficTrack(t, ua, "site.test", "203.0.113.9:1", body, nil, true)
		if len(m) != 0 || len(calls) != 0 {
			t.Errorf("ua %q: rows %v calls %v", ua, m, calls)
		}
	}
}

func TestTrackLegacyBodyWritesNoTrafficRow(t *testing.T) {
	m, calls := trafficTrack(t, browserUA, "site.test", "203.0.113.9:1", `{"events":[{"type":"page_view"}]}`, nil, true)
	if len(m) != 0 || len(calls) != 1 || calls[0].kind != KindPageView || calls[0].n != 1 {
		t.Fatalf("rows %v calls %v", m, calls)
	}
}

func TestTrackEngagement(t *testing.T) {
	body := `{"v":2,"events":[{"type":"engage","kind":"scroll","first":true,"ref":"google.com","path":"/a"}]}`
	m, calls := trafficTrack(t, browserUA, "site.test", "203.0.113.9:1", body, nil, true)
	if got := m["channel|organic|"]; got.Scrolled != 1 || got.Engaged != 1 || got.Pageviews != 0 {
		t.Fatalf("channel = %+v", got)
	}
	if len(calls) != 0 {
		t.Fatalf("engage touched the pageview counter: %+v", calls)
	}
	m, _ = trafficTrack(t, browserUA, "site.test", "203.0.113.9:1", body, nil, false)
	if len(m) != 0 {
		t.Fatalf("engage counted while disabled: %v", m)
	}
}

func TestTrackSanitisesFields(t *testing.T) {
	body := `{"v":2,"events":[{"type":"page_view","entry":true,"utm_source":"<script>alert(1)</script>","utm_medium":"","utm_campaign":"` +
		strings.Repeat("a", 500) + `","ref":"Evil.Example/path?x=1","path":"//x\"y"}]}`
	m, _ := trafficTrack(t, browserUA, "site.test", "203.0.113.9:1", body, nil, true)
	for k := range m {
		if strings.ContainsAny(strings.ReplaceAll(k, "(unknown)", ""), "<>()\"?=") {
			t.Errorf("unsanitised key %q", k)
		}
	}
	if m["source|scriptalert1/script|referral"].Visits != 1 {
		t.Fatalf("rows = %v", m)
	}
	if m["campaign|"+strings.Repeat("a", maxUTMLen)+"|"].Visits != 1 {
		t.Fatalf("campaign not truncated: %v", m)
	}
}

func TestTrackCountryUsesTrustedClientIP(t *testing.T) {
	var ip string
	body := `{"v":2,"events":[{"type":"page_view","entry":true,"path":"/"}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/track", strings.NewReader(body))
	req.RemoteAddr = "203.0.113.9:4444"
	req.Header.Set("X-Forwarded-For", "198.51.100.7")
	req.Header.Set("CF-IPCountry", "FR")
	req.Header.Set("User-Agent", browserUA)
	svc := newTestService(&fakeRepo{})
	trepo := &fakeTrafficRepo{}
	svc.SetTrafficRepo(trepo)
	h := NewHandlers(svc)
	h.SetGeo(fakeGeo{&ip})
	h.Track(httptest.NewRecorder(), req)
	_ = svc.Flush(context.Background())
	if ip != "203.0.113.9" {
		t.Fatalf("geo looked up %q, want the peer (untrusted X-Forwarded-For ignored)", ip)
	}
	if rowMap(trepo.rows)["country|DE|"].Visits != 1 {
		t.Fatalf("rows = %v", rowMap(trepo.rows))
	}
}

func TestTrackNeverTouchesDBBeforeFlush(t *testing.T) {
	r := touchedRepo{t}
	svc := NewService(r, nil)
	svc.SetTrafficRepo(r)
	h := NewHandlers(svc)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/track",
		strings.NewReader(`{"v":2,"events":[{"type":"page_view","entry":true,"path":"/"}]}`))
	req.Header.Set("User-Agent", browserUA)
	h.Track(httptest.NewRecorder(), req)
}

// TestTrackPayloadKeysMatchBeacon pins the /track decoder to the embedded
// beacon: every JSON key the handler reads must be written by hm_track.js,
// except the legacy "kind" that only the old hm.js beacon sent.
func TestTrackPayloadKeysMatchBeacon(t *testing.T) {
	src, err := os.ReadFile("../render/hm_track.js")
	if err != nil {
		t.Fatal(err)
	}
	typ := reflect.TypeOf(trackEvent{})
	for i := 0; i < typ.NumField(); i++ {
		key := strings.Split(typ.Field(i).Tag.Get("json"), ",")[0]
		if key == "kind" {
			continue
		}
		if !regexp.MustCompile(`\b` + key + `\s*:`).Match(src) {
			t.Errorf("hm_track.js never sends %q, which the /track handler decodes", key)
		}
	}
}

func TestTrackReducesFullURLRefToHost(t *testing.T) {
	body := `{"v":2,"events":[{"type":"page_view","entry":true,"ref":"https://www.Google.com:443/search?q=x","path":"/"}]}`
	m, _ := trafficTrack(t, browserUA, "site.test", "203.0.113.9:1", body, nil, true)
	if m["source|google.com|organic"].Visits != 1 {
		t.Fatalf("rows = %v", m)
	}
}

func TestRefHostSchemeOnlyBeforePath(t *testing.T) {
	for in, want := range map[string]string{
		"https://www.google.com/url":     "google.com",
		"google.com/url?q=https://x.com": "google.com",
		"google.com?q=https://x.com":     "google.com",
		"google.com#https://x.com":       "google.com",
	} {
		if got := refHost(in); got != want {
			t.Errorf("refHost(%q) = %q, want %q", in, got, want)
		}
	}
}
