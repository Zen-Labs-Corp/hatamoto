package render

import (
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

type fakeData struct{}

var fakeItems = []RelatedItem{
	{Title: "First post", URL: "/blog/first", Excerpt: "A short intro.", Image: "/media/a.jpg", Category: "News",
		PublishedAt: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), Author: "Ada Lovelace", AuthorURL: "/author/ada-lovelace"},
	{Title: "Second <post>", URL: "/blog/second", PublishedAt: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)},
}

func (fakeData) Recent(schema string, limit int) []RelatedItem { return fakeItems[:1] }
func (fakeData) Terms(kind string, limit int) []TermView {
	return []TermView{{Kind: kind, Slug: "news", Name: "News", URL: "/category/news", Count: 2}}
}
func (fakeData) Menu(location string) []NavItem {
	return []NavItem{{Label: location, Href: "/" + location}}
}
func (fakeData) ImageVariants(u string) []ImageVariant {
	if u != "/media/a.jpg" {
		return nil
	}
	return []ImageVariant{{URL: "/media/a_medium.jpg", Width: 300}, {URL: "/media/a_large.jpg", Width: 1024}}
}

func (fakeData) Query(q QuerySpec) QueryResult {
	return QueryResult{Items: fakeItems, Total: 2, Page: q.Page, Pages: 1, Limit: q.Limit}
}
func (fakeData) AttrValues(schema, attr string, limit int) []AttrCount {
	return []AttrCount{{Value: "A", Count: 2}}
}

// docData serves a three level docs sidebar (section, Guides/Reference group
// labels without a link, pages) and 120 published doc rows for the card
// descriptions, split over two query pages like a real site; everything else
// is fakeData.
type docData struct {
	fakeData
	// onePage makes the query fit on one page, like a site with few docs.
	onePage bool
}

func (docData) Menu(location string) []NavItem {
	if location != "sidebar" {
		return nil
	}
	return []NavItem{
		{Label: "Getting started", Href: "/docs/getting-started", Children: []NavItem{
			{Label: "Guides", Href: "#", Children: []NavItem{{Label: "Install", Href: "/docs/getting-started/install"}}},
		}},
		{Label: "Themes", Href: "/docs/themes", Children: []NavItem{
			{Label: "Guides", Href: "#", Children: []NavItem{{Label: "Overview", Href: "/docs/themes/overview"}}},
			{Label: "Reference", Href: "#", Children: []NavItem{
				{Label: "Layouts", Href: "/docs/themes/layouts/"},
				{Label: "Template funcs", Href: "/docs/themes/template-funcs"},
			}},
		}},
	}
}

// Query returns 100 rows on page 1 and 20 on page 2. Overview is on page 1
// and Layouts on page 2, so a card page must read both.
// With onePage everything is on page 1 and an out-of-range page returns the
// last page, as renderData.Query does.
func (d docData) Query(q QuerySpec) QueryResult {
	var items []RelatedItem
	if d.onePage {
		items = []RelatedItem{
			{Title: "Overview", URL: "/docs/themes/overview", Excerpt: "How themes work."},
			{Title: "Layouts", URL: "/docs/themes/layouts", Excerpt: "How the directory layout works."},
		}
		return QueryResult{Items: items, Total: 2, Page: 1, Pages: 1, Limit: q.Limit}
	}
	if q.Page == 2 {
		items = append(items, RelatedItem{Title: "Layouts", URL: "/docs/themes/layouts", Excerpt: "How the directory layout works."})
		for i := 1; i < 20; i++ {
			items = append(items, RelatedItem{Title: "Filler", URL: fmt.Sprintf("/docs/filler/b%d", i), Excerpt: "Filler."})
		}
	} else {
		items = append(items, RelatedItem{Title: "Overview", URL: "/docs/themes/overview", Excerpt: "How themes work."})
		for i := 1; i < 100; i++ {
			items = append(items, RelatedItem{Title: "Filler", URL: fmt.Sprintf("/docs/filler/a%d", i), Excerpt: "Filler."})
		}
	}
	return QueryResult{Items: items, Total: 120, Page: q.Page, Pages: 2, Limit: q.Limit}
}

func hierarchyEngine(t *testing.T) *Engine {
	t.Helper()
	return newTestEngine(t, Options{Data: fakeData{}})
}

