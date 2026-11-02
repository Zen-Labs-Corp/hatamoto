package wpimport

import (
	"strings"
	"testing"

	"hatamoto.dev/core/internal/seo"
)

func testSchemaOf(t string) (string, bool) {
	switch t {
	case "post", "page":
		return t, true
	}
	return "", false
}

func applyPlan(t *testing.T, p seoPlan) seo.Settings {
	t.Helper()
	kept, dropped := pruneSEOPlan(seo.Defaults(), p)
	if len(dropped) != 0 {
		t.Fatalf("dropped: %v", dropped)
	}
	s := seo.Defaults()
	for _, f := range kept {
		f.Apply(&s)
	}
	if errs := s.Validate(); len(errs) != 0 {
		t.Fatalf("invalid result: %v", errs)
	}
	return s
}

func anyHas(list []string, sub string) bool {
	for _, s := range list {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func TestSEOMapRankMath(t *testing.T) {
	f, err := parseSEOFixture(t, "rankmath-settings.json")
	if err != nil {
		t.Fatal(err)
	}
	p := buildSEOPlan(f, nil, testSchemaOf, "")
	s := applyPlan(t, p)
	if s.Separator != "|" || s.Types["post"].Title != "%title% %sep% %sitename%" || s.Types["post"].Description != "%excerpt%" {
		t.Errorf("settings = %+v", s)
	}
	if s.Home != "%sitename% %sep% %tagline%" {
		t.Errorf("home = %q", s.Home)
	}
	if !s.Index.NoIndexTaxonomy["category"] {
		t.Error("category noindex missing")
	}
	if s.Social.TwitterSite != "@exampleuser" || s.Verification.Google != "abc123verifytoken" {
		t.Errorf("social/verification = %+v %+v", s.Social, s.Verification)
	}
	if s.Social.Image != "https://example.com/wp-content/uploads/og.png" {
		t.Errorf("image = %q", s.Social.Image)
	}
	if p.Plugin != "rankmath" {
		t.Errorf("plugin = %q", p.Plugin)
	}
}

func TestSEOMapUntranslatableGoesToNotMapped(t *testing.T) {
	f := &SEOFile{Plugin: "rankmath", Values: map[string]any{"pt_post_title": "%unknown_var% %title%"}}
	p := buildSEOPlan(f, nil, testSchemaOf, "")
	if !anyHas(p.NotMapped, "%unknown_var% in Title template: post") {
		t.Errorf("notMapped = %v", p.NotMapped)
	}
	if s := applyPlan(t, p); s.Types["post"].Title != "%title%" {
		t.Errorf("title = %q", s.Types["post"].Title)
	}
}

func TestSEOMapYoast(t *testing.T) {
	f, err := parseSEOFixture(t, "yoast-export.txt")
	if err != nil {
		t.Fatal(err)
	}
	p := buildSEOPlan(f, nil, testSchemaOf, "")
	s := applyPlan(t, p)
	if s.Separator != "-" || s.Types["post"].Title != "%title% %sep% %sitename%" {
		t.Errorf("settings = %+v", s)
	}
	if s.Home != "%sitename% %page% %sep% %tagline%" {
		t.Errorf("home = %q", s.Home)
	}
	if !s.Index.NoIndexTaxonomy["tag"] || s.Social.TwitterSite != "@exampleuser" || !s.Social.SiteTags {
		t.Errorf("index/social = %+v %+v", s.Index, s.Social)
	}
	for _, want := range []string{"%%unknownvar%%", "Noindex on date archives"} {
		if !anyHas(p.NotMapped, want) {
			t.Errorf("notMapped lacks %q: %v", want, p.NotMapped)
		}
	}
	if !anyHas(p.Notes, "%%page%%") {
		t.Errorf("notes = %v", p.Notes)
	}
}

func TestSEOMapYoastSeparatorOutsideSet(t *testing.T) {
	f := &SEOFile{Plugin: "yoast", Values: map[string]any{"separator": "sc-colon"}}
	p := buildSEOPlan(f, nil, testSchemaOf, "")
	if len(p.Fields) != 0 || !anyHas(p.NotMapped, "sc-colon") {
		t.Errorf("plan = %+v", p)
	}
}

func TestSEOMapTSF(t *testing.T) {
	f, err := parseSEOFixture(t, "tsf-settings.json")
	if err != nil {
		t.Fatal(err)
	}
	s := applyPlan(t, buildSEOPlan(f, nil, testSchemaOf, ""))
	if s.Separator != "|" || s.Home != "Example Home" || !s.Index.NoIndexTaxonomy["category"] {
		t.Errorf("settings = %+v", s)
	}
	if s.Social.TwitterSite != "@exampleuser" || s.Verification.Google != "abc123verifytoken" {
		t.Errorf("social = %+v %+v", s.Social, s.Verification)
	}
	if len(s.Types) != 0 {
		t.Errorf("TSF has no per-type templates: %+v", s.Types)
	}
}

func TestSEOMapPruneKeepsTheRest(t *testing.T) {
	long := strings.Repeat("a", 400)
	f := &SEOFile{Plugin: "rankmath", Values: map[string]any{
		"title_separator":      "«", // not in the set: never becomes a field
		"pt_post_title":        long,
		"pt_page_title":        "%title%",
		"twitter_author_names": "this_handle_is_way_too_long",
		"google_verify":        "code",
	}}
	p := buildSEOPlan(f, nil, testSchemaOf, "")
	if !anyHas(p.NotMapped, "Separator «") {
		t.Errorf("notMapped = %v", p.NotMapped)
	}
	kept, dropped := pruneSEOPlan(seo.Defaults(), p)
	if len(dropped) != 2 || !anyHas(dropped, "Title template: post") || !anyHas(dropped, "X handle") {
		t.Errorf("dropped = %v", dropped)
	}
	keys := map[string]bool{}
	for _, k := range kept {
		keys[k.Key] = true
	}
	if !keys["type.page.title"] || !keys["verification.google"] || keys["type.post.title"] || keys["social.twitter_site"] {
		t.Errorf("kept = %v", keys)
	}
}

func TestSEOMapPruneForcedBadSeparator(t *testing.T) {
	p := seoPlan{Fields: []seoField{{Key: "separator", Label: "Separator", Path: "separator",
		Apply: func(s *seo.Settings) { s.Separator = ":" }, Show: func(s seo.Settings) string { return s.Separator }}}}
	kept, dropped := pruneSEOPlan(seo.Defaults(), p)
	if len(kept) != 0 || len(dropped) != 1 {
		t.Errorf("kept %d dropped %v", len(kept), dropped)
	}
}

func TestSEOMapLiveOnly(t *testing.T) {
	live := &seoLive{
		Plugin: "Yoast SEO", SiteName: "Example", TwitterSite: "@exampleuser",
		Verification: map[string]string{"google": "g-code", "facebook": "fb-code"},
		Separator:    "–",
		RobotsExtra:  []string{"User-agent: *", "Disallow: /private/", "Disallow: /administrator/"},
		Noindex:      map[string]bool{"archive": true},
	}
	p := buildSEOPlan(nil, live, testSchemaOf, "")
	if p.Plugin != "yoast" {
		t.Errorf("plugin = %q", p.Plugin)
	}
	s := applyPlan(t, p)
	if s.Separator != "–" || !s.Social.SiteTags || s.Social.TwitterSite != "@exampleuser" ||
		s.Verification.Google != "g-code" || s.Verification.Facebook != "fb-code" || !s.Index.NoIndexArchive {
		t.Errorf("settings = %+v", s)
	}
	if len(s.Robots.ExtraLines) != 2 || !anyHas(p.NotMapped, "/administrator/") {
		t.Errorf("robots = %v notMapped = %v", s.Robots.ExtraLines, p.NotMapped)
	}
}

func TestSEOMapFileWinsOverLive(t *testing.T) {
	f := &SEOFile{Plugin: "rankmath", Values: map[string]any{"title_separator": "—", "google_verify": `<meta name="google-site-verification" content="file-code" />`}}
	live := &seoLive{Separator: "-", Verification: map[string]string{"google": "live-code", "bing": "bing-code"}, Noindex: map[string]bool{}}
	s := applyPlan(t, buildSEOPlan(f, live, testSchemaOf, ""))
	if s.Separator != "—" || s.Verification.Google != "file-code" || s.Verification.Bing != "bing-code" {
		t.Errorf("settings = %+v", s)
	}
}
