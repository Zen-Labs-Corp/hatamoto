package admin

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/tags"
)

type fakeTracking struct {
	set   tags.Settings
	saves int
}

func (f *fakeTracking) Get(context.Context) (tags.Settings, error) { return f.set, nil }
func (f *fakeTracking) Save(_ context.Context, s tags.Settings) error {
	s = s.Normalize()
	if errs := s.Validate(); len(errs) > 0 {
		return &tags.ValidationError{Errors: errs}
	}
	f.set = s
	f.saves++
	return nil
}

func newTrackingRig(t *testing.T) (*seoRig, *fakeTracking) {
	t.Helper()
	s := newSEORig(t, newFakeSEO())
	f := &fakeTracking{set: tags.Defaults()}
	s.a.deps.Tracking = f
	return s, f
}

func TestTrackingPageGoldenDefault(t *testing.T) {
	s, _ := newTrackingRig(t)
	body := s.get(t, "/admin/settings/tracking")
	for _, v := range tags.Vendors {
		if !strings.Contains(body, `id="tracking-`+v.ID+`-id"`) || !strings.Contains(body, `id="tracking-`+v.ID+`-enabled"`) {
			t.Errorf("vendor %s has no switch or id field", v.ID)
		}
	}
	if !strings.Contains(body, "Do not load tags for signed-in users") || strings.Contains(body, "Moved into core") {
		t.Error("shared switch missing or a notice shows by default")
	}
	compareGolden(t, "pages/settings-tracking.html", normalizePage(body))
}

func TestTrackingPageGoldenGA4OnWithNotice(t *testing.T) {
	s, f := newTrackingRig(t)
	f.set.Vendors["ga4"] = tags.VendorState{Enabled: true, ID: "G-ABC123XYZ9"}
	f.set.MovedFrom = []string{"gtm", "google-analytics"}
	s.a.deps.LegacyTrackingPlugins = func(context.Context) []string { return []string{"gtm"} }
	body := s.get(t, "/admin/settings/tracking")
	for _, want := range []string{"Moved into core", "Google Tag Manager and Google Analytics plugins were replaced", "An old tracking plugin is still installed", `value="G-ABC123XYZ9"`} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	compareGolden(t, "pages/settings-tracking-ga4-notice.html", normalizePage(body))
}

func TestTrackingSaveAuditsIdsOnlyAndClearsCache(t *testing.T) {
	s, f := newTrackingRig(t)
	f.set.Vendors["gtm"] = tags.VendorState{Enabled: true, ID: "GTM-OLD123"}
	form := url.Values{
		"exclude_signed_in": {"false"},
		"ga4_present":       {"1"}, "ga4_enabled": {"true"}, "ga4_id": {" g-abc123xyz9 "},
		"gtm_present": {"1"}, "gtm_enabled": {"true"}, "gtm_id": {"GTM-NEW456"},
		"meta_present": {"1"}, "meta_enabled": {"false"}, "meta_id": {""},
	}
	rec := s.do("POST", "/admin/settings/tracking", "owner-token", form, nil)
	if rec.Code != 303 {
		t.Fatalf("save = %d: %s", rec.Code, rec.Body.String())
	}
	if f.set.ExcludeSignedIn || f.set.Vendors["ga4"].ID != "G-ABC123XYZ9" || !f.set.Vendors["ga4"].Enabled || f.set.Vendors["gtm"].ID != "GTM-NEW456" {
		t.Errorf("saved = %+v", f.set)
	}
	if s.flushed != 1 {
		t.Errorf("page cache cleared %d times, want 1", s.flushed)
	}
	if len(s.events) != 1 || s.events[0].Action != "tracking.settings" || s.events[0].Target != "tracking" {
		t.Fatalf("audit = %+v", s.events)
	}
	raw, _ := json.Marshal(s.events[0].Detail)
	if got := string(raw); got != `{"changed":["gtm"],"disabled":[],"enabled":["ga4"],"exclude_signed_in":false}` {
		t.Errorf("audit detail = %s", got)
	}
	if strings.Contains(string(raw), "G-ABC") || strings.Contains(string(raw), "GTM-") {
		t.Error("audit detail carries an id")
	}
}

func TestTrackingPostKeepsVendorsNotCarried(t *testing.T) {
	s, f := newTrackingRig(t)
	f.set.Vendors["ga4"] = tags.VendorState{Enabled: true, ID: "G-ABC123XYZ9"}
	rec := s.do("POST", "/admin/settings/tracking", "owner-token", url.Values{"exclude_signed_in": {"true"}}, nil)
	if rec.Code != 303 || !f.set.Vendors["ga4"].Enabled {
		t.Fatalf("status %d, ga4 = %+v", rec.Code, f.set.Vendors["ga4"])
	}
}

