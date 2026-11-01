package admin

import (
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/seo"
)

func goodSocialForm() url.Values {
	return url.Values{
		"image": {"/media/card.png"}, "twitter_site": {"@example"}, "card": {"summary"},
		"fb_app_id": {"12345"}, "fb_page": {"https://www.facebook.com/example"},
		"site_tags":              {"true"},
		"social[service][title]": {"%title% %sep% Services"}, "social[service][image]": {"/media/service.png"},
		"social[recipe][title]": {""}, "social[recipe][image]": {""},
		"social_taxonomy[tag]": {"Tagged %term%"},
	}
}

func TestSEOSocialSaveMergesOnlySocial(t *testing.T) {
	s := newSEORig(t, newFakeSEO())
	s.svc.set.Home = "%sitename%"
	s.svc.set.Types["service"] = seo.TypeTemplates{Title: "Keep %title%"}
	rec := s.do("POST", "/admin/settings/seo/social", "owner-token", goodSocialForm(), nil)
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/settings/seo/social" {
		t.Fatalf("save = %d %s: %s", rec.Code, rec.Header().Get("Location"), rec.Body)
	}
	got := s.svc.set
	want := seo.Social{
		Image: "/media/card.png", TwitterSite: "@example", Card: "summary", FBAppID: "12345",
		FBPage: "https://www.facebook.com/example", SiteTags: true,
		Types:    map[string]seo.SocialType{"service": {Title: "%title% %sep% Services", Image: "/media/service.png"}},
		Taxonomy: map[string]string{"tag": "Tagged %term%"},
	}
	if got.Social.Image != want.Image || got.Social.TwitterSite != want.TwitterSite || got.Social.Card != want.Card ||
		got.Social.FBAppID != want.FBAppID || got.Social.FBPage != want.FBPage || !got.Social.SiteTags || got.Social.Articles {
		t.Fatalf("social = %+v", got.Social)
	}
	if got.Social.Types["service"] != want.Types["service"] || len(got.Social.Types) != 1 {
		t.Errorf("types = %+v", got.Social.Types)
	}
	if got.Social.Taxonomy["tag"] != "Tagged %term%" {
		t.Errorf("taxonomy = %v", got.Social.Taxonomy)
	}
	if got.Home != "%sitename%" || got.Types["service"].Title != "Keep %title%" {
		t.Errorf("a title key was clobbered: %+v", got)
	}
	if s.flushed != 1 {
		t.Errorf("page cache invalidated %d times, want 1", s.flushed)
	}
	if len(s.events) != 1 || s.events[0].Action != "seo.settings" {
		t.Fatalf("audit = %+v", s.events)
	}
}

func TestSEOTitlesSaveKeepsSocial(t *testing.T) {
	s := newSEORig(t, newFakeSEO())
	if rec := s.do("POST", "/admin/settings/seo/social", "owner-token", goodSocialForm(), nil); rec.Code != 303 {
		t.Fatalf("social save = %d", rec.Code)
	}
	if rec := s.do("POST", "/admin/settings/seo/titles", "owner-token", goodTitlesForm(), nil); rec.Code != 303 {
		t.Fatalf("titles save = %d", rec.Code)
	}
	got := s.svc.set
	if got.Social.Image != "/media/card.png" || got.Social.Types["service"].Image != "/media/service.png" || got.Social.Taxonomy["tag"] == "" {
		t.Errorf("Titles save changed social: %+v", got.Social)
	}
	if got.Separator != "-" {
		t.Errorf("titles not saved: %+v", got)
	}
}

func TestSEOSocialUnlistedRowsAreKept(t *testing.T) {
	s := newSEORig(t, newFakeSEO())
	s.svc.set.Social.Types = map[string]seo.SocialType{"service": {Title: "Keep"}}
	form := goodSocialForm()
	form.Del("social[service][title]")
	form.Del("social[service][image]")
	if rec := s.do("POST", "/admin/settings/seo/social", "owner-token", form, nil); rec.Code != 303 {
		t.Fatalf("save = %d", rec.Code)
	}
	if s.svc.set.Social.Types["service"].Title != "Keep" {
		t.Errorf("a row missing from the post was dropped: %+v", s.svc.set.Social.Types)
	}
}

func TestSEOSocialOwnerOnly(t *testing.T) {
	s := newSEORig(t, newFakeSEO())
	for _, p := range []string{"/admin/settings/seo/social", "/admin/settings/seo/social/preview"} {
		for _, m := range []string{"GET", "POST"} {
			if m == "GET" && p != "/admin/settings/seo/social" {
				continue
			}
			if rec := s.do(m, p, "editor-token", goodSocialForm(), nil); rec.Code != 403 {
				t.Errorf("editor %s %s = %d, want 403", m, p, rec.Code)
			}
		}
	}
	if s.svc.merges != 0 {
		t.Error("editor save reached the service")
	}
}

func TestSEOSocialUnavailable(t *testing.T) {
	s := newSEORig(t, nil)
	if rec := s.do("GET", "/admin/settings/seo/social", "owner-token", nil, nil); rec.Code != 503 {
		t.Errorf("GET without service = %d, want 503", rec.Code)
	}
}

func TestSEOSocialRejectsInvalidInput(t *testing.T) {
	for name, edit := range map[string]func(url.Values){
		"bad handle":       func(f url.Values) { f.Set("twitter_site", "example") },
		"unknown variable": func(f url.Values) { f.Set("social[service][title]", "%nope%") },
		"bad card":         func(f url.Values) { f.Set("card", "huge") },
		"bad image":        func(f url.Values) { f.Set("image", "javascript:alert(1)") },
	} {
		s := newSEORig(t, newFakeSEO())
		form := goodSocialForm()
		edit(form)
		rec := s.do("POST", "/admin/settings/seo/social", "owner-token", form, nil)
		if rec.Code != 422 {
			t.Errorf("%s: status %d, want 422", name, rec.Code)
			continue
		}
		body := rec.Body.String()
		if !strings.Contains(body, "problem") || !strings.Contains(body, "hm-field--invalid") {
			t.Errorf("%s: no error shown", name)
		}
		if s.svc.merges != 0 || s.flushed != 0 {
			t.Errorf("%s: something was saved", name)
		}
	}
}

func TestSEOSocialPageAndPreview(t *testing.T) {
	s := newSEORig(t, newFakeSEO())
	s.svc.set.Social.Types = map[string]seo.SocialType{"service": {Title: "%title% %sep% Services"}}
	body := s.get(t, "/admin/settings/seo/social")
	for _, name := range []string{"image", "twitter_site", "card", "fb_app_id", "fb_page", "site_tags", "articles", "social[service][title]", "social[service][image]", "social_taxonomy[tag]"} {
		if seoControlTag(body, name) == "" {
			t.Errorf("page lacks control %q", name)
		}
	}
	form := goodSocialForm()
	rec := s.do("POST", "/admin/settings/seo/social/preview", "owner-token", form, nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Sample title "+seo.DefaultSeparator+" Services") || !strings.Contains(rec.Body.String(), "/media/service.png") {
		t.Errorf("preview = %d, lacks the typed template or image", rec.Code)
	}
	if s.svc.merges != 0 {
		t.Error("preview saved")
	}
}
