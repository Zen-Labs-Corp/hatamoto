package seo

import (
	"reflect"
	"strings"
	"testing"
)

func TestDefaultsAreValid(t *testing.T) {
	if errs := Defaults().Validate(); len(errs) != 0 {
		t.Fatalf("defaults invalid: %v", errs)
	}
}

func TestDecodeFillsMissingKeysAndIgnoresUnknown(t *testing.T) {
	got, err := Decode([]byte(`{"separator":"-","future":{"a":1},"types":{"doc":{"title":"%title% %sep% Docs"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	want := Defaults()
	want.Separator = "-"
	want.Types["doc"] = TypeTemplates{Title: "%title% %sep% Docs"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
}

func TestDecodeEmptyAndNull(t *testing.T) {
	for _, in := range []string{"", "{}", `{"types":null,"taxonomy":null,"variables":null}`} {
		got, err := Decode([]byte(in))
		if err != nil || !reflect.DeepEqual(got, Defaults()) {
			t.Fatalf("%q: got %+v, %v", in, got, err)
		}
	}
}

func TestDecodeBadJSON(t *testing.T) {
	if _, err := Decode([]byte(`{`)); err == nil {
		t.Fatal("want error")
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name  string
		edit  func(*Settings)
		field string
	}{
		{"separator", func(s *Settings) { s.Separator = "~" }, "separator"},
		{"unknown var", func(s *Settings) { s.Home = "%title% %nope%" }, "home"},
		{"undefined custom var", func(s *Settings) { s.Home = "%cv_brand%" }, "home"},
		{"too long", func(s *Settings) { s.Types["a"] = TypeTemplates{Description: strings.Repeat("x", MaxTemplateLen+1)} }, "type[a][description]"},
		{"taxonomy var", func(s *Settings) { s.Taxonomy["tag"] = "%bogus%" }, "taxonomy[tag]"},
		{"bad var name", func(s *Settings) { s.Variables["Bad Name"] = "x" }, "variable[Bad Name]"},
		{"long var value", func(s *Settings) { s.Variables["a"] = strings.Repeat("x", MaxVariableLen+1) }, "variable[a]"},
	}
	for _, tt := range tests {
		s := Defaults()
		tt.edit(&s)
		errs := s.Validate()
		if len(errs) == 0 || errs[0].Field != tt.field {
			t.Errorf("%s: got %v, want field %q", tt.name, errs, tt.field)
		}
	}
}

func TestValidateTooManyVariables(t *testing.T) {
	s := Defaults()
	for i := 0; i <= MaxVariables; i++ {
		s.Variables["v"+strings.Repeat("a", i%20)+string(rune('a'+i/20))] = "x"
	}
	found := false
	for _, e := range s.Validate() {
		found = found || e.Field == "variables"
	}
	if !found {
		t.Fatal("no variables error")
	}
}

func TestValidateAcceptsKnownAndCustomVars(t *testing.T) {
	s := Defaults()
	s.Variables["brand"] = "Acme"
	s.Home = "%title% %sep% %cv_brand% 100% real"
	for _, v := range KnownVars {
		s.Archives.Date += " %" + v + "%"
	}
	if errs := s.Validate(); len(errs) != 0 {
		t.Fatalf("got %v", errs)
	}
}

func TestVarNames(t *testing.T) {
	got := varNames("a %title% 50% off %sep%% %x y% %%")
	want := []string{"title", "sep"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
}

func TestCloneIsIndependent(t *testing.T) {
	a := Defaults()
	b := a.Clone()
	b.Types["x"] = TypeTemplates{}
	if len(a.Types) != 0 {
		t.Fatal("clone shares the map")
	}
}

func TestSocialDecodeWithoutKeyIsZero(t *testing.T) {
	got, err := Decode([]byte(`{"separator":"-"}`))
	if err != nil || !got.Social.IsZero() {
		t.Fatalf("got %+v, %v", got.Social, err)
	}
	if !Defaults().Social.IsZero() {
		t.Fatal("defaults must leave social zero")
	}
}

func TestSocialDecodeRoundTrip(t *testing.T) {
	got, err := Decode([]byte(`{"social":{"image":"/a.png","site_tags":true,"types":{"doc":{"title":"x"}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if got.Social.Image != "/a.png" || !got.Social.SiteTags || got.Social.Types["doc"].Title != "x" || got.Social.IsZero() {
		t.Fatalf("got %+v", got.Social)
	}
}

func TestSocialClonesShareNothing(t *testing.T) {
	s := Defaults()
	s.Social.Types = map[string]SocialType{"doc": {Title: "a"}}
	s.Social.Taxonomy = map[string]string{"tag": "b"}
	c := s.Clone()
	c.Social.Types["doc"] = SocialType{Title: "z"}
	c.Social.Taxonomy["tag"] = "z"
	if s.Social.Types["doc"].Title != "a" || s.Social.Taxonomy["tag"] != "b" {
		t.Fatal("clone shares social maps")
	}
	if z := Defaults().Clone(); z.Social.Types != nil || z.Social.Taxonomy != nil {
		t.Fatal("nil maps must stay nil")
	}
}

func TestSocialValidate(t *testing.T) {
	long := strings.Repeat("a", 301)
	cases := []struct {
		name string
		mut  func(*Social)
		want string // field of the expected error, "" for none
	}{
		{"ok", func(s *Social) {
			s.Image = "https://example.com/a.png"
			s.TwitterSite = "@name_1"
			s.Card = "summary"
			s.FBAppID = "123"
			s.FBPage = "https://example.com/page"
			s.Types = map[string]SocialType{"doc": {Title: "%title% %sep% Docs", Image: "/d.png"}}
			s.Taxonomy = map[string]string{"tag": "%term%"}
		}, ""},
		{"unknown var", func(s *Social) { s.Types = map[string]SocialType{"doc": {Title: "%nope%"}} }, "social[doc][title]"},
		{"long title", func(s *Social) { s.Types = map[string]SocialType{"doc": {Title: long}} }, "social[doc][title]"},
		{"taxonomy var", func(s *Social) { s.Taxonomy = map[string]string{"tag": "%nope%"} }, "social_taxonomy[tag]"},
		{"bad handle", func(s *Social) { s.TwitterSite = "name" }, "social[twitter_site]"},
		{"long handle", func(s *Social) { s.TwitterSite = "@" + strings.Repeat("a", 16) }, "social[twitter_site]"},
		{"bad card", func(s *Social) { s.Card = "huge" }, "social[card]"},
		{"bad app id", func(s *Social) { s.FBAppID = "12a" }, "social[fb_app_id]"},
		{"image not url", func(s *Social) { s.Image = "javascript:alert(1)" }, "social[image]"},
		{"image protocol relative", func(s *Social) { s.Image = "//evil.example/a.png" }, "social[image]"},
		{"image too long", func(s *Social) { s.Image = "/" + strings.Repeat("a", 2048) }, "social[image]"},
		{"type image bad", func(s *Social) { s.Types = map[string]SocialType{"doc": {Image: "nope"}} }, "social[doc][image]"},
		{"fb page bad", func(s *Social) { s.FBPage = "nope" }, "social[fb_page]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := Defaults()
			tc.mut(&s.Social)
			errs := s.Validate()
			if tc.want == "" {
				if len(errs) != 0 {
					t.Fatalf("unexpected %v", errs)
				}
				return
			}
			if len(errs) != 1 || errs[0].Field != tc.want {
				t.Fatalf("want one error on %s, got %v", tc.want, errs)
			}
		})
	}
}

func TestSocialIsZero(t *testing.T) {
	if !(Social{Types: map[string]SocialType{}, Taxonomy: map[string]string{}}).IsZero() {
		t.Fatal("empty maps are zero")
	}
	for _, s := range []Social{{Image: "x"}, {TwitterSite: "@a"}, {Card: "large"}, {FBAppID: "1"}, {FBPage: "x"}, {SiteTags: true}, {Articles: true}, {Types: map[string]SocialType{"a": {}}}, {Taxonomy: map[string]string{"a": ""}}} {
		if s.IsZero() {
			t.Fatalf("%+v should not be zero", s)
		}
	}
}
