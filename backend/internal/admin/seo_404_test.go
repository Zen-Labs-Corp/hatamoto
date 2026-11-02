package admin

import (
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/seo"
)

func post404(s *seoRig, form url.Values) int {
	return s.do("POST", "/admin"+seo404Path, "owner-token", form, nil).Code
}

func TestMonitor404FromForm(t *testing.T) {
	stored := seo.Monitor404{Disabled: true, RetentionDays: 30, IgnorePatterns: []string{"/a"}}
	got := monitor404FromForm(url.Values{}, stored)
	if got.Disabled != true || got.RetentionDays != 30 || len(got.IgnorePatterns) != 1 {
		t.Errorf("empty post changed stored: %+v", got)
	}
	got = monitor404FromForm(url.Values{"enabled": {"true"}, "retention_days": {"180"}, "ignore_patterns": {" /wp-admin/* \n\n*.php\r\n"}}, stored)
	if got.Disabled || got.RetentionDays != 180 || strings.Join(got.IgnorePatterns, "|") != "/wp-admin/*|*.php" {
		t.Errorf("got %+v", got)
	}
}

func TestSEO404SaveRoundTrip(t *testing.T) {
	s := newSEORig(t, newFakeSEO())
	if code := post404(s, url.Values{"enabled": {"false"}, "retention_days": {"365"}, "ignore_patterns": {"/wp-admin/*\n*.php"}}); code != 303 {
		t.Fatalf("save = %d", code)
	}
	m := s.svc.set.Monitor404
	if !m.Disabled || m.RetentionDays != 365 || len(m.IgnorePatterns) != 2 {
		t.Fatalf("saved %+v", m)
	}
	if len(s.events) != 1 || strings.Contains(strings.Join(auditText(s.events), " "), "wp-admin") {
		t.Errorf("audit %+v (patterns must stay out)", s.events)
	}
	body := s.get(t, "/admin"+seo404Path)
	for _, want := range []string{"Log requests for missing pages", "Keep entries for", "Never log these paths", "/wp-admin/*", "Danger zone", "Delete all"} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}

func TestSEO404Rejects(t *testing.T) {
	s := newSEORig(t, newFakeSEO())
	if code := post404(s, url.Values{"retention_days": {"7"}}); code != 422 {
		t.Errorf("retention 7 = %d, want 422", code)
	}
	if code := post404(s, url.Values{"retention_days": {"abc"}}); code != 422 {
		t.Errorf("retention abc = %d, want 422", code)
	}
	many := strings.Repeat("/x\n", seo.MaxIgnorePatterns404+1)
	rec := s.do("POST", "/admin"+seo404Path, "owner-token", url.Values{"ignore_patterns": {many}}, nil)
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), "Never log these paths") {
		t.Errorf("51 patterns = %d, want 422 with the field label", rec.Code)
	}
	if s.svc.merges != 0 {
		t.Error("a refused save must not merge")
	}
}

func TestSEO404OwnerOnly(t *testing.T) {
	s := newSEORig(t, newFakeSEO())
	fr := newFakeRedirects()
	s.a.deps.Growth.Redirects = fr
	fr.addLog("/x", 1)
	for _, c := range [][2]string{{"GET", seo404Path}, {"POST", seo404Path}, {"GET", seo404DeletePath}, {"POST", seo404DeletePath}} {
		if rec := s.do(c[0], "/admin"+c[1], "editor-token", url.Values{"confirm_phrase": {seo404Phrase}}, nil); rec.Code != 403 {
			t.Errorf("editor %s %s = %d, want 403", c[0], c[1], rec.Code)
		}
	}
	if len(fr.log) != 1 {
		t.Error("an editor emptied the log")
	}
}

func TestSEO404DeleteAll(t *testing.T) {
	s := newSEORig(t, newFakeSEO())
	fr := newFakeRedirects()
	s.a.deps.Growth.Redirects = fr
	fr.addLog("/secret-path", 3)
	fr.addLog("/other", 1)
	if body := s.get(t, "/admin"+seo404DeletePath); !strings.Contains(body, seo404Phrase) {
		t.Fatal("the confirm page must name the phrase")
	}
	rec := s.do("POST", "/admin"+seo404DeletePath, "owner-token", url.Values{"confirm_phrase": {"nope"}}, nil)
	if rec.Code != 422 || len(fr.log) != 2 {
		t.Fatalf("wrong phrase = %d, log %d (want 422, 2)", rec.Code, len(fr.log))
	}
	rec = s.do("POST", "/admin"+seo404DeletePath, "owner-token", url.Values{"confirm_phrase": {seo404Phrase}}, nil)
	if rec.Code != 303 || len(fr.log) != 0 {
		t.Fatalf("delete = %d, log %d (want 303, 0)", rec.Code, len(fr.log))
	}
	if len(s.events) != 1 || s.events[0].Action != "404log.cleared" {
		t.Fatalf("audit %+v", s.events)
	}
	if txt := strings.Join(auditText(s.events), " "); strings.Contains(txt, "secret-path") || !strings.Contains(txt, "2") {
		t.Errorf("audit must hold the count only: %s", txt)
	}
}

func TestSEO404WithoutStore(t *testing.T) {
	s := newSEORig(t, newFakeSEO())
	if rec := s.do("GET", "/admin"+seo404DeletePath, "owner-token", nil, nil); rec.Code == 200 {
		t.Errorf("delete-all without a log store = %d", rec.Code)
	}
	if body := s.get(t, "/admin"+seo404Path); strings.Contains(body, "missing address") {
		t.Error("no count line without a store")
	}
}
