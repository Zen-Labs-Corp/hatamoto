package render

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"html/template"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// update rewrites testdata/golden files (shared by every golden test here).
var update = flag.Bool("update", false, "rewrite golden files")

func loadFixtureView(t *testing.T, name string) *PageView {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "fixtures", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var v PageView
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	return &v
}

func renderHead(t *testing.T, h headView) string {
	t.Helper()
	src, err := os.ReadFile("themes/default/partials/head.html")
	if err != nil {
		t.Fatal(err)
	}
	tpl, err := template.New("head").Funcs(baseFuncs()).Parse(string(src))
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := tpl.ExecuteTemplate(&b, "head", h); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestGoldenHead(t *testing.T) {
	t.Run("head", func(t *testing.T) {
		got := renderHead(t, buildHeadView(context.Background(), loadFixtureView(t, "head"), nil))
		path := filepath.Join("testdata", "golden", "head.html")
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
	})
}

func TestSEOTitleSiteSuffix(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		page PageMeta
		want string
	}{
		{"meta title gets site suffix", PageMeta{Title: "Exact", Name: "N"}, "Exact | Site"},
		{"falls back to name", PageMeta{Name: "N"}, "N | Site"},
		{"already suffixed", PageMeta{Title: "X | Site"}, "X | Site"},
		{"falls back to site", PageMeta{}, "Site"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := seoHead(&PageView{Site: SiteView{Name: "Site"}, Page: tt.page, BaseURL: "https://x.io", Path: "/p"}, false)
			if h.Title != tt.want {
				t.Fatalf("title %q", h.Title)
			}
		})
	}
}

func TestSEOCanonicalRobotsAndTwitter(t *testing.T) {
	t.Parallel()
	base := PageView{BaseURL: "https://x.io/", Path: "/p"}

	h := seoHead(&base, false)
	if h.Canonical != "https://x.io/p" {
		t.Fatalf("canonical %q", h.Canonical)
	}
	if h.Robots != "" {
		t.Fatalf("robots %q", h.Robots)
	}
	if h.Twitter[0] != (metaPair{"twitter:card", "summary"}) {
		t.Fatalf("twitter %v", h.Twitter)
	}

	v := base
	v.Page = PageMeta{CanonicalURL: "https://other.io/c", NoIndex: true, OGImage: "https://i/x.png"}
	h = seoHead(&v, false)
	if h.Canonical != "https://other.io/c" || h.Robots != "noindex,follow" {
		t.Fatalf("canonical=%q robots=%q", h.Canonical, h.Robots)
	}
	if h.Twitter[0] != (metaPair{"twitter:card", "summary_large_image"}) {
		t.Fatalf("twitter %v", h.Twitter)
	}

	v = base
	v.Preview = true
	if got := seoHead(&v, false).Robots; got != "noindex,follow" {
		t.Fatalf("preview must be noindex: %q", got)
	}
}

func TestSEOSiteGraphLanguageAndContact(t *testing.T) {
	t.Parallel()
	h := seoHead(&PageView{BaseURL: "https://x", Site: SiteView{
		Name: "X", Language: "en-AU", Phone: "+61 2 5550 0100", Hours: "Mon-Fri",
		Address: Address{Street: "1 Example St", City: "Sydney", Country: "AU"},
	}}, false)
	var graph string
	for _, s := range h.Scripts {
		graph += string(s)
	}
	for _, want := range []string{
		`"inLanguage":"en-AU"`, `"telephone":"+61 2 5550 0100"`, `"@type":"ContactPoint"`,
		`"hoursAvailable":"Mon-Fri"`, `"@type":"PostalAddress"`, `"addressLocality":"Sydney"`, `"addressCountry":"AU"`,
	} {
		if !strings.Contains(graph, want) {
			t.Errorf("site graph missing %s:\n%s", want, graph)
		}
	}
	bare := seoHead(&PageView{BaseURL: "https://x", Site: SiteView{Name: "X"}}, false)
	for _, s := range bare.Scripts {
		for _, banned := range []string{"telephone", "PostalAddress", "ContactPoint"} {
			if strings.Contains(string(s), banned) {
				t.Errorf("unset contact details must not emit %s: %s", banned, s)
			}
		}
	}
}