func hierarchyView() *PageView {
	v := minimalView()
	v.Site.Name = "Field Notes"
	v.Site.Language = "en"
	v.Related = func(int) []RelatedItem { return fakeItems[1:] }
	return v
}

func TestLayoutCandidates(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		kind, schema string
		want         []string
	}{
		{KindSingle, "post", []string{"single-post", "single", "page"}},
		{KindSingle, "doc", []string{"single-doc", "single", "page"}},
		{KindSingle, "../x", []string{"single", "page"}},
		{KindHome, "page", []string{"home", "page"}},
		{KindArchive, "post", []string{"archive-post", "archive", "page"}},
		{KindArchive, "release", []string{"archive-release", "archive", "page"}},
		{KindTaxonomy, "category", []string{"taxonomy-category", "taxonomy", "page"}},
		{KindAuthor, "post", []string{"author", "page"}},
		{KindSearch, "", []string{"search", "page"}},
		{KindPage, "post", []string{"page"}},
	} {
		if got := layoutCandidates(tc.kind, tc.schema); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("layoutCandidates(%q, %q) = %v, want %v", tc.kind, tc.schema, got, tc.want)
		}
	}
}

// A theme that ships only page.html wins over the default's single.html for a
// post; the default fills in everything the theme omits.
func TestHierarchyFallsThroughThemeBeforeDefault(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "layouts/page.html", `{{define "content"}}<p>THEME-PAGE</p>{{end}}`)
	writeFile(t, dir, "layouts/taxonomy-tag.html", `{{define "content"}}<p>THEME-TAG</p>{{end}}`)
	e := newTestEngine(t, Options{Layers: []fs.FS{os.DirFS(dir)}})

	post := hierarchyView()
	post.EntityID, post.SchemaSlug, post.Kind = "e1", "post", KindSingle
	if out := renderToString(t, e, post); !strings.Contains(out, "THEME-PAGE") {
		t.Errorf("single must fall back to the theme's page.html:\n%s", out)
	}

	tag := hierarchyView()
	tag.Kind, tag.Term = KindTaxonomy, &TermView{Kind: "tag", Name: "Go"}
	if out := renderToString(t, e, tag); !strings.Contains(out, "THEME-TAG") {
		t.Errorf("taxonomy-tag must win for a tag:\n%s", out)
	}
	cat := hierarchyView()
	cat.Kind, cat.Term = KindTaxonomy, &TermView{Kind: "category", Name: "News"}
	if out := renderToString(t, e, cat); !strings.Contains(out, "THEME-PAGE") {
		t.Errorf("taxonomy for a category must fall back to the theme's page.html:\n%s", out)
	}

	// search is omitted by the theme, but page.html is: page wins over the default search.
	s := hierarchyView()
	s.Kind = KindSearch
	if out := renderToString(t, e, s); !strings.Contains(out, "THEME-PAGE") {
		t.Errorf("search must fall back to the theme's page.html:\n%s", out)
	}

	// The default alone covers every kind.
	d := hierarchyEngine(t)
	for _, k := range []string{KindSingle, KindHome, KindArchive, KindTaxonomy, KindAuthor, KindSearch, KindPage} {
		v := hierarchyView()
		v.Kind, v.Term, v.Author = k, &TermView{Kind: "tag", Name: "Go"}, &AuthorView{Name: "Ada"}
		renderToString(t, d, v)
	}
}

func TestBodyIsSanitisedAtRenderTime(t *testing.T) {
	t.Parallel()
	e := hierarchyEngine(t)
	v := hierarchyView()
	v.EntityID, v.SchemaSlug, v.Kind = "e1", "post", KindSingle
	v.Entity = &EntityView{Title: "Only content", Schema: "post"}
	v.Attrs = map[string]any{"content": `<p>Hello <b>world</b></p><script>alert(1)</script><img src=x onerror=alert(2)>`}
	out := renderToString(t, e, v)
	if !strings.Contains(out, "<p>Hello <b>world</b></p>") {
		t.Errorf("body missing:\n%s", out)
	}
	for _, bad := range []string{"<script>alert", "onerror"} {
		if strings.Contains(out, bad) {
			t.Errorf("unsanitised %q in output:\n%s", bad, out)
		}
	}
}

