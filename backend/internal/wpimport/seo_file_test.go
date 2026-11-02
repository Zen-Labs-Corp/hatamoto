package wpimport

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func parseSEOFixture(t *testing.T, name string) (*SEOFile, error) {
	t.Helper()
	f, err := os.Open("testdata/seo/" + name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	return ParseSEOFile(f)
}

func TestSEOFileParsesFixtures(t *testing.T) {
	cases := []struct {
		file, plugin, key string
		want              any
	}{
		{"rankmath-settings.json", "rankmath", "title_separator", "|"},
		{"rankmath-settings.json", "rankmath", "google_verify", "abc123verifytoken"},
		{"yoast-export.txt", "yoast", "separator", "sc-dash"},
		{"yoast-export.txt", "yoast", "noindex-archive-wpseo", true},
		{"yoast-export.txt", "yoast", "twitter_site", "exampleuser"},
		{"tsf-settings.json", "tsf", "title_separator", "pipe"},
		{"tsf-settings.json", "tsf", "twitter_site", "@exampleuser"},
	}
	for _, c := range cases {
		f, err := parseSEOFixture(t, c.file)
		if err != nil {
			t.Fatalf("%s: %v", c.file, err)
		}
		if f.Plugin != c.plugin {
			t.Errorf("%s: plugin %q, want %q", c.file, f.Plugin, c.plugin)
		}
		if f.Values[c.key] != c.want {
			t.Errorf("%s: %s = %#v, want %#v", c.file, c.key, f.Values[c.key], c.want)
		}
	}
}

func TestSEOFileYoastArraysAndSections(t *testing.T) {
	f, err := parseSEOFixture(t, "yoast-export.txt")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := f.Values["excluded"].([]string); len(got) != 2 || got[1] != "two" {
		t.Errorf("excluded = %#v", f.Values["excluded"])
	}
	if _, ok := f.Values["ignored"]; ok {
		t.Error("key from an unrelated section was merged")
	}
}

func TestSEOFileRankMathBareAndLists(t *testing.T) {
	f, err := ParseSEOFile(strings.NewReader(` {"title_separator":"-","pt_post_title":"%title%","tax_category_robots":["noindex"],"n":5}`))
	if err != nil || f.Plugin != "rankmath" {
		t.Fatalf("bare: %v %+v", err, f)
	}
	if got, _ := f.Values["tax_category_robots"].([]string); len(got) != 1 || got[0] != "noindex" {
		t.Errorf("list = %#v", f.Values["tax_category_robots"])
	}
	if f.Values["n"].(interface{ String() string }).String() != "5" {
		t.Errorf("number = %#v", f.Values["n"])
	}
}

func TestSEOFileRefusals(t *testing.T) {
	if _, err := parseSEOFixture(t, "unsupported.xml"); !errors.Is(err, ErrSEOFileFormat) {
		t.Errorf("xml: %v", err)
	}
	for _, in := range []string{"", "garbage", `{"a":1}`, `{broken`, "PK\x03\x04"} {
		if _, err := ParseSEOFile(strings.NewReader(in)); !errors.Is(err, ErrSEOFileFormat) {
			t.Errorf("%q: %v", in, err)
		}
	}
}

func TestSEOFileOversized(t *testing.T) {
	big := `{"title_separator":"-","pt_post_title":"` + strings.Repeat("a", MaxSEOFileBytes) + `"}`
	_, err := ParseSEOFile(strings.NewReader(big))
	if err == nil || !strings.Contains(err.Error(), "larger than 1 MiB") {
		t.Fatalf("err = %v", err)
	}
	exact := strings.Repeat(" ", MaxSEOFileBytes)
	if _, err := ParseSEOFile(strings.NewReader(exact)); err == nil || strings.Contains(err.Error(), "larger") {
		t.Errorf("exactly 1 MiB should not be 'larger': %v", err)
	}
}

func TestSEOFileRankMathTopLevelSections(t *testing.T) {
	f, err := ParseSEOFile(strings.NewReader(`{"modules":["a"],"general":{"google_verify":"abc"},"titles":{"title_separator":"-"},"sitemap":{"x":"1"},"redirections":{}}`))
	if err != nil || f.Plugin != "rankmath" {
		t.Fatalf("top-level: %v %+v", err, f)
	}
	if f.Values["google_verify"] != "abc" || f.Values["title_separator"] != "-" {
		t.Errorf("values = %v", f.Values)
	}
}

func TestSEOFileRankMathWrapperWithoutValuesIsRefused(t *testing.T) {
	for _, in := range []string{`{"modules":[]}`, `{"modules":[],"settings":{}}`, `{"settings":{"general":{}}}`} {
		if _, err := ParseSEOFile(strings.NewReader(in)); !errors.Is(err, ErrSEOFileFormat) {
			t.Errorf("%s: %v", in, err)
		}
	}
}
