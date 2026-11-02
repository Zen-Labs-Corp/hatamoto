package server

import (
	"reflect"
	"testing"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/seo"
)

func smSettings(sm seo.Sitemap) *seo.Settings { return &seo.Settings{Sitemap: sm} }

func smInput(t *testing.T, sm *seo.Settings, entries ...*entity.SitemapEntry) sitemapInput {
	t.Helper()
	in := basePlanInput(sitemapSnap(t, sitemapSchema("post", `{"url_pattern":"/post/{slug}","archive_path":"/post"}`)), entries...)
	in.SEO = sm
	return in
}

func smPostURLs(plan *sitemapPlan) []string {
	u, _ := plan.GroupURLs("post")
	return urlsOf(u)
}

func TestSitemapSettings_NilEqualsZero(t *testing.T) {
	t.Parallel()
	entries := []*entity.SitemapEntry{entry("post", "a", "2025-01-01", nil), entry("page", "about", "2025-01-01", nil)}
	a, b := planSitemap(smInput(t, nil, entries...)), planSitemap(smInput(t, smSettings(seo.Sitemap{}), entries...))
	for _, key := range []string{"post", "misc"} {
		ua, _ := a.GroupURLs(key)
		ub, _ := b.GroupURLs(key)
		if !reflect.DeepEqual(ua, ub) {
			t.Errorf("%s differs between nil and zero settings: %v vs %v", key, ua, ub)
		}
	}
}