func TestPagination(t *testing.T) {
	t.Parallel()
	p := NewPagination("/blog", url.Values{"page": {"9"}, "q": {"a b"}}, 5, 10, 95)
	if p.Page != 5 || p.TotalPages != 10 || p.PrevURL != "/blog?q=a+b&page=4" || p.NextURL != "/blog?q=a+b&page=6" {
		t.Errorf("unexpected pagination: %+v", p)
	}
	if got := p.URL(1); got != "/blog?q=a+b" {
		t.Errorf("page 1 URL = %q", got)
	}
	if got := NewPagination("/blog", nil, 99, 10, 25); got.Page != 3 || got.HasNext {
		t.Errorf("page must clamp to the last page: %+v", got)
	}
	if got := NewPagination("/blog", nil, 1, 10, 0); got.TotalPages != 1 || paginate(got) != nil {
		t.Errorf("empty listing has one page and no links: %+v", got)
	}
	var shape []string
	for _, l := range paginate(p) {
		switch {
		case l.Gap:
			shape = append(shape, "…")
		case l.Current:
			shape = append(shape, "[5]")
		default:
			shape = append(shape, strconv.Itoa(l.N))
		}
	}
	if got, want := strings.Join(shape, " "), "1 … 3 4 [5] 6 7 … 10"; got != want {
		t.Errorf("paginate = %q, want %q", got, want)
	}
}

func TestSrcset(t *testing.T) {
	t.Parallel()
	if got, want := srcset(fakeData{}, "/media/a.jpg"), "/media/a_medium.jpg 300w, /media/a_large.jpg 1024w"; got != want {
		t.Errorf("srcset = %q, want %q", got, want)
	}
	for _, in := range []string{"/media/other.jpg", "javascript:alert(1)", "", "data:image/png;base64,AA=="} {
		if got := srcset(fakeData{}, in); got != "" {
			t.Errorf("srcset(%q) = %q, want empty", in, got)
		}
	}
	if srcset(nil, "/media/a.jpg") != "" {
		t.Error("nil data source gives no srcset")
	}
}

func TestDataFuncsFailOpenWithoutDataSource(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "layouts/page.html", `{{define "content"}}<p>{{len (recent "post" 3)}}{{len (terms "tag" 3)}}{{len (menu "header")}}{{len (related . 3)}}</p>{{end}}`)
	e := newTestEngine(t, Options{Layers: []fs.FS{os.DirFS(dir)}})
	if out := renderToString(t, e, minimalView()); !strings.Contains(out, "<p>0000</p>") {
		t.Errorf("expected empty data:\n%s", out)
	}
}

