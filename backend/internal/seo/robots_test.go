package seo

import (
	"encoding/json"
	"strings"
	"testing"
)

func intp(n int) *int { return &n }

func TestParseDirectivesAndString(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"noindex, follow", "noindex"},
		{"nofollow, max-snippet:50", "nofollow, max-snippet:50"},
		{"NOINDEX,Nofollow", "noindex, nofollow"},
		{"max-image-preview:large, unavailable_after: 25 Jun 2030", "max-image-preview:large, unavailable_after: 25 Jun 2030"},
		{"max-snippet:abc", "max-snippet:abc"},
	}
	for _, c := range cases {
		if got := ParseDirectives(c.in).String(); got != c.want {
			t.Errorf("%q: got %q, want %q", c.in, got, c.want)
		}
	}
}

func TestComposeRobots(t *testing.T) {
	def := &Robots{NoArchive: true, MaxImagePreview: "large"}
	cases := []struct {
		name    string
		entry   string
		def     *Robots
		noindex bool
		want    string
	}{
		{"nothing saved", "", nil, false, ""},
		{"zero defaults", "", &Robots{}, false, ""},
		{"setting noindex", "", nil, true, "noindex,follow"},
		{"legacy noindex, follow", "noindex, follow", nil, false, "noindex,follow"},
		{"entry noindex nofollow", "noindex, nofollow", nil, false, "noindex,nofollow"},
		{"entry extras", "nofollow, max-snippet:50", nil, false, "index,nofollow,max-snippet:50"},
		{"defaults only", "", def, false, "index,noarchive,max-image-preview:large"},
		{"entry overrides default", "max-image-preview:none, nosnippet", def, false, "index,noarchive,nosnippet,max-image-preview:none"},
		{"noindex ignores defaults", "", def, true, "noindex,follow"},
		{"max-snippet zero", "max-snippet:0", nil, false, "index,max-snippet:0"},
	}
	for _, c := range cases {
		if got := ComposeRobots(ParseDirectives(c.entry), c.def, c.noindex); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestEntryNoIndex(t *testing.T) {
	cases := []struct {
		name, meta, attrs string
		want              bool
	}{
		{"empty", ``, ``, false},
		{"metadata flag", `{"no_index":true}`, `{}`, true},
		{"metadata string flag", `{"no_index":"true"}`, `{}`, true},
		{"attrs flag", `{}`, `{"no_index":true}`, true},
		{"robots token", `{}`, `{"seo":{"robots":"nofollow, NoIndex"}}`, true},
		{"robots other", `{}`, `{"seo":{"robots":"nofollow"}}`, false},
		{"false flags", `{"no_index":false}`, `{"no_index":false}`, false},
		{"invalid metadata, attrs ok", `{`, `{"no_index":true}`, true},
		{"invalid attrs", `{}`, `{`, false},
	}
	for _, c := range cases {
		if got := EntryNoIndex(json.RawMessage(c.meta), json.RawMessage(c.attrs)); got != c.want {
			t.Errorf("%s: got %v", c.name, got)
		}
	}
}

func TestNoIndexView(t *testing.T) {
	var nilS *Settings
	s := &Settings{Index: Index{
		NoIndexTypes:    map[string]bool{"release": true, "doc": false},
		NoIndexTaxonomy: map[string]bool{"tag": true},
		NoIndexAuthor:   true,
	}}
	idx := &Settings{Index: Index{IndexSearch: true, NoIndexArchive: true}}
	cases := []struct {
		s                    *Settings
		kind, schema, termKd string
		want                 bool
	}{
		{nilS, "search", "", "", true},
		{nilS, "single", "release", "", false},
		{nilS, "author", "", "", false},
		{s, "single", "release", "", true},
		{s, "page", "release", "", true},
		{s, "home", "release", "", true},
		{s, "single", "doc", "", false},
		{s, "taxonomy", "", "tag", true},
		{s, "taxonomy", "", "category", false},
		{s, "author", "", "", true},
		{s, "archive", "release", "", false},
		{s, "search", "", "", true},
		{idx, "search", "", "", false},
		{idx, "archive", "x", "", true},
		{s, "notfound", "", "", false},
	}
	for _, c := range cases {
		if got := c.s.NoIndexView(c.kind, c.schema, c.termKd); got != c.want {
			t.Errorf("%v %s/%s/%s: got %v", c.s != nil, c.kind, c.schema, c.termKd, got)
		}
	}
	if got := s.NoIndexTypeSlugs(); len(got) != 1 || got[0] != "release" {
		t.Errorf("slugs = %v", got)
	}
	if nilS.NoIndexType("x") || nilS.NoIndexTypeSlugs() != nil {
		t.Error("nil settings must report nothing")
	}
}

func TestValidateRobotsLines(t *testing.T) {
	ok := []string{"User-agent: Foo", "Disallow: /private", "", "# note", "Crawl-delay: 5", "Sitemap: https://x.io/s.xml", "Disallow:"}
	if errs := ValidateRobotsLines(ok, "/admin-x"); len(errs) != 0 {
		t.Fatalf("valid lines rejected: %v", errs)
	}
	bad := map[string][]string{
		"unknown directive": {"Host: x.io"},
		"no colon":          {"hello"},
		"ftp sitemap":       {"Sitemap: ftp://x.io/s"},
		"control":           {"Disallow: /a\x00b"},
		"too long":          {"Disallow: /" + strings.Repeat("a", 200)},
		"admin path":        {"Disallow: /Admin-X/foo"},
		"empty agent":       {"User-agent:"},
		"too many":          make([]string, MaxRobotsLines+1),
	}
	for name, lines := range bad {
		errs := ValidateRobotsLines(lines, "/admin-x")
		if len(errs) != 1 || errs[0].Field != "robots[extra]" {
			t.Errorf("%s: errs = %v", name, errs)
		}
	}
}

func TestVerificationMetas(t *testing.T) {
	if got := (Verification{}).Metas(); len(got) != 0 {
		t.Fatalf("zero value emits %v", got)
	}
	v := Verification{Google: "g", Facebook: "f", Custom: []MetaTag{{"x-a", "1"}, {"x-b", ""}}}
	got := v.Metas()
	want := []MetaTag{{"google-site-verification", "g"}, {"facebook-domain-verification", "f"}, {"x-a", "1"}}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] got %v, want %v", i, got[i], want[i])
		}
	}
}

func TestValidateRobotsAndVerificationSettings(t *testing.T) {
	s := Defaults()
	s.Robots = Robots{MaxSnippet: intp(-2), MaxImagePreview: "huge", ExtraLines: []string{"nope"}}
	s.Verification.Google = strings.Repeat("a", MaxMetaContentLen+1)
	s.Verification.Custom = []MetaTag{{"Bad Name", "x"}}
	fields := map[string]bool{}
	for _, e := range s.Validate() {
		fields[e.Field] = true
	}
	for _, f := range []string{"robots[max_snippet]", "robots[max_image_preview]", "robots[extra]", "verification[google]", "verification[custom][0][name]"} {
		if !fields[f] {
			t.Errorf("missing error for %s (got %v)", f, fields)
		}
	}
	s = Defaults()
	s.Verification.Custom = make([]MetaTag, MaxCustomMetaTags+1)
	for i := range s.Verification.Custom {
		s.Verification.Custom[i] = MetaTag{"a", "b"}
	}
	found := false
	for _, e := range s.Validate() {
		found = found || e.Field == "verification[custom]"
	}
	if !found {
		t.Error("custom tag cap not enforced")
	}
}
