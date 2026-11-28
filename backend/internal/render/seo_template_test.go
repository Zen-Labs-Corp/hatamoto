package render

import (
	"reflect"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/seo"
)

func seoView(page PageMeta, set *seo.Settings) *PageView {
	return &PageView{
		Kind: KindSingle, EntityID: "e1", SchemaSlug: "doc",
		Site: SiteView{Name: "Site"}, Page: page, SEO: set,
		BaseURL: "https://x.io", Path: "/p",
	}
}

// With the default settings the head is what it was before templates existed,
// for every title shape the legacy suffix rule handles.
func TestSEODefaultSettingsMatchLegacyHead(t *testing.T) {
	t.Parallel()
	def := seo.Defaults()
	pages := []PageMeta{
		{Title: "Exact", Name: "N"},
		{Name: "N"},
		{Title: "X | Site"},
		{Title: "X - Site"},
		{Title: "X – Site"},
		{Title: "X — Site"},
		{Title: "X · Site"},
		{Title: "X : Site"},
		{Title: "Site"},
		{},
		{Title: "Exact", TitleSet: true, Description: "d", DescriptionSet: true},
	}
	for _, p := range pages {
		legacy := seoHead(seoView(p, nil), false)
		got := seoHead(seoView(p, &def), false)
		if got.Title != legacy.Title || got.Description != legacy.Description {
			t.Errorf("%+v: got %q/%q, want %q/%q", p, got.Title, got.Description, legacy.Title, legacy.Description)
		}
		if !reflect.DeepEqual(got.OG, legacy.OG) || !reflect.DeepEqual(got.Twitter, legacy.Twitter) {
			t.Errorf("%+v: social tags differ: OG %v vs %v, Twitter %v vs %v", p, got.OG, legacy.OG, got.Twitter, legacy.Twitter)
		}
	}
}

func TestSEOTemplateTitleAndDescription(t *testing.T) {
	t.Parallel()
	set := seo.Defaults()
	set.Types["doc"] = seo.TypeTemplates{Title: "%title% %sep% Docs", Description: "Read %title% on %sitename%."}

	h := seoHead(seoView(PageMeta{Name: "Install", Title: "Install", Description: "fallback"}, &set), false)
	if h.Title != "Install | Docs" {
		t.Fatalf("title %q", h.Title)
	}
	if h.Description != "Read Install on Site." {
		t.Fatalf("description %q", h.Description)
	}
	for _, m := range h.OG {
		if m.Key == "og:title" && m.Content != "Install" {
			t.Fatalf("og:title %q must stay bare", m.Content)
		}
	}
}

func TestSEOHandWrittenValuesWin(t *testing.T) {
	t.Parallel()
	set := seo.Defaults()
	set.Types["doc"] = seo.TypeTemplates{Title: "%title% %sep% Docs", Description: "template"}

	h := seoHead(seoView(PageMeta{Name: "N", Title: "Mine", TitleSet: true, Description: "Mine too", DescriptionSet: true}, &set), false)
	if h.Title != "Mine | Site" {
		t.Fatalf("title %q", h.Title)
	}
	if h.Description != "Mine too" {
		t.Fatalf("description %q", h.Description)
	}
}

func TestSEOTemplateKinds(t *testing.T) {
	t.Parallel()
	set := seo.Defaults()
	set.Taxonomy["tag"] = "Tag: %term% %sep% %sitename%"
	set.Archives.Search = "Results for %searchphrase%"
	set.Separator = "-"

	v := &PageView{Kind: KindTaxonomy, Site: SiteView{Name: "Site"}, SEO: &set, Term: &TermView{Kind: "tag", Name: "go"}, Page: PageMeta{Title: "go"}}
	if h := seoHead(v, false); h.Title != "Tag: go - Site" {
		t.Fatalf("taxonomy %q", h.Title)
	}
	v = &PageView{Kind: KindSearch, Site: SiteView{Name: "Site"}, SEO: &set, Query: "q", Page: PageMeta{Title: "Search"}}
	if h := seoHead(v, false); h.Title != "Results for q" {
		t.Fatalf("search %q", h.Title)
	}
}

func TestSEODefaultPathDoesNotAllocate(t *testing.T) {
	set := seo.Defaults()
	v := seoView(PageMeta{Title: "Exact"}, &set)
	h := headView{}
	n := testing.AllocsPerRun(100, func() { applySEOTemplates(&h, v, "Exact") })
	if n != 0 {
		t.Fatalf("allocs = %v", n)
	}
}

func TestSEODescriptionFallsBackToTagline(t *testing.T) {
	t.Parallel()
	const tagline = "Model it once."
	withTagline := func(page PageMeta, set *seo.Settings, tl string) *PageView {
		v := seoView(page, set)
		v.Site.Tagline = tl
		return v
	}
	docTpl := seo.Defaults()
	docTpl.Types["doc"] = seo.TypeTemplates{Description: "Read %title%."}
	notFound := withTagline(PageMeta{Title: "Page not found", NoIndex: true}, nil, tagline)
	notFound.Kind = KindNotFound

	french := withTagline(PageMeta{Name: "N"}, nil, tagline)
	french.Locale, french.Segment = "fr", "fr"

	prefixedDefault := withTagline(PageMeta{Name: "N"}, nil, tagline)
	prefixedDefault.Locale, prefixedDefault.Segment, prefixedDefault.DefaultLocale = "en", "en", true
	prefixedOther := withTagline(PageMeta{Name: "N"}, nil, tagline)
	prefixedOther.Locale, prefixedOther.Segment = "fr", "fr"

	tests := []struct {
		name string
		v    *PageView
		want string
	}{
		{"non-default language", french, ""},
		{"prefix_all default locale", prefixedDefault, tagline},
		{"prefix_all non-default locale", prefixedOther, ""},
		{"tagline", withTagline(PageMeta{Name: "N"}, nil, tagline), tagline},
		{"no tagline", withTagline(PageMeta{Name: "N"}, nil, ""), ""},
		{"own description", withTagline(PageMeta{Name: "N", Description: "Mine", DescriptionSet: true}, nil, tagline), "Mine"},
		{"template description", withTagline(PageMeta{Name: "N"}, &docTpl, tagline), "Read N."},
		{"not found", notFound, tagline},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := seoHead(tc.v, false)
			if h.Description != tc.want {
				t.Fatalf("Description = %q, want %q", h.Description, tc.want)
			}
			out := renderHead(t, h)
			if tc.want == "" {
				if strings.Contains(out, `name="description"`) {
					t.Errorf("empty description still renders a tag:\n%s", out)
				}
				return
			}
			if want := `<meta name="description" content="` + tc.want + `">`; !strings.Contains(out, want) {
				t.Errorf("head lacks %s:\n%s", want, out)
			}
		})
	}

	// The fallback is head-only: the admin "missing description" signal reads
	// the hand-written value.
	v := withTagline(PageMeta{Name: "N"}, nil, tagline)
	seoHead(v, false)
	if got := seoInput(v, "N").HandDescription; got != "" {
		t.Errorf("HandDescription = %q, want empty", got)
	}
	if v.Page.Description != "" {
		t.Errorf("Page.Description = %q, want empty", v.Page.Description)
	}
}