// Goldens: one per default-theme template, the <main> contents only.
func TestGoldenHierarchyTemplates(t *testing.T) {
	nowFunc = func() time.Time { return time.Date(2031, 6, 1, 0, 0, 0, 0, time.UTC) }
	defer func() { nowFunc = time.Now }()
	e := hierarchyEngine(t)
	pub := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	ada := &AuthorView{Name: "Ada Lovelace", Slug: "ada-lovelace", URL: "/author/ada-lovelace", Avatar: "/media/ada.png",
		JobTitle: "Mathematician", SameAs: []string{"https://example.test/ada", "https://linkedin.example/ada"},
		Links: []AuthorLink{{"Website", "https://example.test/ada"}, {"LinkedIn", "https://linkedin.example/ada"}}}
	listing := func(path string, total int) (*Pagination, []RelatedItem) {
		return NewPagination(path, nil, 2, 2, total), fakeItems
	}

	cases := map[string]func() *PageView{
		"tpl-single": func() *PageView {
			v := hierarchyView()
			v.EntityID, v.SchemaSlug, v.Kind = "e1", "post", KindSingle
			v.Entity = &EntityView{
				ID: "e1", Title: "Hello world", Slug: "hello", Schema: "post", URL: "/blog/hello",
				Excerpt: "A short intro.", Image: "/media/a.jpg", Author: ada, PublishedAt: pub,
				Terms: []TermView{{Kind: "category", Name: "News", URL: "/category/news"}, {Kind: "tag", Name: "Go", URL: "/tag/go"}},
			}
			v.Attrs = map[string]any{"content": `<p>Only <a href="/x">content</a> here.</p><figure class="wp-block-image alignwide"><img src="/media/a.jpg" alt="A"><figcaption>Caption</figcaption></figure>`}
			return v
		},
		"tpl-single-page": func() *PageView {
			v := hierarchyView()
			v.EntityID, v.SchemaSlug, v.Kind = "e2", "page", KindSingle
			v.Entity = &EntityView{ID: "e2", Title: "About", Schema: "page"}
			v.Attrs = map[string]any{"content": "<p>We are small.</p>"}
			return v
		},
		"tpl-single-doc": func() *PageView {
			v := hierarchyView()
			v.EntityID, v.SchemaSlug, v.Kind, v.Path = "e9", "doc", KindSingle, "/docs/themes/layouts"
			v.Entity = &EntityView{ID: "e9", Title: "Layouts", Slug: "layouts", Schema: "doc", URL: "/docs/themes/layouts"}
			v.Attrs = map[string]any{"content": `<p>Intro.</p><h2>Setup</h2><p>Text.</p><h3>Files</h3><h2>Setup</h2><p>Again &amp; more.</p>`}
			return v
		},
		"tpl-home": func() *PageView {
			v := hierarchyView()
			v.EntityID, v.SchemaSlug, v.Kind = "e3", "page", KindHome
			v.Entity = &EntityView{ID: "e3", Title: "Home", Schema: "page"}
			v.Attrs = map[string]any{"content": "<p>Welcome.</p>"}
			return v
		},
		"tpl-archive": func() *PageView {
			v := hierarchyView()
			v.Kind, v.SchemaSlug, v.Page.Name = KindArchive, "post", "Blog"
			v.Page.Description = "Notes from the field."
			v.Pagination, v.Items = listing("/blog", 5)
			return v
		},
		"tpl-taxonomy": func() *PageView {
			v := hierarchyView()
			v.Kind, v.Term, v.Page.Name = KindTaxonomy, &TermView{Kind: "tag", Slug: "go", Name: "Go", URL: "/tag/go"}, "Go"
			v.Pagination, v.Items = listing("/tag/go", 4)
			return v
		},
		"tpl-author": func() *PageView {
			v := hierarchyView()
			v.Kind, v.Author, v.Page.Name = KindAuthor, ada, "Ada Lovelace"
			v.Pagination, v.Items = listing("/author/ada-lovelace", 3)
			return v
		},
		"tpl-search": func() *PageView {
			v := hierarchyView()
			v.Kind, v.Query, v.Page.Name = KindSearch, "field", "Search"
			v.Pagination, v.Items = listing("/search", 3)
			return v
		},
		"tpl-search-empty": func() *PageView {
			v := hierarchyView()
			v.Kind, v.Page.Name = KindSearch, "Search"
			return v
		},
	}
	docEngine := newTestEngine(t, Options{Data: docData{}})
	for name, build := range cases {
		t.Run(name, func(t *testing.T) {
			eng := e
			if name == "tpl-single-doc" {
				eng = docEngine
			}
			compareGolden(t, name, mainInner(t, renderToString(t, eng, build())))
		})
	}
}

// releaseData feeds the changelog layout's query with canned release entries,
// newest first as the real query returns them.
type releaseData struct{ fakeData }

func (releaseData) Query(q QuerySpec) QueryResult {
	if q.Schema != "release" || q.Order != "-attr:version_sort" || q.Limit != MaxQueryLimit {
		return QueryResult{}
	}
	items := []RelatedItem{
		{Name: "v0.27.0", Schema: "release", Attrs: map[string]any{
			"version": "v0.27.0", "version_sort": 27000.0, "released_at": "2026-10-19", "channel": "edge",
			"features": []any{"Builder: reusable sections (#161)", "Default theme is light (#160)"},
			"fixes":    []any{"Date range picker polish (#158)"},
			"docs":     []any{"Vanity module path (#141)"},
			"chores":   []any{"Bump <deps> & friends (#142)"},
		}},
		{Name: "v0.26.99", Schema: "release", Attrs: map[string]any{
			"version": "v0.26.99", "version_sort": 26099.0, "released_at": "2026-10-18", "channel": "stable", "yanked": true,
			"fixes": []any{"Hotfix for scheduler (#150)"},
			"docs":  "<p>Rich <strong>docs</strong> note</p><script>alert(1)</script>",
		}},
		{Name: "v0.25.0", Schema: "release", Attrs: map[string]any{
			"version": "v0.25.0", "version_sort": 25000.0, "released_at": "2026-10-01", "channel": "stable",
			"summary": "First Hatamoto 1.x release.",
		}},
	}
	return QueryResult{Items: items, Total: len(items), Page: 1, Pages: 1, Limit: q.Limit}
}