func TestSitemapSettings_HideAndExclusion(t *testing.T) {
	t.Parallel()
	entries := []*entity.SitemapEntry{
		entry("post", "a", "2025-01-01", nil),
		entry("post", "secret", "2025-01-01", func(e *entity.SitemapEntry) { e.Hidden = true }),
		entry("post", "b", "2025-01-01", nil),
		entry("post", "c", "2025-01-01", nil),
	}
	got := smPostURLs(planSitemap(smInput(t, nil, entries...)))
	if want := []string{"/post", "/post/a", "/post/b", "/post/c"}; !reflect.DeepEqual(got, want) {
		t.Errorf("hidden flag: %v, want %v", got, want)
	}

	cases := []struct {
		name     string
		patterns []string
		want     []string
	}{
		{"exact", []string{"/post/a"}, []string{"/post", "/post/b", "/post/c"}},
		{"exact with slash", []string{"/post/a/"}, []string{"/post", "/post/b", "/post/c"}},
		{"prefix", []string{"/post/b*"}, []string{"/post", "/post/a", "/post/c"}},
		{"section prefix also covers the section itself", []string{"/post/*"}, []string{}},
		{"archive", []string{"/post"}, []string{"/post/a", "/post/b", "/post/c"}},
	}
	for _, tc := range cases {
		got := smPostURLs(planSitemap(smInput(t, smSettings(seo.Sitemap{ExcludePaths: tc.patterns}), entries...)))
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestSitemapSettings_ExclusionComposesWithRedirectAndStatic(t *testing.T) {
	t.Parallel()
	in := smInput(t, smSettings(seo.Sitemap{ExcludePaths: []string{"/post/b", "/quiz"}}),
		entry("post", "a", "2025-01-01", nil), entry("post", "b", "2025-01-01", nil), entry("post", "c", "2025-01-01", nil))
	in.Redirected = func(p string) bool { return p == "/post/a" }
	in.Plugins = sitemapPluginHooks{Static: []SitemapEntry{{Path: "quiz"}, {Path: "tool"}}}
	plan := planSitemap(in)
	if got, want := smPostURLs(plan), []string{"/post", "/post/c"}; !reflect.DeepEqual(got, want) {
		t.Errorf("post = %v, want %v", got, want)
	}
	if misc, _ := plan.GroupURLs("misc"); !reflect.DeepEqual(urlsOf(misc), []string{"/tool"}) {
		t.Errorf("misc = %v", urlsOf(misc))
	}
}

func TestSitemapSettings_AuthorOff(t *testing.T) {
	t.Parallel()
	entries := []*entity.SitemapEntry{entry(entity.PersonSchemaSlug, "ann", "2025-01-01", nil), entry("page", "about", "2025-01-01", nil)}
	snap := sitemapSnap(t, sitemapSchema(entity.PersonSchemaSlug, `{"url_pattern":"/author/{slug}"}`))
	for _, off := range []bool{false, true} {
		in := basePlanInput(snap, entries...)
		in.SEO = smSettings(seo.Sitemap{NoAuthor: off})
		urls, _ := planSitemap(in).GroupURLs("author")
		if listed := len(urls) > 0; listed == off {
			t.Errorf("NoAuthor=%v: author URLs = %v", off, urlsOf(urls))
		}
	}
}

func TestSitemapSettings_DefaultPrecedence(t *testing.T) {
	t.Parallel()
	snap := sitemapSnap(t, sitemapSchema("page", `{"sitemap":{"priority":0.3}}`))
	p := 0.4
	in := basePlanInput(snap, entry("page", "about", "2025-01-01", nil), entry("case", "study", "2025-01-01", nil))
	in.SEO = smSettings(seo.Sitemap{DefaultPriority: &p, DefaultChangeFreq: "daily"})
	misc, _ := planSitemap(in).GroupURLs("misc")
	byLoc := map[string]URL{}
	for _, u := range misc {
		byLoc[u.Loc[len("http://example.test"):]] = u
	}
	if u := byLoc["/about"]; *u.Priority != 0.3 || u.ChangeFreq != "daily" {
		t.Errorf("type override beats default: %v %v", *u.Priority, u.ChangeFreq)
	}
	if u := byLoc["/study"]; *u.Priority != 0.4 || u.ChangeFreq != "daily" {
		t.Errorf("global default beats repository default: %v %v", *u.Priority, u.ChangeFreq)
	}
}

func TestSitemapSettings_NoStats(t *testing.T) {
	t.Parallel()
	in := smInput(t, smSettings(seo.Sitemap{NoStats: true}), entry("post", "a", "2025-01-01", nil))
	in.Plugins = sitemapPluginHooks{Static: []SitemapEntry{{Path: "quiz", Priority: 0.5, ChangeFreq: "monthly"}}}
	plan := planSitemap(in)
	for _, key := range []string{"post", "misc"} {
		urls, _ := plan.GroupURLs(key)
		for _, u := range urls {
			if u.Priority != nil || u.ChangeFreq != "" {
				t.Errorf("%s %s keeps stats: %v %q", key, u.Loc, u.Priority, u.ChangeFreq)
			}
		}
	}
	added := plan.dropStats([]URL{{Loc: "x", Priority: ptr(0.1), ChangeFreq: "daily"}})
	if added[0].Priority != nil || added[0].ChangeFreq != "" {
		t.Errorf("dropStats left %v", added[0])
	}
}

func TestSitemapSettings_Images(t *testing.T) {
	t.Parallel()
	e := entry("post", "a", "2025-01-01", func(e *entity.SitemapEntry) {
		e.Attrs = map[string]string{"og_image": "/media/a.png", "featured_image": "/media/a.png", "photo": "https://cdn.test/p.png"}
	})
	off := planSitemap(smInput(t, nil, e))
	if u, _ := off.GroupURLs("post"); len(u[1].Images) != 0 {
		t.Errorf("images listed while off: %v", u[1].Images)
	}
	on := planSitemap(smInput(t, smSettings(seo.Sitemap{Images: true}), e))
	u, _ := on.GroupURLs("post")
	if want := []ImageRef{{Loc: "http://example.test/media/a.png"}}; !reflect.DeepEqual(u[1].Images, want) {
		t.Errorf("images = %v, want %v (absolute, de-duplicated, no photo for a post)", u[1].Images, want)
	}
	if len(u[0].Images) != 0 {
		t.Errorf("archive row has images: %v", u[0].Images)
	}
	p := entry(entity.PersonSchemaSlug, "ann", "2025-01-01", func(e *entity.SitemapEntry) {
		e.Attrs = map[string]string{"photo": "https://cdn.test/p.png", "og_image": "javascript:x"}
	})
	in := basePlanInput(sitemapSnap(t, sitemapSchema(entity.PersonSchemaSlug, `{"url_pattern":"/author/{slug}"}`)), p)
	in.SEO = smSettings(seo.Sitemap{Images: true})
	au, _ := planSitemap(in).GroupURLs("author")
	if want := []ImageRef{{Loc: "https://cdn.test/p.png"}}; !reflect.DeepEqual(au[len(au)-1].Images, want) {
		t.Errorf("person images = %v, want %v", au[len(au)-1].Images, want)
	}
}

func TestSitemapSettings_Taxonomy(t *testing.T) {
	t.Parallel()
	terms := []sitemapTerm{
		{Kind: "tag", Path: "go", LastMod: "2025-02-01"},
		{Kind: "tag", Path: "quiet", NoIndex: true},
		{Kind: "category", Path: "a/b", LastMod: "2025-03-01"},
		{Kind: "genre", Path: "jazz"},
	}
	build := func(sm *seo.Settings) *sitemapPlan {
		in := smInput(t, sm, entry("post", "a", "2025-01-01", nil))
		in.Terms = terms
		return planSitemap(in)
	}
	if off := build(smSettings(seo.Sitemap{})); off.HasGroup("taxonomy-tag") {
		t.Error("taxonomy file listed while off")
	}
	settings := smSettings(seo.Sitemap{Taxonomy: true})
	settings.Index.NoIndexTaxonomy = map[string]bool{"genre": true}
	plan := build(settings)
	if plan.HasGroup("taxonomy-genre") {
		t.Error("globally noindex taxonomy listed")
	}
	tag, ok := plan.GroupURLs("taxonomy-tag")
	if !ok || !reflect.DeepEqual(urlsOf(tag), []string{"/tag/go"}) || tag[0].LastMod != "2025-02-01" {
		t.Errorf("tag file = %v ok=%v", tag, ok)
	}
	if cat, _ := plan.GroupURLs("taxonomy-category"); !reflect.DeepEqual(urlsOf(cat), []string{"/category/a/b"}) {
		t.Errorf("category file = %v", urlsOf(cat))
	}
	var files []string
	for _, e := range plan.Index().Sitemaps {
		files = append(files, e.Loc)
	}
	for _, want := range []string{"http://example.test/sitemap-taxonomy-tag.xml", "http://example.test/sitemap-taxonomy-category.xml"} {
		found := false
		for _, f := range files {
			found = found || f == want
		}
		if !found {
			t.Errorf("index lacks %s: %v", want, files)
		}
	}
}
