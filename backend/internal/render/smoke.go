package render

import (
	"fmt"
	"io"
	"net/url"
	"time"
)

// smokeData is the DataSource of the smoke render: fixed data, no I/O.
type smokeData struct{}

func (smokeData) Recent(string, int) []RelatedItem { return smokeItems }
func (smokeData) Terms(kind string, _ int) []TermView {
	return []TermView{{Kind: kind, Slug: "news", Name: "News", URL: "/category/news", Count: 2}}
}
func (smokeData) Menu(string) []NavItem               { return smokeNav }
func (smokeData) ImageVariants(string) []ImageVariant { return nil }
func (smokeData) Query(q QuerySpec) QueryResult {
	return QueryResult{Items: smokeItems, Total: len(smokeItems), Page: 1, Pages: 1, Limit: q.Limit}
}
func (smokeData) AttrValues(string, string, int) []AttrCount {
	return []AttrCount{{Value: "News", Count: 1}}
}

var (
	smokeNav = []NavItem{
		{Label: "Home", Href: "/"},
		{Label: "Blog", Href: "/blog", Children: []NavItem{{Label: "News", Href: "/blog/news", Target: "_blank"}}},
	}
	smokeSeries = &SeriesView{Slug: "s", Name: "Series", Total: 1, IndexURL: "/blog/s", Parts: []SeriesPartView{
		{Title: "Index", URL: "/blog/s", Index: true, Published: true},
		{Title: "One", URL: "/blog/one", Part: 1, Published: true},
	}}
	smokeItems = []RelatedItem{{
		Title: "Hello", URL: "/blog/hello", Excerpt: "An excerpt.", Image: "/media/a.jpg", Category: "News",
		PublishedAt: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), Author: "Ada", AuthorURL: "/author/ada",
	}}
)

// smokeRender trial-renders every layout kind of the engine's chain against a
// fixture view. It catches what parsing cannot: a partial called with the
// wrong dot, a missing field, a bad pipeline. The embedded default theme is
// covered by the tests, so only chains with a theme layer are checked.
func (e *Engine) smokeRender(chain Chain) error {
	c, err := e.compileChain(chain, smokeData{})
	if err != nil {
		return err
	}
	for _, kind := range []string{KindPage, KindSingle, KindHome, KindArchive, KindTaxonomy, KindAuthor, KindSearch, KindRegistry, "404", "error"} {
		v := smokeView(kind)
		cands := layoutCandidates(kind, "post")
		status := 200
		switch kind {
		case "404":
			cands, status = []string{"404"}, 404
		case "error":
			cands, status = []string{"error"}, 500
		case KindTaxonomy:
			cands = layoutCandidates(kind, "category")
		}
		if err := e.executeWith(c, io.Discard, cands, v, status); err != nil {
			return fmt.Errorf("%s view: %w", kind, err)
		}
	}
	return nil
}

func smokeView(kind string) *PageView {
	v := &PageView{
		Kind: kind, BaseURL: "https://example.test", Path: "/blog",
		Site: SiteView{
			Name: "Smoke", Language: "en", Nav: smokeNav, FooterNav: smokeNav,
			SocialLinks: []Link{{Label: "Mastodon", URL: "https://example.test/@a"}},
			HeaderCTA:   &Link{Label: "Contact", URL: "/contact"},
		},
		Page:       PageMeta{Name: "Smoke", Title: "Smoke", Description: "d", CanonicalURL: "https://example.test/blog"},
		Items:      smokeItems,
		Pagination: NewPagination("/blog", url.Values{}, 1, 12, 30),
		Term:       &TermView{Kind: "category", Slug: "news", Name: "News", URL: "/category/news"},
		Author:     &AuthorView{Name: "Ada", Slug: "ada", URL: "/author/ada", JobTitle: "Engineer", SameAs: []string{"https://example.test/ada"}, Links: []AuthorLink{{"Website", "https://example.test/ada"}}},
		Query:      "q",
		Related:    func(int) []RelatedItem { return smokeItems },
		Series:     func() *SeriesView { return smokeSeries },
		PageNum:    1,
		PostsByAuthor: func(_, page int) PostsPage {
			return PostsPage{Items: smokeItems, Total: 1, Page: page, Pages: 1}
		},
	}
	if kind == KindRegistry {
		v.Registry = &RegistryPageView{Type: "plugin", ListName: "Plugins", ListURL: "/plugins", Plugin: &PluginCard{
			Name: "example", Title: "Example", Version: "v1.0.0", Tier: "community", TierLabel: "Community",
			Author: "ada", Description: "d", Source: "https://example.test/src", PermCount: 1,
			PermLines: []PermLine{{Name: "site.scripts", Text: "Add scripts", Warn: true}},
			About:     []string{"About."}, Features: []string{"A feature"},
		}}
	}
	if kind == KindSingle || kind == KindHome {
		v.EntityID, v.SchemaSlug = "e1", "post"
		v.Attrs = map[string]any{"content": "<p>Body</p>"}
		v.Entity = &EntityView{
			ID: "e1", Title: "Hello", Slug: "hello", URL: "/blog/hello", Schema: "post", Image: "/media/a.jpg",
			Attributes: v.Attrs, Terms: []TermView{*v.Term, {Kind: "tag", Slug: "t", Name: "t", URL: "/tag/t"}},
			Author: v.Author, PublishedAt: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
		}
	}
	return v
}