// The changelog layout reads releases through query, groups docs and chores
// under one heading, omits empty groups and anchors each section by version.
func TestGoldenArchiveRelease(t *testing.T) {
	e := newTestEngine(t, Options{Data: releaseData{}})
	v := hierarchyView()
	v.Kind, v.SchemaSlug, v.Page.Name = KindArchive, "release", "Changelog"
	v.Page.Description = "Every Hatamoto release, newest first."
	out := mainInner(t, renderToString(t, e, v))
	for _, want := range []string{`id="v0.27.0"`, `id="v0.26.99"`, `id="v0.25.0"`, "Docs / Chores", "yanked"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q", want)
		}
	}
	if strings.Contains(out, "<script") {
		t.Error("string group was not sanitised")
	}
	compareGolden(t, "tpl-archive-release", out)
}

// Themes written against the original partial contract (nav takes []NavItem,
// nav-link takes a NavItem) must keep rendering.
func TestLegacyNavPartialCalls(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "partials/header.html", `{{define "header"}}<header>{{template "nav" .Site.Nav}}</header>{{end}}`)
	writeFile(t, dir, "partials/footer.html", `{{define "footer"}}<footer>{{range .Site.FooterNav}}{{template "nav-link" .}}{{end}}<ul>{{template "nav-items" .Site.Nav}}</ul></footer>{{end}}`)
	e := newTestEngine(t, Options{Layers: []fs.FS{os.DirFS(dir)}})
	v := minimalView()
	v.Path = "/b"
	v.Site.Nav = []NavItem{{Label: "A", Href: "/a"}, {Label: "B", Href: "/b", Children: []NavItem{{Label: "C", Href: "/c"}}}}
	v.Site.FooterNav = []NavItem{{Label: "F", Href: "/f", Target: "_blank"}}
	out := renderToString(t, e, v)
	for _, want := range []string{`<a href="/a">A</a>`, `<a href="/b">B</a>`, `<a href="/c">C</a>`, `<a href="/f" target="_blank" rel="noopener noreferrer">F</a>`} {
		if !strings.Contains(out, want) {
			t.Errorf("legacy partial call output missing %s:\n%s", want, out)
		}
	}
	if strings.Contains(out, "THEME-FALLBACK") {
		t.Error("unexpected fallback")
	}
}

// The default header marks the current page in the dict form.
func TestDefaultNavMarksCurrentPage(t *testing.T) {
	t.Parallel()
	v := minimalView()
	v.Path = "/b"
	v.Site.Nav = []NavItem{{Label: "A", Href: "/a"}, {Label: "B", Href: "/b"}}
	out := renderToString(t, newTestEngine(t, Options{}), v)
	if !strings.Contains(out, `<a href="/b" aria-current="page">B</a>`) || strings.Contains(out, `<a href="/a" aria-current`) {
		t.Errorf("aria-current:\n%s", out)
	}
}

// A theme whose templates parse but fail to execute is replaced by the default
// templates instead of failing every page.
func TestBrokenThemeFallsBackToDefault(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// .Site.Nav.Missing: a field that does not exist, only caught at execution.
	writeFile(t, dir, "partials/header.html", `{{define "header"}}<header>{{.Site.Nav.Missing}}</header>{{end}}`)
	e := newTestEngine(t, Options{Layers: []fs.FS{os.DirFS(dir)}})
	v := minimalView()
	v.Site.Nav = []NavItem{{Label: "A", Href: "/a"}}
	out := renderToString(t, e, v)
	if !strings.Contains(out, `class="hm-header"`) || !strings.Contains(out, `>A</a>`) {
		t.Errorf("expected the default header after fallback:\n%s", out)
	}
	if !slices.Contains(BootFaults(), "theme-fallback:custom") {
		t.Errorf("fallback not recorded as a boot fault: %v", BootFaults())
	}
}

