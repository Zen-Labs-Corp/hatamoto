package admin

import (
	"net/url"
	"testing"
)

func goodSitemapForm() url.Values {
	return url.Values{
		"sitemap_author": {"false"}, "sitemap_taxonomy": {"true"}, "sitemap_images": {"true"}, "sitemap_stats": {"true"},
		"sitemap_max_urls": {"1000"}, "sitemap_default_priority": {"0.5"}, "sitemap_default_changefreq": {"weekly"},
		"sitemap_exclude_paths": {"/legal/*\r\n/old\r\n\r\n"},
	}
}

func TestSEOSitemapSave(t *testing.T) {
	s := newSEORig(t, newFakeSEO())
	s.svc.set.Home = "%sitename%"
	rec := s.do("POST", "/admin/settings/seo/sitemap", "owner-token", goodSitemapForm(), nil)
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/settings/seo/sitemap" {
		t.Fatalf("save = %d %s: %s", rec.Code, rec.Header().Get("Location"), rec.Body)
	}
	got := s.svc.set.Sitemap
	if !got.NoAuthor || !got.Taxonomy || !got.Images || got.NoStats || got.MaxURLs != 1000 ||
		got.DefaultPriority == nil || *got.DefaultPriority != 0.5 || got.DefaultChangeFreq != "weekly" {
		t.Fatalf("sitemap = %+v", got)
	}
	if len(got.ExcludePaths) != 2 || got.ExcludePaths[0] != "/legal/*" || got.ExcludePaths[1] != "/old" {
		t.Errorf("paths = %q", got.ExcludePaths)
	}
	if s.svc.set.Home != "%sitename%" {
		t.Errorf("another tab's keys were clobbered: %+v", s.svc.set)
	}
	if s.svc.merges != 1 || s.flushed != 1 {
		t.Errorf("merges = %d, flushed = %d, want 1 each", s.svc.merges, s.flushed)
	}
	if len(s.events) != 1 || s.events[0].Action != "seo.settings" {
		t.Fatalf("audit = %+v", s.events)
	}
	if len(s.svc.patches) != 0 {
		t.Errorf("no type changed, patches = %v", s.svc.patches)
	}
}

func TestSEOSitemapKeepsUnpostedAndDefaults(t *testing.T) {
	s := newSEORig(t, newFakeSEO())
	s.svc.set.Sitemap.Images = true
	s.svc.set.Sitemap.ExcludePaths = []string{"/keep"}
	form := url.Values{"sitemap_max_urls": {"50000"}, "sitemap_default_priority": {""}}
	if rec := s.do("POST", "/admin/settings/seo/sitemap", "owner-token", form, nil); rec.Code != 303 {
		t.Fatalf("save = %d: %s", rec.Code, rec.Body)
	}
	got := s.svc.set.Sitemap
	if !got.Images || len(got.ExcludePaths) != 1 || got.MaxURLs != 0 || got.DefaultPriority != nil {
		t.Errorf("sitemap = %+v", got)
	}
}

func TestSEOSitemapPatchesOnlyChangedTypes(t *testing.T) {
	s := newSEORig(t, newFakeSEO())
	form := url.Values{
		"sitemap[page][include]": {"false"}, "sitemap[page][priority]": {"0.8"}, "sitemap[page][changefreq]": {"daily"},
		"sitemap[service][include]": {"true"}, "sitemap[service][priority]": {""}, "sitemap[service][changefreq]": {""},
	}
	if rec := s.do("POST", "/admin/settings/seo/sitemap", "owner-token", form, nil); rec.Code != 303 {
		t.Fatalf("save = %d: %s", rec.Code, rec.Body)
	}
	if len(s.svc.patches) != 1 {
		t.Fatalf("patches = %v, want only the built-in page type", s.svc.patches)
	}
	p := s.svc.patches[s.page.ID].Sitemap
	if p == nil || !p.Exclude || p.Priority == nil || *p.Priority != 0.8 || p.ChangeFreq != "daily" {
		t.Errorf("page patch = %+v", p)
	}
}

func TestSEOSitemapEditorForbidden(t *testing.T) {
	s := newSEORig(t, newFakeSEO())
	for _, m := range []string{"GET", "POST"} {
		if rec := s.do(m, "/admin/settings/seo/sitemap", "editor-token", goodSitemapForm(), nil); rec.Code != 403 {
			t.Errorf("editor %s = %d, want 403", m, rec.Code)
		}
	}
	if s.svc.merges != 0 {
		t.Errorf("editor changed settings")
	}
}