func TestSEOJSONLD(t *testing.T) {
	t.Parallel()
	h := seoHead(&PageView{
		Site:       SiteView{Name: "S", LogoURL: "/l.png", SocialLinks: []Link{{URL: "https://t.co/x"}, {URL: "javascript:1"}}},
		BaseURL:    "https://x.io",
		JSONLD:     []json.RawMessage{json.RawMessage(`{"name":"</script><script>alert(1)</script>"}`), json.RawMessage(`{broken`)},
		Breadcrumb: []Crumb{{Name: "Home", URL: "/"}, {Name: "Here"}},
	}, false)
	if len(h.Scripts) != 3 { // backend doc, breadcrumb, graph; invalid doc skipped
		t.Fatalf("scripts=%d", len(h.Scripts))
	}
	for _, s := range h.Scripts {
		body := strings.TrimSuffix(strings.TrimPrefix(string(s), `<script type="application/ld+json">`), "</script>")
		if strings.Contains(body, "<") || strings.Contains(body, "</") {
			t.Errorf("unescaped < in JSON-LD: %s", s)
		}
		var any map[string]any
		if err := json.Unmarshal([]byte(body), &any); err != nil {
			t.Errorf("invalid JSON-LD: %v: %s", err, body)
		}
	}
	all := string(h.Scripts[1]) + string(h.Scripts[2])
	for _, want := range []string{`"BreadcrumbList"`, `"https://x.io/"`, `"Organization"`, `"WebSite"`, `"SearchAction"`, `https://x.io/search?q={search_term_string}`, `https://x.io#organization`, `https://t.co/x`, `https://x.io/l.png`} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %s in %s", want, all)
		}
	}
	for _, bad := range []string{"javascript:"} {
		if strings.Contains(all, bad) {
			t.Errorf("must not contain %s", bad)
		}
	}
}

func TestSEOAICrawlerComment(t *testing.T) {
	t.Parallel()
	h := seoHead(&PageView{AICrawlerComment: "hello --> <script>x</script> --!> end--"}, false)
	got := string(h.Comment)
	inner := strings.TrimSuffix(strings.TrimPrefix(got, "<!-- "), " -->")
	if strings.Contains(inner, "--") {
		t.Fatalf("comment body still has --: %q", got)
	}
	if !strings.HasPrefix(got, "<!-- ") || !strings.HasSuffix(got, " -->") {
		t.Fatalf("shape: %q", got)
	}
	if seoHead(&PageView{}, false).Comment != "" {
		t.Fatal("no comment when empty")
	}
}

func TestBreadcrumbLDID(t *testing.T) {
	t.Parallel()
	v := &PageView{BaseURL: "https://x.io/", Breadcrumb: []Crumb{{Name: "Home", URL: "/"}, {Name: "Here", URL: "/a/b"}}}
	if got := breadcrumbLD(v)["@id"]; got != "https://x.io/a/b#breadcrumb" {
		t.Errorf("@id = %v", got)
	}
	v.Breadcrumb = []Crumb{{Name: "Home", URL: "https://y.io/h"}, {Name: "Here", URL: "https://y.io/p"}}
	if got := breadcrumbLD(v)["@id"]; got != "https://y.io/p#breadcrumb" {
		t.Errorf("absolute @id = %v", got)
	}
	v.Breadcrumb = []Crumb{{Name: "Home", URL: "/"}, {Name: "Here"}}
	if got := breadcrumbLD(v)["@id"]; got != "https://x.io/#breadcrumb" {
		t.Errorf("no-URL @id = %v, want base#breadcrumb (as Next)", got)
	}
}

func TestSEOArchiveCollectionPage(t *testing.T) {
	t.Parallel()
	v := &PageView{
		Kind: KindArchive, BaseURL: "https://x.io", Path: "/blog",
		Page:       PageMeta{Title: "Blog", Description: "All posts"},
		Items:      []RelatedItem{{Title: "a"}},
		Pagination: &Pagination{Total: 7},
	}
	var found string
	for _, s := range seoHead(v, true).Scripts {
		if strings.Contains(string(s), `"CollectionPage"`) {
			found = string(s)
		}
	}
	for _, want := range []string{`"name":"Blog"`, `"url":"https://x.io/blog"`, `"numberOfItems":7`, `"description":"All posts"`} {
		if !strings.Contains(found, want) {
			t.Errorf("missing %s in %q", want, found)
		}
	}
	v.Kind = KindPage
	for _, s := range seoHead(v, true).Scripts {
		if strings.Contains(string(s), "CollectionPage") {
			t.Error("non-archive view must not emit CollectionPage")
		}
	}
}

func TestSEOFeedAlternateLinks(t *testing.T) {
	t.Parallel()
	e := newTestEngine(t, Options{})
	v := minimalView()
	v.BaseURL = "https://x.io"
	v.Site.Feeds = []FeedLink{
		{Title: "Acme", Type: "application/rss+xml", Href: "/feed.xml"},
		{Title: "Acme (Atom)", Type: "application/atom+xml", Href: "/atom.xml"},
	}
	out := renderToString(t, e, v)
	for _, want := range []string{
		`<link rel="alternate" type="application/rss&#43;xml" title="Acme" href="https://x.io/feed.xml">`, // html/template escapes "+"
		`<link rel="alternate" type="application/atom&#43;xml" title="Acme (Atom)" href="https://x.io/atom.xml">`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in %s", want, out)
		}
	}
	v.Site.Feeds = nil
	if strings.Contains(renderToString(t, e, v), "rss&#43;xml") {
		t.Error("no feeds, no alternate links")
	}
}