// With no owner-chosen tokens no inline <style> is emitted, so the stylesheet's
// dark-mode variant is not overridden.
func TestNoInlineTokenStyleByDefault(t *testing.T) {
	t.Parallel()
	e := newTestEngine(t, Options{})
	out := renderToString(t, e, minimalView())
	if strings.Contains(out, "<style>") {
		t.Errorf("default render must not pin colours inline:\n%s", out)
	}
	v := minimalView()
	v.Site.Tokens = ThemeTokens{Background: "#101010"}
	if out := renderToString(t, e, v); !strings.Contains(out, "--hm-bg:#101010") || strings.Contains(out, "--hm-text:") {
		t.Errorf("only the owner's token is emitted:\n%s", out)
	}
}

// An organisation byline shows the site name without a link or a profile.
func TestOrgBylineHasNoAuthorLink(t *testing.T) {
	e := hierarchyEngine(t)
	v := hierarchyView()
	v.EntityID, v.SchemaSlug, v.Kind = "e1", "post", KindSingle
	v.Entity = &EntityView{ID: "e1", Title: "Hello", Slug: "hello", Schema: "post", URL: "/blog/hello",
		Author: &AuthorView{Name: "Acme Org", IsOrg: true}}
	v.Attrs = map[string]any{"content": "<p>Body</p>"}
	out := mainInner(t, renderToString(t, e, v))
	if !strings.Contains(out, "Written by Acme Org") {
		t.Errorf("org byline missing:\n%s", out)
	}
	if strings.Contains(out, "/author/") || strings.Contains(out, `<a href="">`) {
		t.Errorf("org byline links somewhere:\n%s", out)
	}
}

// A doc page renders without a sidebar menu (no data source, or the page
// missing from the menu): no nav entries, no prev/next, no panic.
func TestSingleDocWithoutMenu(t *testing.T) {
	t.Parallel()
	v := hierarchyView()
	v.EntityID, v.SchemaSlug, v.Kind, v.Path = "e9", "doc", KindSingle, "/docs/x"
	v.Entity = &EntityView{ID: "e9", Title: "X", Schema: "doc"}
	v.Attrs = map[string]any{"content": "<h2>One</h2>"}
	for name, e := range map[string]*Engine{
		"no data":     newTestEngine(t, Options{}),
		"not in menu": newTestEngine(t, Options{Data: docData{}}),
	} {
		out := renderToString(t, e, v)
		if !strings.Contains(out, `class="hm-doc__main"`) || strings.Contains(out, `rel="prev"`) || strings.Contains(out, `aria-current="page"`) {
			t.Errorf("%s: unexpected output:\n%s", name, mainInner(t, out))
		}
	}
}

