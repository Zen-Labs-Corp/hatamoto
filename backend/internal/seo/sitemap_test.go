package seo

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSitemapZeroKeepsStoredShape(t *testing.T) {
	raw, err := json.Marshal(Defaults())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"sitemap"`) {
		t.Fatalf("default settings must not carry a sitemap key: %s", raw)
	}
	if !(Sitemap{}).IsZero() {
		t.Fatal("zero Sitemap must report IsZero")
	}
}

func TestSitemapRoundTripAndClone(t *testing.T) {
	pri := 0.7
	s := Defaults()
	s.Sitemap = Sitemap{NoAuthor: true, Taxonomy: true, Images: true, MaxURLs: 10, NoStats: true,
		DefaultPriority: &pri, DefaultChangeFreq: "daily", ExcludePaths: []string{"/legal/*", "/x"}}
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.Sitemap.IsZero() || got.Sitemap.MaxURLs != 10 || *got.Sitemap.DefaultPriority != 0.7 || len(got.Sitemap.ExcludePaths) != 2 {
		t.Fatalf("round trip lost data: %+v", got.Sitemap)
	}
	c := got.Clone()
	*c.Sitemap.DefaultPriority = 0.1
	c.Sitemap.ExcludePaths[0] = "/changed"
	if *got.Sitemap.DefaultPriority != 0.7 || got.Sitemap.ExcludePaths[0] != "/legal/*" {
		t.Fatal("Clone shares memory with the original")
	}
	if errs := got.Validate(); len(errs) != 0 {
		t.Fatalf("valid settings rejected: %v", errs)
	}
}

func TestSitemapNilReceiver(t *testing.T) {
	var s *Sitemap
	if s.EffectiveMaxURLs() != MaxSitemapURLs || s.ExcludedPath("/a") {
		t.Fatal("nil receiver must behave like the zero value")
	}
	if (&Sitemap{MaxURLs: 3}).EffectiveMaxURLs() != 3 {
		t.Fatal("explicit max ignored")
	}
}

func TestSitemapExcludedPath(t *testing.T) {
	s := &Sitemap{ExcludePaths: []string{"/legal/*", "/about", "/z*"}}
	cases := map[string]bool{
		"/legal/terms": true, "/legal/": true, "/about": true, "/about/": true,
		"/about/team": false, "/zed": true, "/b": false, "/": false,
	}
	for p, want := range cases {
		if got := s.ExcludedPath(p); got != want {
			t.Errorf("%q: got %v, want %v", p, got, want)
		}
	}
}

func TestValidateSitemap(t *testing.T) {
	bad := func(p float64) *float64 { return &p }
	many := make([]string, 101)
	for i := range many {
		many[i] = "/p" + strings.Repeat("a", i%5) + string(rune('a'+i%26)) + strings.Repeat("b", i/26)
	}
	cases := []struct {
		name string
		s    Sitemap
		bad  bool
	}{
		{"empty", Sitemap{}, false},
		{"prefix", Sitemap{ExcludePaths: []string{"/a*"}}, false},
		{"star mid", Sitemap{ExcludePaths: []string{"/a*b"}}, true},
		{"no slash", Sitemap{ExcludePaths: []string{"a"}}, true},
		{"bare star", Sitemap{ExcludePaths: []string{"*"}}, true},
		{"two stars", Sitemap{ExcludePaths: []string{"/a**"}}, true},
		{"query", Sitemap{ExcludePaths: []string{"/a?b"}}, true},
		{"dotdot", Sitemap{ExcludePaths: []string{"/a/../b"}}, true},
		{"space", Sitemap{ExcludePaths: []string{"/a b"}}, true},
		{"dup", Sitemap{ExcludePaths: []string{"/a", "/a"}}, true},
		{"too long", Sitemap{ExcludePaths: []string{"/" + strings.Repeat("a", 200)}}, true},
		{"101 lines", Sitemap{ExcludePaths: many}, true},
		{"max 0", Sitemap{MaxURLs: 0}, false},
		{"max ok", Sitemap{MaxURLs: 50000}, false},
		{"max high", Sitemap{MaxURLs: 50001}, true},
		{"max negative", Sitemap{MaxURLs: -1}, true},
		{"priority high", Sitemap{DefaultPriority: bad(1.1)}, true},
		{"priority low", Sitemap{DefaultPriority: bad(-0.1)}, true},
		{"priority ok", Sitemap{DefaultPriority: bad(0)}, false},
		{"freq ok", Sitemap{DefaultChangeFreq: "weekly"}, false},
		{"freq bad", Sitemap{DefaultChangeFreq: "sometimes"}, true},
	}
	for _, c := range cases {
		s := Defaults()
		s.Sitemap = c.s
		errs := s.Validate()
		if (len(errs) > 0) != c.bad {
			t.Errorf("%s: errs=%v, want bad=%v", c.name, errs, c.bad)
		}
		for _, e := range errs {
			if !strings.HasPrefix(e.Field, "sitemap[") {
				t.Errorf("%s: unexpected field %q", c.name, e.Field)
			}
		}
	}
}
