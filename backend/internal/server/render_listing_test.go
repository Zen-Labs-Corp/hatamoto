package server

import (
	"context"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/person"
	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/core/internal/siteconfig"
)

func TestBylineView(t *testing.T) {
	t.Parallel()
	schemas := []string{"post", "blog"}
	jane, quiet, anon := uuid.New(), uuid.New(), uuid.New()
	b := &bylines{
		path: func(_ context.Context, slug string) string { return "/author/" + slug },
		credits: map[uuid.UUID]person.Credit{
			jane:  {PersonID: &jane},
			quiet: {PersonID: &quiet},
			anon:  {Org: true},
		},
		people: map[uuid.UUID]*person.Person{
			jane:  {ID: jane, Slug: "jane-guest", Name: "Jane Guest", ShortBio: "Writes.", Photo: "/media/j.png", JobTitle: "Editor", Public: true},
			quiet: {ID: quiet, Slug: "quiet", Name: "Quiet Person", Photo: "/media/q.png", JobTitle: "Spy", ShortBio: "Secret."},
		},
		org: "Acme Org",
	}
	ctx := context.Background()
	if a := b.view(ctx, jane, nil, "post", schemas); a == nil || a.Name != "Jane Guest" || a.URL != "/author/jane-guest" || a.Bio != "Writes." || a.Avatar != "/media/j.png" || a.JobTitle != "Editor" {
		t.Errorf("public person: %+v", a)
	}
	if a := b.view(ctx, jane, nil, "page", schemas); a == nil || a.URL != "" || a.Name != "Jane Guest" {
		t.Errorf("a schema with no author pages must not link: %+v", a)
	}
	if a := b.view(ctx, quiet, nil, "post", schemas); a == nil || !reflect.DeepEqual(*a, render.AuthorView{Name: "Quiet Person"}) {
		t.Errorf("a non-public person is a name only: no link, photo, job title or bio: %+v", a)
	}
	doc := map[string]any{"@type": "BlogPosting"}
	b.applyJSONLD(ctx, quiet, "https://x.test", doc)
	if node, _ := doc["author"].(map[string]any); len(node) != 2 || node["name"] != "Quiet Person" || node["@type"] != "Person" {
		t.Errorf("JSON-LD of a non-public person is the name only: %v", doc["author"])
	}
	if a := b.view(ctx, anon, nil, "post", schemas); a == nil || !a.IsOrg || a.Name != "Acme Org" || a.URL != "" {
		t.Errorf("organisation byline: %+v", a)
	}
	other := uuid.New()
	if a := b.view(ctx, other, map[string]any{"author": " Jane Guest "}, "post", schemas); a == nil || a.Name != "Jane Guest" || a.URL != "" {
		t.Errorf("plain byline without a credit: %+v", a)
	}
	if a := b.view(ctx, other, map[string]any{"author_name": "Bob Writer"}, "blog", schemas); a == nil || a.Name != "Bob Writer" {
		t.Errorf("author_name fallback: %+v", a)
	}
	if b.view(ctx, other, map[string]any{"title": "x"}, "post", schemas) != nil {
		t.Error("no author must give no byline (never the creating user)")
	}
}

func TestCleanSearchQuery(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"  hello   world ": "hello world",
		"a\x00b":           "a b",
		"bad\xffbyte":      "badbyte",
		"tab\there\n":      "tab here",
	} {
		if got := cleanSearchQuery(in); got != want {
			t.Errorf("cleanSearchQuery(%q) = %q, want %q", in, got, want)
		}
	}
	long := cleanSearchQuery(strings.Repeat("é", 500))
	if n := utf8.RuneCountInString(long); n != maxSearchQuery || !utf8.ValidString(long) {
		t.Errorf("long query: %d runes, valid=%v", n, utf8.ValidString(long))
	}
	if hasPositiveTerm("-a -b") || !hasPositiveTerm("-a b") || hasPositiveTerm("") {
		t.Error("hasPositiveTerm")
	}
}

func TestCleanSearchType(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]struct {
		slug string
		ok   bool
	}{
		"":                      {"", true},
		"  ":                    {"", true},
		"doc":                   {"doc", true},
		" Doc ":                 {"doc", true},
		"release-note_2":        {"release-note_2", true},
		"1doc":                  {"", false},
		"a b":                   {"", false},
		"a/../b":                {"", false},
		"doc%00":                {"", false},
		strings.Repeat("a", 65): {"", false},
		strings.Repeat("a", 64): {strings.Repeat("a", 64), true},
	} {
		if slug, ok := cleanSearchType(in); slug != want.slug || ok != want.ok {
			t.Errorf("cleanSearchType(%q) = %q, %v; want %q, %v", in, slug, ok, want.slug, want.ok)
		}
	}
}