// The sticky columns and heading anchors must clear the sticky site header
// (4rem tall), as the essay aside in sections/content.css does with 6rem.
func TestDocCSSClearsStickyHeader(t *testing.T) {
	t.Parallel()
	b, err := fs.ReadFile(defaultThemeFS, "themes/default/assets/css/docs.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(b)
	for _, want := range []string{"position: sticky; top: 6rem;", "scroll-margin-top: 6rem;"} {
		if !strings.Contains(css, want) {
			t.Errorf("docs.css missing %q", want)
		}
	}
}

// Every section is a <details>; only the one holding the current page is
// open, and the group labels are plain text, never links to "#".
func TestSingleDocSidebarExpandsCurrentSectionOnly(t *testing.T) {
	t.Parallel()
	out := docPage(t, "/docs/themes/layouts")
	side := out[strings.Index(out, `class="hm-doc__nav"`):]
	if n := strings.Count(side, "<details"); n != 2 {
		t.Errorf("want one <details> per section (2), got %d:\n%s", n, side)
	}
	if n := strings.Count(side, "<details open>"); n != 1 || !strings.Contains(side, "<details open><summary>Themes</summary>") {
		t.Errorf("only the Themes section must be open, got %d open:\n%s", n, side)
	}
	if !strings.Contains(side, `<a href="/docs/themes/layouts/" aria-current="page">Layouts</a>`) {
		t.Errorf("current page not marked:\n%s", side)
	}
	if !strings.Contains(side, `<a href="/docs/getting-started">About Getting started</a>`) {
		t.Error("collapsed section lost its own link")
	}
	if strings.Contains(out, `href="#"`) || !strings.Contains(side, `<span class="hm-doc-nav__group">Reference</span>`) {
		t.Errorf("group labels must be spans, not links:\n%s", side)
	}
}

// docPage renders the docs layout for path with docData and returns the HTML.
func docPage(t *testing.T, path string) string {
	t.Helper()
	v := hierarchyView()
	v.EntityID, v.SchemaSlug, v.Kind, v.Path = "e9", "doc", KindSingle, path
	v.Entity = &EntityView{ID: "e9", Title: "Layouts", Schema: "doc"}
	return renderToString(t, newTestEngine(t, Options{Data: docData{}}), v)
}

// A section index shows a Guides grid before a Reference grid, each card with
// the description of its page. Overview is described on query page 1 and
// Layouts on page 2.
func TestSingleDocCardsOnSectionIndex(t *testing.T) {
	t.Parallel()
	out := docPage(t, "/docs/themes")
	art := out[strings.Index(out, `class="hm-doc__main"`):strings.Index(out, `class="hm-doc__nav"`)]
	g, r := strings.Index(art, `hm-doc-cards__label">Guides<`), strings.Index(art, `hm-doc-cards__label">Reference<`)
	if g < 0 || r < 0 || g > r {
		t.Fatalf("want Guides then Reference grids:\n%s", art)
	}
	for _, want := range []string{
		`<a class="hm-doc-card__link" href="/docs/themes/overview">Overview</a><p>How themes work.</p>`,
		`<a class="hm-doc-card__link" href="/docs/themes/layouts/">Layouts</a><p>How the directory layout works.</p>`,
		`<a class="hm-doc-card__link" href="/docs/themes/template-funcs">Template funcs</a></li>`,
	} {
		if !strings.Contains(art, want) {
			t.Errorf("missing %q in:\n%s", want, art)
		}
	}
}

// Each card is exactly one well-formed <li>: the title link and the optional
// description are inside it, whichever query page the description came from.
func TestSingleDocCardsAreWellFormed(t *testing.T) {
	t.Parallel()
	out := docPage(t, "/docs/themes")
	art := out[strings.Index(out, `class="hm-doc__main"`):strings.Index(out, `class="hm-doc__nav"`)]
	grids := regexp.MustCompile(`(?s)<ul class="hm-doc-cards">.*?</ul>`).FindAllString(art, -1)
	if len(grids) != 2 {
		t.Fatalf("want 2 grids, got %d:\n%s", len(grids), art)
	}
	all := strings.Join(grids, "\n")
	if o, c := strings.Count(all, "<li"), strings.Count(all, "</li>"); o != 3 || c != 3 {
		t.Errorf("want 3 <li> opened and closed, got %d and %d:\n%s", o, c, all)
	}
	card := regexp.MustCompile(`(?s)<li class="hm-doc-card">(.*?)</li>`)
	for _, want := range map[string]string{"Overview": "How themes work.", "Layouts": "How the directory layout works."} {
		found := false
		for _, m := range card.FindAllStringSubmatch(all, -1) {
			found = found || strings.Contains(m[1], "<p>"+want+"</p>")
		}
		if !found {
			t.Errorf("description %q not inside a card:\n%s", want, all)
		}
	}
}

// With a single query page the out-of-range page 2 repeats page 1, so a card
// must still show its description once.
func TestSingleDocCardsOneQueryPage(t *testing.T) {
	t.Parallel()
	v := hierarchyView()
	v.EntityID, v.SchemaSlug, v.Kind, v.Path = "e9", "doc", KindSingle, "/docs/themes"
	v.Entity = &EntityView{ID: "e9", Title: "Themes", Schema: "doc"}
	out := renderToString(t, newTestEngine(t, Options{Data: docData{onePage: true}}), v)
	for _, d := range []string{"How themes work.", "How the directory layout works."} {
		if n := strings.Count(out, "<p>"+d+"</p>"); n != 1 {
			t.Errorf("description %q appears %d times, want 1", d, n)
		}
	}
}

// The landing page (not in the menu, parent of the top-level items) shows one
// card per section.
func TestSingleDocCardsOnLanding(t *testing.T) {
	t.Parallel()
	out := docPage(t, "/docs")
	if n := strings.Count(out, `class="hm-doc-card"`); n != 2 {
		t.Errorf("want 2 section cards, got %d", n)
	}
	if !strings.Contains(out, `href="/docs/getting-started">Getting started</a></li>`) {
		t.Error("section card missing")
	}
	if !strings.Contains(out, `<h2 class="hm-doc-cards__label">Sections</h2>`) {
		t.Error("landing grid has no heading")
	}
	if strings.Contains(out, `aria-label="Breadcrumb"><ol>`+"\n"+`</ol>`) {
		t.Error("empty breadcrumb landmark")
	}
}

func TestSingleDocNoCardsOnArticle(t *testing.T) {
	t.Parallel()
	for _, p := range []string{"/docs/themes/layouts", "/docs/themes/overview", "/docs/nowhere"} {
		if out := docPage(t, p); strings.Contains(out, "hm-doc-card") {
			t.Errorf("%s: cards on an article page", p)
		}
	}
}

// The search box is in both copies of the sidebar, limited to the page's
// schema, and neither copy carries an id.
func TestSingleDocSearchForm(t *testing.T) {
	t.Parallel()
	out := docPage(t, "/docs/themes/layouts")
	form := `<form class="hm-doc__search" role="search" action="/search" method="get"><input type="search" name="q" aria-label="Search docs" placeholder="Search docs..."><input type="hidden" name="type" value="doc"></form>`
	if n := strings.Count(out, form); n != 2 {
		t.Errorf("want the search form in the mobile and aside copies, got %d", n)
	}
	body := mainInner(t, out)
	ids := regexp.MustCompile(`\bid="([^"]+)"`).FindAllStringSubmatch(body, -1)
	seen := map[string]bool{}
	for _, m := range ids {
		if seen[m[1]] {
			t.Errorf("duplicate id %q", m[1])
		}
		seen[m[1]] = true
	}
}

// The article precedes the sidebar columns in the source, so the skip link
// (which targets main) does not land before the whole menu; the pager landmark
// is only emitted when there is a neighbour.
func TestSingleDocSourceOrderAndPager(t *testing.T) {
	t.Parallel()
	v := hierarchyView()
	v.EntityID, v.SchemaSlug, v.Kind, v.Path = "e9", "doc", KindSingle, "/docs/themes/layouts"
	v.Entity = &EntityView{ID: "e9", Title: "Layouts", Schema: "doc"}
	out := renderToString(t, newTestEngine(t, Options{Data: docData{}}), v)
	art, side := strings.Index(out, `class="hm-doc__main"`), strings.Index(out, `class="hm-doc__nav"`)
	if art < 0 || side < 0 || art > side {
		t.Errorf("article must come before the sidebar aside (article %d, aside %d)", art, side)
	}
	if !strings.Contains(out, `class="hm-doc__pager"`) {
		t.Error("pager missing although prev/next exist")
	}
	v.Path = "/docs/nowhere"
	if out := renderToString(t, newTestEngine(t, Options{Data: docData{}}), v); strings.Contains(out, `hm-doc__pager`) {
		t.Errorf("empty pager emitted:\n%s", mainInner(t, out))
	}
}

// The root crumb is the schema's archive crumb from .Breadcrumb (between Home
// and the entity); a layout must not assume where the docs live.
func TestSingleDocRootCrumbFromBreadcrumb(t *testing.T) {
	t.Parallel()
	e := newTestEngine(t, Options{Data: docData{}})
	v := hierarchyView()
	v.EntityID, v.SchemaSlug, v.Kind, v.Path = "e9", "doc", KindSingle, "/handbook/themes/layouts"
	v.Entity = &EntityView{ID: "e9", Title: "Layouts", Schema: "doc"}
	v.Breadcrumb = []Crumb{{Name: "Home", URL: "/"}, {Name: "Handbook", URL: "/handbook"}, {Name: "Layouts", URL: "/handbook/themes/layouts"}}
	out := renderToString(t, e, v)
	if !strings.Contains(out, `<li><a href="/handbook">Handbook</a></li>`) || strings.Contains(out, `href="/docs"`) {
		t.Errorf("root crumb not taken from .Breadcrumb:\n%s", mainInner(t, out))
	}
	v.Breadcrumb = []Crumb{{Name: "Home", URL: "/"}, {Name: "Layouts", URL: "/x"}}
	if out := renderToString(t, e, v); strings.Contains(out, `>Handbook<`) || strings.Contains(out, `<li><a href="/x">`) {
		t.Errorf("a schema without an archive must not get a root crumb:\n%s", mainInner(t, out))
	}
}
