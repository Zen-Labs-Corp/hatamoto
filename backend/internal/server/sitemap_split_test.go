package server

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/seo"
)

func splitIndexFiles(plan *sitemapPlan) []string {
	var files []string
	for _, e := range plan.Index().Sitemaps {
		files = append(files, strings.TrimPrefix(e.Loc, "http://example.test"))
	}
	return files
}

func TestSitemapSplit_ChunksAChildPerLimit(t *testing.T) {
	t.Parallel()
	in := smInput(t, smSettings(seo.Sitemap{MaxURLs: 2}),
		entry("post", "a", "2025-01-01", nil), entry("post", "b", "2025-03-01", nil), entry("post", "c", "2025-02-01", nil))
	plan := planSitemap(in)

	if want := []string{"/sitemap-post.xml", "/sitemap-post-2.xml", "/sitemap-misc.xml"}; !reflect.DeepEqual(splitIndexFiles(plan), want) {
		t.Errorf("index = %v, want %v", splitIndexFiles(plan), want)
	}
	first, _ := plan.GroupURLs("post")
	second, ok := plan.GroupURLs("post-2")
	if !ok || !reflect.DeepEqual(urlsOf(first), []string{"/post", "/post/a"}) || !reflect.DeepEqual(urlsOf(second), []string{"/post/b", "/post/c"}) {
		t.Errorf("chunks = %v / %v ok=%v", urlsOf(first), urlsOf(second), ok)
	}
	if _, ok := plan.GroupURLs("post-3"); ok || plan.HasGroup("post-3") {
		t.Error("a third file exists")
	}
	var lastMod string
	for _, e := range plan.Index().Sitemaps {
		if strings.HasSuffix(e.Loc, "/sitemap-post-2.xml") {
			lastMod = e.LastMod
		}
	}
	if lastMod != "2025-03-01" {
		t.Errorf("child lastmod = %q", lastMod)
	}
}

func TestSitemapSplit_DefaultLimitChangesNothing(t *testing.T) {
	t.Parallel()
	plan := planSitemap(smInput(t, nil, entry("post", "a", "2025-01-01", nil), entry("post", "b", "2025-01-01", nil)))
	if want := []string{"/sitemap-post.xml", "/sitemap-misc.xml"}; !reflect.DeepEqual(splitIndexFiles(plan), want) {
		t.Errorf("index = %v, want %v", splitIndexFiles(plan), want)
	}
}

func TestSitemapSplit_TakenNameIsReportedNotSplit(t *testing.T) {
	t.Parallel()
	snap := sitemapSnap(t, sitemapSchema("post", `{"url_pattern":"/post/{slug}","archive_path":"/post"}`), sitemapSchema("note", `{"url_pattern":"/note/{slug}","sitemap_group":"post-2"}`))
	in := basePlanInput(snap, entry("post", "a", "2025-01-01", nil), entry("post", "b", "2025-01-01", nil), entry("note", "n", "2025-01-01", nil))
	in.SEO = smSettings(seo.Sitemap{MaxURLs: 1})
	plan := planSitemap(in)
	if len(plan.collisions) == 0 {
		t.Error("no collision reported")
	}
	if u, _ := plan.GroupURLs("post"); len(u) != 3 {
		t.Errorf("post stays whole: %v", urlsOf(u))
	}
}

func TestSitemapSplit_PerLocaleFilesSplit(t *testing.T) {
	t.Parallel()
	in := basePlanInput(multiSitemapSnap(t, false), localeEntry("a", "fr", nil), localeEntry("b", "fr", nil))
	in.SEO = smSettings(seo.Sitemap{MaxURLs: 1})
	plan := planSitemap(in)
	if !plan.HasGroup("fr-misc") || !plan.HasGroup("fr-misc-2") {
		t.Fatalf("files = %v", splitIndexFiles(plan))
	}
	if u, _ := plan.GroupURLs("fr-misc-2"); !reflect.DeepEqual(urlsOf(u), []string{"/fr/b"}) {
		t.Errorf("fr-misc-2 = %v", urlsOf(u))
	}
	if plan.historicName("fr-misc-2") && !plan.HasGroup("fr-misc-2") {
		t.Error("a split file name is not a historic one")
	}
}

func TestSitemapSplit_HandlerServesChildren(t *testing.T) {
	t.Parallel()
	in := smInput(t, smSettings(seo.Sitemap{MaxURLs: 1}), entry("post", "a", "2025-01-01", nil))
	plan := planSitemap(in)
	s := &Server{}
	for file, want := range map[string]int{"post": 1, "post-2": 1} {
		urls := s.sitemapFileURLs(httptest.NewRequest(http.MethodGet, "/", nil).Context(), plan, file)
		if len(urls) != want {
			t.Errorf("%s has %d URLs, want %d", file, len(urls), want)
		}
	}
	if s.sitemapFileURLs(httptest.NewRequest(http.MethodGet, "/", nil).Context(), plan, "post-3") != nil {
		t.Error("unknown child lists URLs")
	}
}
