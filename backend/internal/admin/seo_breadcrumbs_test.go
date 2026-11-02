package admin

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/seo"
)

func TestBreadcrumbsFromForm(t *testing.T) {
	stored := seo.Breadcrumbs{HomeLabel: "Start", Separator: "»", HideCurrent: true}
	if got := breadcrumbsFromForm(url.Values{}, stored); got != stored {
		t.Errorf("empty post changed stored: %+v", got)
	}
	got := breadcrumbsFromForm(url.Values{
		"home_label": {"  Index "}, "separator": {""}, "show_home": {"false"},
		"hide_current": {"false"}, "show_term": {"true"},
	}, stored)
	want := seo.Breadcrumbs{HomeLabel: "Index", HideHome: true, ShowTerm: true}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
	if got := breadcrumbsFromForm(url.Values{"show_home": {"true"}}, seo.Breadcrumbs{HideHome: true}); got.HideHome {
		t.Error("show_home=true must clear HideHome")
	}
}

func TestBreadcrumbsPreview(t *testing.T) {
	for _, c := range []struct {
		b    seo.Breadcrumbs
		want string
	}{
		{seo.Breadcrumbs{}, "Home / Install Hatamoto"},
		{seo.Breadcrumbs{HomeLabel: "Start", Separator: "›", ShowTerm: true}, "Start › Guides › Install Hatamoto"},
		{seo.Breadcrumbs{HideHome: true, HideCurrent: true, ShowTerm: true}, "Guides"},
	} {
		if got := breadcrumbsPreview(c.b); got != c.want {
			t.Errorf("%+v = %q, want %q", c.b, got, c.want)
		}
	}
}

func TestSEOBreadcrumbsSave(t *testing.T) {
	s := newSEORig(t, newFakeSEO())
	s.svc.set.Home = "%sitename%"
	r := httptest.NewRequest("POST", "/admin"+seoBreadcrumbsPath, strings.NewReader(url.Values{
		"home_label": {"Start"}, "separator": {"›"}, "show_term": {"true"}, "hide_current": {"true"},
	}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	s.a.handleSEOBreadcrumbsSave(rec, r)
	if rec.Code != 303 {
		t.Fatalf("save = %d: %s", rec.Code, rec.Body)
	}
	want := seo.Breadcrumbs{HomeLabel: "Start", Separator: "›", ShowTerm: true, HideCurrent: true}
	if got := s.svc.set.Breadcrumbs; got != want {
		t.Errorf("saved %+v, want %+v", got, want)
	}
	if s.svc.set.Home != "%sitename%" {
		t.Error("another tab's key was clobbered")
	}
	if s.flushed != 1 || len(s.events) != 1 || s.events[0].Action != "seo.settings" {
		t.Errorf("flushed %d, audit %+v", s.flushed, s.events)
	}
}

func TestSEOBreadcrumbsSaveRejectsBadSeparator(t *testing.T) {
	s := newSEORig(t, newFakeSEO())
	r := httptest.NewRequest("POST", "/admin"+seoBreadcrumbsPath, strings.NewReader(url.Values{"separator": {"<b>"}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	s.a.handleSEOBreadcrumbsSave(rec, r)
	// Re-rendering the form needs the signed-in view, which only the routed
	// handler has; here it is enough that nothing was redirected or saved.
	if rec.Code == 303 {
		t.Fatalf("an invalid post redirected as saved")
	}
	if !s.svc.set.Breadcrumbs.IsZero() || s.svc.merges != 0 {
		t.Error("an invalid post was saved")
	}
}
