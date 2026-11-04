package admin

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/consent"
)

type fakePrivacy struct {
	set    consent.Settings
	counts []consent.DayCounts
	saves  int
}

func (f *fakePrivacy) Get(context.Context) (consent.Settings, error) { return f.set, nil }
func (f *fakePrivacy) Save(_ context.Context, s consent.Settings) error {
	if errs := s.Validate(); len(errs) > 0 {
		return &consent.ValidationError{Errors: errs}
	}
	f.set = s
	f.saves++
	return nil
}
func (f *fakePrivacy) Counts(context.Context, int) ([]consent.DayCounts, error) {
	return f.counts, nil
}

func newPrivacyRig(t *testing.T) (*seoRig, *fakePrivacy) {
	t.Helper()
	s := newSEORig(t, newFakeSEO())
	f := &fakePrivacy{set: consent.Defaults()}
	s.a.deps.Privacy = f
	return s, f
}

func TestPrivacyPageGolden(t *testing.T) {
	s, f := newPrivacyRig(t)
	f.counts = []consent.DayCounts{{Day: "2026-11-03", All: 7, None: 3, Custom: 1}, {Day: "2026-11-02", All: 2}}
	body := s.get(t, "/admin/settings/privacy")
	if !strings.Contains(body, "2026-11-03") || !strings.Contains(body, "Show a cookie banner") {
		t.Error("page lacks the counts table or the switch")
	}
	compareGolden(t, "pages/settings-privacy.html", normalizePage(body))
}

func TestPrivacySaveAuditsAndClearsCache(t *testing.T) {
	s, f := newPrivacyRig(t)
	form := url.Values{
		"enabled": {"true"}, "region": {"eu_uk"}, "title": {"Cookies"}, "text": {"Pick one."},
		"categories_present": {"1"}, "categories": {"analytics", "marketing"},
	}
	rec := s.do("POST", "/admin/settings/privacy", "owner-token", form, nil)
	if rec.Code != 303 {
		t.Fatalf("save = %d: %s", rec.Code, rec.Body.String())
	}
	if !f.set.Enabled || f.set.Region != "eu_uk" || strings.Join(f.set.Categories, ",") != "analytics,marketing" {
		t.Errorf("saved = %+v", f.set)
	}
	if s.flushed != 1 {
		t.Errorf("page cache cleared %d times, want 1", s.flushed)
	}
	if len(s.events) != 1 || s.events[0].Action != "privacy.settings" {
		t.Errorf("audit = %+v", s.events)
	}
}

func TestPrivacySaveRejectsInvalid(t *testing.T) {
	s, f := newPrivacyRig(t)
	rec := s.do("POST", "/admin/settings/privacy", "owner-token", url.Values{"region": {"mars"}, "text": {strings.Repeat("a", 501)}}, nil)
	if rec.Code != 422 {
		t.Fatalf("status %d, want 422", rec.Code)
	}
	if f.saves != 0 || s.flushed != 0 || len(s.events) != 0 {
		t.Error("an invalid post saved, audited or cleared the cache")
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Choose where the banner shows.") || !strings.Contains(body, "at most 500") {
		t.Error("field errors not shown")
	}
}

func TestPrivacyOwnerOnlyAndUnavailable(t *testing.T) {
	s, _ := newPrivacyRig(t)
	if rec := s.do("GET", "/admin/settings/privacy", "editor-token", nil, nil); rec.Code == 200 {
		t.Error("an editor can open the privacy page")
	}
	s.a.deps.Privacy = nil
	if rec := s.do("GET", "/admin/settings/privacy", "owner-token", nil, nil); rec.Code != 503 {
		t.Errorf("no service: status %d, want 503", rec.Code)
	}
}