func TestArchiveHeadingHelpers(t *testing.T) {
	t.Parallel()
	nav := []render.NavItem{{Label: "Home", Href: "/"}, {Label: "More", Href: "/more", Children: []render.NavItem{{Label: "Blog", Href: "/post/"}}}}
	if got := navLabelFor(nav, "/post"); got != "Blog" {
		t.Errorf("navLabelFor = %q", got)
	}
	for in, want := range map[string]string{"Post": "Posts", "Category": "Categories", "News": "News", "Day": "Days", "": ""} {
		if got := pluralLabel(in); got != want {
			t.Errorf("pluralLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestThemeTokensDropStockDefaults(t *testing.T) {
	t.Parallel()
	if tok := themeTokens(siteconfig.DefaultThemeConfig()); tok != (render.ThemeTokens{}) {
		t.Errorf("stock defaults must not be emitted inline (they would pin light colours in dark mode): %+v", tok)
	}
	th := siteconfig.DefaultThemeConfig()
	th.Colors.Background, th.Colors.Primary = "#101010", "#C23B22"
	tok := themeTokens(th)
	if tok.Background != "#101010" || tok.Primary != "#C23B22" || tok.Text != "" {
		t.Errorf("owner-set values must pass through: %+v", tok)
	}
}

func TestListingPage(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]int{"": 1, "abc": 1, "0": 1, "-3": 1, "2": 2, "99999999": maxListingPage} {
		r := httptest.NewRequest("GET", "/x?page="+in, nil)
		if got := listingPage(r); got != want {
			t.Errorf("page=%q -> %d, want %d", in, got, want)
		}
	}
}

func TestPlainTextAndListItem(t *testing.T) {
	t.Parallel()
	if got := plainText("<p>Hello   <b>big</b>\n world</p><script>x</script>", 11); got != "Hello big w" {
		t.Errorf("plainText = %q", got)
	}
	for in, want := range map[string]string{
		"<h2>Why it matters</h2><p>Lorem ipsum.</p>":          "Why it matters Lorem ipsum.",
		"<p>elit.</p><h3>Heading</h3><p>More</p>":             "elit. Heading More",
		"<p>It&#8217;s&nbsp;a &amp; b</p>":                    "It\u2019s a & b",
		"<style>p{color:red}</style><p>x</p><b>a</b><i>b</i>": "x ab",
		"<ul><li>one</li><li>two</li></ul>":                   "one two",
	} {
		if got := plainText(in, 200); got != want {
			t.Errorf("plainText(%q) = %q, want %q", in, got, want)
		}
	}
	pub := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	e := &entity.Entity{Name: "Name", PublishedAt: &pub, Attributes: []byte(`{"content":"<p>From body</p>","hero_image":"/media/h.jpg"}`)}
	it := listItem(e, "/blog/x")
	if it.Title != "Name" || it.Excerpt != "From body" || it.Image != "/media/h.jpg" || it.URL != "/blog/x" || !it.PublishedAt.Equal(pub) {
		t.Errorf("listItem = %+v", it)
	}
}

func TestImageVariants(t *testing.T) {
	t.Parallel()
	w := 2000
	raw := []byte(`[{"size":"thumbnail","url":"/m/a_thumbnail.jpg","width":150},{"size":"large","url":"/m/a_large.jpg","width":1024},{"size":"medium","url":"/m/a_medium.jpg","width":300}]`)
	got := imageVariants(raw, "/m/a.jpg", &w)
	if len(got) != 3 || got[0].Width != 300 || got[1].Width != 1024 || got[2].URL != "/m/a.jpg" {
		t.Errorf("imageVariants = %+v", got)
	}
	if imageVariants([]byte(`[]`), "/m/a.jpg", &w) != nil || imageVariants([]byte(`nope`), "/m/a.jpg", &w) != nil {
		t.Error("no usable variants must give none")
	}
}

func TestDims(t *testing.T) {
	t.Parallel()
	w, h, zero, neg := 1600, 900, 0, -3
	for _, c := range []struct {
		w, h         *int
		wantW, wantH int
	}{{&w, &h, 1600, 900}, {nil, &h, 0, 0}, {&w, nil, 0, 0}, {&w, &zero, 0, 0}, {&neg, &h, 0, 0}} {
		if gw, gh := dims(c.w, c.h); gw != c.wantW || gh != c.wantH {
			t.Errorf("dims(%v, %v) = %d, %d", c.w, c.h, gw, gh)
		}
	}
}

func TestScheduledItemExposesOnlyTheDateRow(t *testing.T) {
	t.Parallel()
	pub := time.Date(2026, 3, 4, 0, 0, 0, 0, time.UTC)
	e := &entity.Entity{
		Name: "Soon", Status: entity.StatusScheduled, PublishedAt: &pub,
		Schema:     &entity.EntitySchema{Slug: "blog"},
		Attributes: []byte(`{"title":"Soon!","content":"<p>embargoed body</p>","excerpt":"lede","featured_image":"/m/i.jpg","category":"c","series_slug":"s","part":3,"secret":"x"}`),
	}
	got := scheduledItem(e, queryAttrKeys(render.QuerySpec{Attr: "series_slug", Order: "-attr:part"}))
	want := render.RelatedItem{
		Title: "Soon!", Name: "Soon", Schema: "blog", PublishedAt: pub,
		Attrs: map[string]any{"series_slug": "s", "part": float64(3)},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("scheduledItem = %+v, want %+v", got, want)
	}
	if got := scheduledItem(e, []string{"content", "page_sections", "missing"}); len(got.Attrs) != 0 {
		t.Errorf("body or absent keys kept: %v", got.Attrs)
	}
	if k := queryAttrKeys(render.QuerySpec{Order: "name"}); len(k) != 0 {
		t.Errorf("non-attr order keys = %v", k)
	}
}
