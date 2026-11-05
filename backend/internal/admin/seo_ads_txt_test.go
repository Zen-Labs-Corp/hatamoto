package admin

import (
	"net/url"
	"strings"
	"testing"
)

const adsGood = "google.com, pub-1, DIRECT, f08c47fec0942fa0"

func TestSEOAdsTxtSave(t *testing.T) {
	s := newSEORig(t, newFakeSEO())
	s.svc.set.Home = "%sitename%"
	form := url.Values{"ads_txt": {"# me\r\n" + adsGood + " \r\n\r\n"}, "app_ads_txt": {""}}
	rec := s.do("POST", "/admin/settings/seo/ads-txt", "owner-token", form, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/settings/seo/ads-txt" {
		t.Fatalf("save = %d %s: %s", rec.Code, rec.Header().Get("Location"), rec.Body)
	}
	if got := s.svc.set.AdsTxt; got != "# me\n"+adsGood+"\n" {
		t.Errorf("ads_txt = %q", got)
	}
	if s.svc.set.AppAdsTxt != "" || s.svc.set.Home != "%sitename%" {
		t.Errorf("settings = %+v", s.svc.set)
	}
	if s.flushed != 1 {
		t.Errorf("page cache invalidated %d times, want 1", s.flushed)
	}
	if len(s.events) != 1 || s.events[0].Action != "seo.settings" {
		t.Fatalf("audit = %+v", s.events)
	}
	if d := s.events[0].Detail; len(d) != 3 || d["ads_lines"] != 2 || d["app_ads_lines"] != 0 {
		t.Errorf("audit detail = %v, want line counts only", d)
	}
}

func TestSEOAdsTxtRejectsBadLine(t *testing.T) {
	s := newSEORig(t, newFakeSEO())
	form := url.Values{"ads_txt": {adsGood}, "app_ads_txt": {"# ok\ngoogle.com, pub-1, MAYBE"}}
	rec := s.do("POST", "/admin/settings/seo/ads-txt", "owner-token", form, nil)
	body := rec.Body.String()
	if rec.Code != 422 || !strings.Contains(body, "Line 2") || !strings.Contains(body, `aria-invalid="true"`) {
		t.Fatalf("%d, want 422 with Line 2 and aria-invalid: %s", rec.Code, body)
	}
	if !strings.Contains(body, "google.com, pub-1, MAYBE") {
		t.Error("the typed text was not kept")
	}
	if s.svc.merges != 0 || s.flushed != 0 || len(s.events) != 0 {
		t.Error("a rejected save reached the store")
	}
}

func TestSEOAdsTxtOwnerOnly(t *testing.T) {
	s := newSEORig(t, newFakeSEO())
	for _, m := range []string{"GET", "POST"} {
		if rec := s.do(m, "/admin/settings/seo/ads-txt", "editor-token", url.Values{}, nil); rec.Code != 403 {
			t.Errorf("editor %s = %d, want 403", m, rec.Code)
		}
	}
	if !strings.Contains(s.get(t, "/admin/settings/seo/ads-txt"), `>ads.txt</a>`) {
		t.Error("the tab is missing from the SEO sub-nav")
	}
}

func TestSEOAdsTxtAddPrefillsWithoutSaving(t *testing.T) {
	s := newSEORig(t, newFakeSEO())
	body := s.get(t, "/admin/settings/seo/ads-txt?add=ca-pub-123")
	if !strings.Contains(body, "google.com, pub-123, DIRECT, f08c47fec0942fa0") || !strings.Contains(body, "not saved until you press Save") {
		t.Errorf("no prefill or note: %s", body)
	}
	if s.svc.set.AdsTxt != "" || s.svc.merges != 0 {
		t.Error("the prefill was stored")
	}
	s.svc.set.AdsTxt = "google.com, pub-123, DIRECT, f08c47fec0942fa0\n"
	body = s.get(t, "/admin/settings/seo/ads-txt?add=pub-123")
	if strings.Count(body, "pub-123, DIRECT") != 2 || !strings.Contains(body, "already in your ads.txt") { // textarea + preview
		t.Errorf("duplicate line added: %s", body)
	}
	if body := s.get(t, "/admin/settings/seo/ads-txt?add=evil"); strings.Contains(body, "AdSense line") {
		t.Error("an invalid id was accepted")
	}
}
