package render

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func hreflangView() *PageView {
	return &PageView{
		Site:    SiteView{Name: "Site", Language: "fr"},
		Page:    PageMeta{Title: "Bonjour"},
		BaseURL: "https://x.io",
		Path:    "/fr/bonjour",
		Locale:  "fr",
		Alternates: []Alternate{
			{Locale: "en", Lang: "en", Href: "https://x.io/hello", Default: true},
			{Locale: "fr", Lang: "fr", Href: "https://x.io/fr/bonjour", Current: true},
			{Locale: "es", Lang: "es", Href: "https://x.io/es/", Missing: true},
			{Locale: "nl", Lang: "nl", Href: "https://x.io/nl/hallo"},
		},
		XDefaultHref: "https://x.io/hello",
	}
}

func TestGoldenHeadHreflang(t *testing.T) {
	v := hreflangView()
	f := func(_ context.Context, c HeadContext) HeadContext {
		c.Tags = []HeadTag{
			// duplicate of a computed entry: dropped
			{Kind: "link", Attrs: map[string]string{"rel": "alternate", "hreflang": "nl", "href": "https://x.io/nl/hallo"}},
			// different href: kept
			{Kind: "link", Attrs: map[string]string{"rel": "alternate", "hreflang": "it", "href": "https://x.io/it/ciao"}},
		}
		return c
	}
	got := renderHead(t, buildHeadView(context.Background(), v, f))
	path := filepath.Join("testdata", "golden", "head-hreflang.html")
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatalf("golden mismatch (run with -update)\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestHreflangAbsent(t *testing.T) {
	t.Parallel()
	v := hreflangView()
	v.Alternates = nil
	if h := seoHead(v, false); h.Alt != nil {
		t.Fatalf("one-locale page got alternates: %v", h.Alt)
	}
	v = hreflangView()
	v.Pagination = &Pagination{Page: 2}
	if h := seoHead(v, false); h.Alt != nil {
		t.Fatalf("paginated page got alternates: %v", h.Alt)
	}
}
