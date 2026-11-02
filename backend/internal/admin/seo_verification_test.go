package admin

import (
	"fmt"
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/seo"
)

func goodVerificationForm() url.Values {
	return url.Values{
		"google": {" abc "}, "bing": {"def"}, "yandex": {""}, "baidu": {""}, "pinterest": {"pin"}, "facebook": {""},
		"custom[0][name]": {"my-tag"}, "custom[0][content]": {"value"},
		"custom[1][name]": {""}, "custom[1][content]": {""},
		"custom[2][name]": {"other:tag"}, "custom[2][content]": {"x"},
	}
}

func TestSEOVerificationSave(t *testing.T) {
	s := newSEORig(t, newFakeSEO())
	s.svc.set.Home = "%sitename%"
	s.svc.set.Robots.NoArchive = true
	rec := s.do("POST", "/admin/settings/seo/verification", "owner-token", goodVerificationForm(), nil)
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/settings/seo/verification" {
		t.Fatalf("save = %d %s: %s", rec.Code, rec.Header().Get("Location"), rec.Body)
	}
	got := s.svc.set.Verification
	if got.Google != "abc" || got.Bing != "def" || got.Pinterest != "pin" || got.Yandex != "" {
		t.Errorf("codes = %+v", got)
	}
	if len(got.Custom) != 2 || got.Custom[0] != (seo.MetaTag{Name: "my-tag", Content: "value"}) || got.Custom[1].Name != "other:tag" {
		t.Errorf("custom = %+v", got.Custom)
	}
	if s.svc.set.Home != "%sitename%" || !s.svc.set.Robots.NoArchive {
		t.Errorf("another tab's keys were clobbered: %+v", s.svc.set)
	}
	if s.flushed != 1 || len(s.events) != 1 || s.events[0].Action != "seo.settings" {
		t.Errorf("flushed %d, audit %+v", s.flushed, s.events)
	}
}

func TestSEOVerificationPageListsFieldsAndRows(t *testing.T) {
	s := newSEORig(t, newFakeSEO())
	s.svc.set.Verification = seo.Verification{Google: "g-code", Custom: []seo.MetaTag{{Name: "a", Content: "b"}}}
	body := s.get(t, "/admin/settings/seo/verification")
	for _, want := range []string{
		`name="google"`, `value="g-code"`, `name="bing"`, `name="yandex"`, `name="baidu"`, `name="pinterest"`, `name="facebook"`,
		`name="custom[0][name]"`, `name="custom[3][content]"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(body, `name="custom[4][name]"`) {
		t.Error("more blank rows than the three offered")
	}
}

func TestSEOVerificationRejectsBadInput(t *testing.T) {
	for name, tc := range map[string]struct {
		key, val, want string
	}{
		"tag name":     {"custom[0][name]", "Bad Name", "lowercase letters"},
		"long code":    {"google", strings.Repeat("a", 301), "At most 300 characters."},
		"control char": {"bing", "a\x01b", "control characters"},
	} {
		s := newSEORig(t, newFakeSEO())
		form := goodVerificationForm()
		form.Set(tc.key, tc.val)
		rec := s.do("POST", "/admin/settings/seo/verification", "owner-token", form, nil)
		if rec.Code != 422 || !strings.Contains(rec.Body.String(), tc.want) {
			t.Errorf("%s: %d, want 422 with %q", name, rec.Code, tc.want)
		}
		if s.svc.merges != 0 {
			t.Errorf("%s: a rejected save reached the store", name)
		}
	}
	// More than ten custom tags are refused, not trimmed.
	s := newSEORig(t, newFakeSEO())
	form := goodVerificationForm()
	for i := 3; i < 12; i++ {
		p := fmt.Sprintf("custom[%d]", i)
		form.Set(p+"[name]", fmt.Sprintf("t%d", i))
		form.Set(p+"[content]", "v")
	}
	if rec := s.do("POST", "/admin/settings/seo/verification", "owner-token", form, nil); rec.Code != 422 {
		t.Errorf("eleven custom tags = %d, want 422", rec.Code)
	}
}

func TestSEOVerificationOwnerOnly(t *testing.T) {
	s := newSEORig(t, newFakeSEO())
	for _, m := range []string{"GET", "POST"} {
		if rec := s.do(m, "/admin/settings/seo/verification", "editor-token", goodVerificationForm(), nil); rec.Code != 403 {
			t.Errorf("editor %s = %d, want 403", m, rec.Code)
		}
	}
	if s.svc.merges != 0 {
		t.Error("editor save reached the service")
	}
}

func TestSEOTitlesSaveKeepsVerification(t *testing.T) {
	s := newSEORig(t, newFakeSEO())
	if rec := s.do("POST", "/admin/settings/seo/verification", "owner-token", goodVerificationForm(), nil); rec.Code != 303 {
		t.Fatalf("verification save = %d", rec.Code)
	}
	if rec := s.do("POST", "/admin/settings/seo/titles", "owner-token", goodTitlesForm(), nil); rec.Code != 303 {
		t.Fatalf("titles save = %d", rec.Code)
	}
	if got := s.svc.set.Verification; got.Google != "abc" || len(got.Custom) != 2 {
		t.Errorf("a Titles save changed verification: %+v", got)
	}
}
