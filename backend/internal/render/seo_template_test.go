package render

import (
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
