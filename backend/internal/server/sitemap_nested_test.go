package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/entity"
)

// nestedDatedSchemas route pages by their parent chain and posts by date.
func nestedDatedSchemas(t *testing.T) *entity.URLSnapshot {
	t.Helper()
	return sitemapSnap(t,
		sitemapSchema("page", `{"url_pattern":"/{path}"}`),
		sitemapSchema("post", `{"url_pattern":"/{year}/{month}/{slug}","archive_path":"/news","sitemap_group":"news"}`),
		sitemapSchema("note", `{"url_pattern":"/notes/{year}/{month}/{day}/{slug}"}`),
	)
}

func nestedDatedEntries() []*entity.SitemapEntry {
	at := func(s string) time.Time {
		tm, err := time.Parse(time.RFC3339, s)
		if err != nil {
			panic(err)
		}
		return tm
	}
	return []*entity.SitemapEntry{
		entry("note", "late-note", "2025-03-05", func(e *entity.SitemapEntry) { e.PublishedAt = at("2025-03-09T23:30:00Z") }),
		entry("post", "hello-world", "2025-03-04", func(e *entity.SitemapEntry) { e.PublishedAt = at("2024-05-17T12:00:00Z") }),
		entry("post", "year-end", "2025-03-03", func(e *entity.SitemapEntry) { e.PublishedAt = at("2024-12-31T23:59:59Z") }),
		entry("page", "local", "2025-03-02", func(e *entity.SitemapEntry) {
			e.ParentSlug, e.AncestorPath = "seo", "services/seo"
		}),
		entry("page", "local", "2025-03-02", func(e *entity.SitemapEntry) {
			e.ParentSlug, e.AncestorPath = "pt", "services/pt"
		}),
		entry("page", "seo", "2025-03-01", func(e *entity.SitemapEntry) { e.ParentSlug, e.AncestorPath = "services", "services" }),
		entry("page", "services", "2025-02-28", nil),
		entry("page", "about", "2025-02-27", nil),
	}
}

func TestSitemapBuilder_NestedAndDatedGolden(t *testing.T) {
	t.Parallel()
	plan := planSitemap(basePlanInput(nestedDatedSchemas(t), nestedDatedEntries()...))

	var b strings.Builder
	b.WriteString(renderXML(t, plan.Index()))
	for _, key := range []string{"news", "notes", "misc"} {
		urls, ok := plan.GroupURLs(key)
		if !ok {
			continue
		}
		b.WriteString("\n<!-- sitemap-" + key + " -->\n")
		b.WriteString(renderXML(t, URLSet{XMLNS: "http://www.sitemaps.org/schemas/sitemap/0.9", URLs: urls}))
	}
	checkGolden(t, filepath.Join("testdata", "sitemap", "nested_dated.golden"), b.String())
}

func TestSitemapBuilder_NestedPathsDoNotCollide(t *testing.T) {
	t.Parallel()
	plan := planSitemap(basePlanInput(nestedDatedSchemas(t), nestedDatedEntries()...))
	misc, _ := plan.GroupURLs("misc")
	seen := map[string]bool{}
	for _, u := range urlsOf(misc) {
		if seen[u] {
			t.Errorf("duplicate sitemap url %s", u)
		}
		seen[u] = true
	}
	for _, want := range []string{"/services/seo/local", "/services/pt/local", "/services/seo", "/services", "/about"} {
		if !seen[want] {
			t.Errorf("%s missing from misc: %v", want, urlsOf(misc))
		}
	}
	news, _ := plan.GroupURLs("news")
	if got := urlsOf(news); len(got) != 3 || got[1] != "/2024/05/hello-world" || got[2] != "/2024/12/year-end" {
		t.Errorf("news = %v", got)
	}
}

// TestCanonicalURLsGolden pins the canonical path of nested and dated
// entities (the URL the page, its JSON-LD and the sitemap agree on).
func TestCanonicalURLsGolden(t *testing.T) {
	t.Parallel()
	snap := nestedDatedSchemas(t)
	at := func(s string) *time.Time {
		tm, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatal(err)
		}
		return &tm
	}
	rows := []struct {
		label     string
		schema    string
		slug      string
		ancestors []string
		published *time.Time
	}{
		{"root page", "page", "about", nil, nil},
		{"child page", "page", "seo", []string{"services"}, nil},
		{"grandchild page", "page", "local", []string{"services", "seo"}, nil},
		{"same slug, other branch", "page", "local", []string{"services", "pt"}, nil},
		{"dated post", "post", "hello-world", nil, at("2024-05-17T12:00:00Z")},
		{"post at year end", "post", "year-end", nil, at("2024-12-31T23:59:59Z")},
		{"post stamped with an offset (read in UTC)", "post", "offset", nil, at("2024-05-17T08:00:00+10:00")},
		{"day-dated note", "note", "late-note", nil, at("2025-03-09T23:30:00Z")},
	}
	var b strings.Builder
	for _, r := range rows {
		ent := &entity.Entity{Slug: r.slug, PublishedAt: r.published, Schema: &entity.EntitySchema{Slug: r.schema}}
		b.WriteString(r.label + " => http://example.test" + snap.EntityPath(ent, r.ancestors) + "\n")
	}
	checkGolden(t, filepath.Join("testdata", "canonical_nested_dated.golden"), b.String())
}

func checkGolden(t *testing.T, path, got string) {
	t.Helper()
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("%s differs:\n--- got\n%s\n--- want\n%s", path, got, want)
	}
}
