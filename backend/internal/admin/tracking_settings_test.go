package admin

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/admin/a11y"
	"hatamoto.dev/core/internal/jsonx"
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
		if !strings.Contains(body, `id="tracking-`+v.ID+`"`) || !strings.Contains(body, `href="/admin/settings/tracking/tags/`+v.ID+`"`) || !strings.Contains(body, `id="tracking-`+v.ID+`-id"`) {
			t.Errorf("vendor %s has no card, tag page link or drawer field", v.ID)
		}
	}
	if !strings.Contains(body, "Skip tags for signed-in users") || !strings.Contains(body, `href="/admin/settings/privacy">Cookie consent</a>`) || strings.Contains(body, "Moved into core") {
		t.Error("All tags card wrong or a notice shows by default")
	}
	compareGolden(t, "pages/settings-tracking.html", normalizePage(body))
}

// Ad platform tags that wait for analytics consent are still filed under ads.
func TestTrackingGroupsByKindOfTag(t *testing.T) {
	s, _ := newTrackingRig(t)
	body := s.get(t, "/admin/settings/tracking")
	ads := strings.Index(body, ">Ads and marketing<")
	chat := strings.Index(body, ">Chat and support<")
	if ads < 0 || chat < 0 {
		t.Fatal("group headings missing")
	}
	for _, id := range []string{"uet", "linkedin"} {
		if i := strings.Index(body, `id="tracking-`+id+`"`); i < ads || i > chat {
			t.Errorf("%s is not under Ads and marketing", id)
		}
	}
	if i := strings.Index(body, `id="tracking-clarity"`); i > ads {
		t.Error("clarity left Analytics")
	}
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
	raw, _ := jsonx.Marshal(s.events[0].Detail)
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
	}, nil)
	if rec.Code != 422 {
		t.Fatalf("status %d, want 422", rec.Code)
	}
	if f.saves != 0 || s.flushed != 0 || len(s.events) != 0 {
		t.Error("an invalid post saved, audited or cleared the cache")
	}
	body := rec.Body.String()
	for _, want := range []string{`href="#tracking-ga4-id"`, `id="tracking-ga4-id-error"`, `aria-invalid="true"`, `value="UA-123"`, ">Turn on<"} {
		if !strings.Contains(body, want) {
			t.Errorf("response lacks %q", want)
		}
	}
	if strings.Contains(body, ">Save<") {
		t.Error("a tag that was off offers Save instead of Turn on after a bad id")
	}
}

// The error page is the failing tag's own page and lists only its fields.
func TestTrackingSaveErrorShowsTheFailingTagOnly(t *testing.T) {
	s, f := newTrackingRig(t)
	f.set.Vendors["gads"] = tags.VendorState{Enabled: true, ID: "AW-123456789"}
	rec := s.do("POST", "/admin/settings/tracking", "owner-token", url.Values{
		"gads_present": {"1"}, "gads_enabled": {"true"}, "gads_id": {"AW-123456789"}, "gads_aux": {"bad label"},
	}, nil)
	body := rec.Body.String()
	if rec.Code != 422 || !strings.Contains(body, `href="#tracking-gads-aux"`) || !strings.Contains(body, ">Save<") {
		t.Fatalf("status %d; want the Google Ads page with the label error and Save", rec.Code)
	}
	if strings.Contains(body, `id="tracking-ga4-id"`) {
		t.Error("the error page shows other tags")
	}
}

func TestTrackingTagPage(t *testing.T) {
	s, f := newTrackingRig(t)
	f.set.Vendors["meta"] = tags.VendorState{ID: "123456789012345"}
	body := s.get(t, "/admin/settings/tracking/tags/meta")
	for _, want := range []string{"Meta Pixel", `value="123456789012345"`, "connect.facebook.net", ">Turn on<", `href="/admin/settings/tracking"`} {
		if !strings.Contains(body, want) {
			t.Errorf("tag page lacks %q", want)
		}
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("tag page a11y: %v", issues)
	}
	if rec := s.do("GET", "/admin/settings/tracking/tags/nope", "owner-token", nil, nil); rec.Code != 404 {
		t.Errorf("unknown tag = %d, want 404", rec.Code)
	}
	if rec := s.do("GET", "/admin/settings/tracking/tags/meta", "editor-token", nil, nil); rec.Code == 200 {
		t.Error("an editor can open a tag page")
	}
}

// A card's one-click toggle posts no id; the stored id stays.
func TestTrackingToggleKeepsTheID(t *testing.T) {
	s, f := newTrackingRig(t)
	f.set.Vendors["meta"] = tags.VendorState{Enabled: true, ID: "123456789012345"}
	rec := s.do("POST", "/admin/settings/tracking", "owner-token", url.Values{"meta_present": {"1"}, "meta_enabled": {"false"}}, nil)
	if rec.Code != 303 {
		t.Fatalf("status %d", rec.Code)
	}
	if st := f.set.Vendors["meta"]; st.Enabled || st.ID != "123456789012345" {
		t.Errorf("meta = %+v, want off with its id kept", st)
	}
	s.do("POST", "/admin/settings/tracking", "owner-token", url.Values{"meta_present": {"1"}, "meta_enabled": {"true"}}, nil)
	if st := f.set.Vendors["meta"]; !st.Enabled || st.ID != "123456789012345" {
		t.Errorf("meta = %+v, want on with its id kept", st)
	}
}

func TestTrackingSavedFlash(t *testing.T) {
	for _, c := range []struct {
		en, dis, ch []string
		want        string
	}{
		{[]string{"ga4"}, nil, nil, "Google Analytics 4 is on. It loads after a visitor accepts cookies."},
		{nil, []string{"meta"}, nil, "Meta Pixel is off."},
		{nil, nil, []string{"gtm"}, "Google Tag Manager saved."},
		{[]string{"ga4"}, []string{"meta"}, nil, "Tracking settings saved."},
		{nil, nil, nil, "Tracking settings saved."},
	} {
		if got := trackingSavedFlash(c.en, c.dis, c.ch); got != c.want {
			t.Errorf("flash(%v,%v,%v) = %q, want %q", c.en, c.dis, c.ch, got, c.want)
		}
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