func TestTrackingSaveRejectsInvalidIDNothingSaved(t *testing.T) {
	s, f := newTrackingRig(t)
	rec := s.do("POST", "/admin/settings/tracking", "owner-token", url.Values{
		"ga4_present": {"1"}, "ga4_enabled": {"true"}, "ga4_id": {"UA-123"},
		"gtm_present": {"1"}, "gtm_enabled": {"true"}, "gtm_id": {""},
		"gads_present": {"1"}, "gads_enabled": {"false"}, "gads_id": {"AW-123456789"}, "gads_aux": {"bad label"},
	}, nil)
	if rec.Code != 422 {
		t.Fatalf("status %d, want 422", rec.Code)
	}
	if f.saves != 0 || s.flushed != 0 || len(s.events) != 0 {
		t.Error("an invalid post saved, audited or cleared the cache")
	}
	body := rec.Body.String()
	for _, want := range []string{`href="#tracking-ga4-id"`, `href="#tracking-gtm-id"`, `href="#tracking-gads-aux"`, `id="tracking-ga4-id-error"`, "Enter the container id to turn this tag on.", `aria-invalid="true"`} {
		if !strings.Contains(body, want) {
			t.Errorf("response lacks %q", want)
		}
	}
	if !strings.Contains(body, `value="UA-123"`) {
		t.Error("the rejected value is not kept in the field")
	}
}

func TestTrackingDismissNoticeAudits(t *testing.T) {
	s, f := newTrackingRig(t)
	f.set.MovedFrom = []string{"gtm"}
	rec := s.do("POST", "/admin/settings/tracking/notice", "owner-token", url.Values{}, nil)
	if rec.Code != 303 || !f.set.NoticeDismissed {
		t.Fatalf("status %d, dismissed %v", rec.Code, f.set.NoticeDismissed)
	}
	if len(s.events) != 1 || s.events[0].Action != "tracking.notice_dismiss" {
		t.Errorf("audit = %+v", s.events)
	}
	if body := s.get(t, "/admin/settings/tracking"); strings.Contains(body, "Moved into core") {
		t.Error("dismissed notice still shows")
	}
	// A second dismissal is a quiet no-op.
	s.do("POST", "/admin/settings/tracking/notice", "owner-token", url.Values{}, nil)
	if len(s.events) != 1 {
		t.Errorf("second dismissal audited: %+v", s.events)
	}
}

func TestTrackingOwnerOnlyAndUnavailable(t *testing.T) {
	s, _ := newTrackingRig(t)
	for _, m := range []string{"GET", "POST"} {
		if rec := s.do(m, "/admin/settings/tracking", "editor-token", url.Values{}, nil); rec.Code == 200 || rec.Code == 303 {
			t.Errorf("an editor can %s the tracking page: %d", m, rec.Code)
		}
	}
	s.a.deps.Tracking = nil
	if rec := s.do("GET", "/admin/settings/tracking", "owner-token", nil, nil); rec.Code != 503 {
		t.Errorf("no service: status %d, want 503", rec.Code)
	}
}

func TestTrackingAdSenseLinksToTheAdsTxtTab(t *testing.T) {
	s, f := newTrackingRig(t)
	if body := s.get(t, "/admin/settings/tracking"); !strings.Contains(body, `href="/admin/settings/seo/ads-txt"`) {
		t.Error("no plain ads.txt link without a saved AdSense id")
	}
	f.set.Vendors["adsense"] = tags.VendorState{Enabled: true, ID: "ca-pub-1234567890"}
	if body := s.get(t, "/admin/settings/tracking"); !strings.Contains(body, `href="/admin/settings/seo/ads-txt?add=pub-1234567890"`) {
		t.Error("the ads.txt link does not carry the publisher id")
	}
}

func TestTrackingWarnsWhenAnEnabledTagsCategoryIsNotAsked(t *testing.T) {
	s, f := newTrackingRig(t)
	f.set.Vendors["gtm"] = tags.VendorState{Enabled: true, ID: "GTM-ABC123"}
	f.set.Vendors["ga4"] = tags.VendorState{Enabled: true, ID: "G-ABC123XYZ9"}
	s.a.deps.TrackingNeverLoads = func(context.Context) func(string) bool {
		return func(category string) bool { return category == "marketing" }
	}
	body := s.get(t, "/admin/settings/tracking")
	if !strings.Contains(body, "Some tags will never load") || !strings.Contains(body, "Google Tag Manager") {
		t.Errorf("no warning for the unasked marketing tag")
	}
	s.a.deps.TrackingNeverLoads = func(context.Context) func(string) bool { return func(string) bool { return false } }
	if strings.Contains(s.get(t, "/admin/settings/tracking"), "Some tags will never load") {
		t.Errorf("warned although every category is asked")
	}
}
