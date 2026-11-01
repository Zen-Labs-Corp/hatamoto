package server

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/entity"
)

// namingSnap is a site with the page and post (group "blog") schemas and the
// locales en (default), fr and nl.
func namingSnap(t *testing.T, prefixAll bool, segs ...string) *entity.URLSnapshot {
	t.Helper()
	enSeg := ""
	if prefixAll {
		enSeg = "en"
	}
	entries := []entity.LocaleEntry{{Tag: "en", Segment: enSeg}, {Tag: "fr", Segment: "fr"}, {Tag: "nl", Segment: "nl"}}
	for i, s := range segs {
		entries[i+1].Segment = s
	}
	set, err := entity.NewLocaleSet("en", entries, prefixAll, "")
	if err != nil {
		t.Fatal(err)
	}
	reg := entity.NewURLRegistry(schemaLister{sitemapSchema("page", ""), sitemapSchema("post", "")}, nil)
	reg.SetLocales(func(context.Context) (*entity.LocaleSet, error) { return set, nil })
	snap, err := reg.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func namingEntry(slug, schema, locale, updated string) *entity.SitemapEntry {
	e := localeEntry(slug, locale, nil)
	e.SchemaSlug, e.UpdatedAt = schema, updated
	return e
}

func namingPlan(t *testing.T, snap *entity.URLSnapshot, naming string, entries ...*entity.SitemapEntry) *sitemapPlan {
	t.Helper()
	return planSitemap(sitemapInput{Snap: snap, BaseURL: "https://x.io", Today: testToday, Naming: naming, Entries: entries,
		Plugins: sitemapPluginHooks{Static: []SitemapEntry{{Path: "quiz", Priority: 0.5, ChangeFreq: "monthly"}}}})
}

func indexNames(plan *sitemapPlan) string {
	var names []string
	for _, e := range plan.Index().Sitemaps {
		names = append(names, strings.TrimPrefix(e.Loc, "https://x.io/sitemap-"))
	}
	return strings.Join(names, ",")
}

func locsOf(urls []URL) string {
	var out []string
	for _, u := range urls {
		out = append(out, strings.TrimPrefix(u.Loc, "https://x.io"))
	}
	return strings.Join(out, " ")
}

func namingEntries() []*entity.SitemapEntry {
	return []*entity.SitemapEntry{
		namingEntry("about", "page", "en", "2029-01-01"),
		namingEntry("a-propos", "page", "fr", "2029-02-01"),
		namingEntry("hello", "post", "en", "2029-03-01"),
		namingEntry("bonjour", "post", "fr", "2029-05-01"),
		namingEntry("hallo", "post", "nl", "2029-04-01"),
		namingEntry("shared-post", "post", entity.LocaleShared, "2029-06-01"),
		namingEntry("shared-page", "page", entity.LocaleShared, "2029-01-01"),
	}
}

func TestPlanSitemapNaming_LegacyPrefixedDefault(t *testing.T) {
	plan := namingPlan(t, namingSnap(t, true), "legacy", namingEntries()...)
	if got, want := indexNames(plan), "en.xml,fr.xml,nl.xml,blog.xml,misc.xml"; got != want {
		t.Fatalf("index = %s, want %s", got, want)
	}
	fr, _ := plan.GroupURLs("fr")
	if got, want := locsOf(fr), "/fr/blog /fr/blog/bonjour /fr/a-propos"; got != want {
		t.Errorf("fr = %q, want %q", got, want)
	}
	blog, _ := plan.GroupURLs("blog")
	if got, want := locsOf(blog), "/blog /blog/shared-post"; got != want {
		t.Errorf("blog = %q, want %q", got, want)
	}
	misc, _ := plan.GroupURLs("misc")
	if got, want := locsOf(misc), "/quiz /shared-page"; got != want {
		t.Errorf("misc = %q, want %q (static routes only here)", got, want)
	}
	for _, key := range []string{"en-blog", "fr-misc", "fr-blog"} {
		if plan.HasGroup(key) {
			t.Errorf("group-mode name %s is routable", key)
		}
	}
	// Index lastmod is the max over the folded rows (en: 2029-03-01).
	lastmod := map[string]string{}
	for _, e := range plan.Index().Sitemaps {
		lastmod[strings.TrimPrefix(e.Loc, "https://x.io/sitemap-")] = e.LastMod
	}
	if lastmod["fr.xml"] != "2029-05-01" || lastmod["en.xml"] != "2029-03-01" || lastmod["blog.xml"] != "2029-06-01" {
		t.Errorf("lastmod = %v", lastmod)
	}
}

func TestPlanSitemapNaming_LegacyUnprefixedDefault(t *testing.T) {
	plan := namingPlan(t, namingSnap(t, false), "legacy", namingEntries()...)
	if got, want := indexNames(plan), "fr.xml,nl.xml,blog.xml,misc.xml"; got != want {
		t.Fatalf("index = %s, want %s", got, want)
	}
	blog, _ := plan.GroupURLs("blog")
	if got, want := locsOf(blog), "/blog /blog/hello /blog/shared-post"; got != want {
		t.Errorf("blog = %q, want %q", got, want)
	}
	misc, _ := plan.GroupURLs("misc")
	if got, want := locsOf(misc), "/quiz /about /shared-page"; got != want {
		t.Errorf("misc = %q, want %q", got, want)
	}
	if !plan.HasGroup("misc") || len(plan.bare) == 0 {
		t.Error("misc or bare keys missing")
	}
}

func TestPlanSitemapNaming_NoDuplicateLocs(t *testing.T) {
	// The archive page and a section page of the same group name one URL.
	snap := namingSnap(t, true)
	section := namingEntry("blog", "page", "fr", "2029-01-01")
	plan := namingPlan(t, snap, "legacy", append(namingEntries(), section)...)
	fr, _ := plan.GroupURLs("fr")
	seen := map[string]bool{}
	for _, u := range fr {
		if seen[u.Loc] {
			t.Fatalf("duplicate loc %s in %s", u.Loc, locsOf(fr))
		}
		seen[u.Loc] = true
	}
}

func TestPlanSitemapNaming_HiddenLocaleSkipped(t *testing.T) {
	set, err := entity.NewLocaleSet("en", []entity.LocaleEntry{{Tag: "en"}, {Tag: "fr", Segment: "fr", Hidden: true}, {Tag: "nl", Segment: "nl"}}, false, "")
	if err != nil {
		t.Fatal(err)
	}
	reg := entity.NewURLRegistry(schemaLister{sitemapSchema("page", ""), sitemapSchema("post", "")}, nil)
	reg.SetLocales(func(context.Context) (*entity.LocaleSet, error) { return set, nil })
	snap, err := reg.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	plan := namingPlan(t, snap, "legacy", namingEntries()...)
	if got, want := indexNames(plan), "nl.xml,blog.xml,misc.xml"; got != want {
		t.Fatalf("index = %s, want %s", got, want)
	}
}

func TestPlanSitemapNaming_SegmentGroupCollision(t *testing.T) {
	// Segment "blog" names the same file as the bare group "blog".
	plan := namingPlan(t, namingSnap(t, true, "blog"), "legacy", namingEntries()...)
	if got, want := indexNames(plan), "en.xml,blog.xml,nl.xml,misc.xml"; got != want {
		t.Fatalf("index = %s, want %s", got, want)
	}
	blog, _ := plan.GroupURLs("blog")
	if got, want := locsOf(blog), "/blog/blog /blog/blog/bonjour /blog/a-propos /blog /blog/shared-post"; got != want {
		t.Errorf("blog = %q, want %q", got, want)
	}
	if len(plan.collisions) != 1 || !strings.Contains(plan.collisions[0], `"blog"`) {
		t.Errorf("collisions = %v", plan.collisions)
	}
}

func TestPlanSitemapNaming_GroupModeCollisionReported(t *testing.T) {
	// Group "a" in locale "b-c" and group "a-b" in locale "c" share "a-b-c".
	set, err := entity.NewLocaleSet("en", []entity.LocaleEntry{{Tag: "en"}, {Tag: "fr", Segment: "a"}, {Tag: "nl", Segment: "a-b"}}, false, "")
	if err != nil {
		t.Fatal(err)
	}
	reg := entity.NewURLRegistry(schemaLister{sitemapSchema("x", `{"url_pattern":"/x/{slug}","sitemap_group":"b-c"}`), sitemapSchema("y", `{"url_pattern":"/y/{slug}","sitemap_group":"c"}`)}, nil)
	reg.SetLocales(func(context.Context) (*entity.LocaleSet, error) { return set, nil })
	snap, err := reg.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	plan := namingPlan(t, snap, "", namingEntry("one", "x", "fr", "2029-01-01"), namingEntry("two", "y", "nl", "2029-01-01"))
	if len(plan.collisions) == 0 {
		t.Fatal("collision not reported")
	}
	urls, _ := plan.GroupURLs("a-b-c")
	if len(urls) != 2 {
		t.Errorf("rows are no longer merged: %s", locsOf(urls))
	}
}

func TestPlanSitemapNaming_GroupModeUnchanged(t *testing.T) {
	a := namingPlan(t, namingSnap(t, true), "", namingEntries()...)
	b := namingPlan(t, namingSnap(t, true), "group", namingEntries()...)
	if indexNames(a) != indexNames(b) || !strings.Contains(indexNames(a), "fr-blog.xml") || len(a.collisions) != 0 {
		t.Fatalf("group mode index = %s / %s", indexNames(a), indexNames(b))
	}
	if parts, ok := b.FileParts("fr-blog"); !ok || len(parts) != 1 || parts[0].key != "fr-blog" {
		t.Errorf("FileParts = %v", parts)
	}
}

func TestPlanSitemapNaming_IndexLegacyGolden(t *testing.T) {
	plan := namingPlan(t, namingSnap(t, false), "legacy", namingEntries()...)
	got := renderXML(t, plan.Index())
	got = strings.ReplaceAll(got, "https://x.io", "http://example.test")
	path := filepath.Join("testdata", "sitemap", "index_multi_legacy.xml")
	if os.Getenv("SITEMAP_GOLDEN_UPDATE") != "" {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("index_multi_legacy differs:\n%s", got)
	}
}

// A locale whose segment is a legacy file name (misc) keeps group naming, so
// the index never lists sitemap-misc.xml twice.
func TestPlanSitemapNaming_LegacyKeepsGroupForFileNameSegment(t *testing.T) {
	plan := namingPlan(t, namingSnap(t, true, "misc"), "legacy", namingEntries()...)
	seen := map[string]int{}
	for _, e := range plan.Index().Sitemaps {
		seen[e.Loc]++
		if seen[e.Loc] > 1 {
			t.Errorf("index lists %s twice", e.Loc)
		}
	}
	if plan.legacy() {
		t.Error("legacy naming applied with a misc locale segment")
	}
}

// The over-limit note names the file, not the count, so it is logged once
// however many URLs the file gains; the log-once set is bounded.
func TestSitemapLogOnce(t *testing.T) {
	note := func(n int) string {
		var entries []*entity.SitemapEntry
		for i := 0; i < n; i++ {
			entries = append(entries, namingEntry(fmt.Sprintf("p%d", i), "page", "fr", "2029-01-01"))
		}
		plan := namingPlan(t, namingSnap(t, true), "legacy", entries...)
		if len(plan.notes) != 1 {
			t.Fatalf("notes = %v", plan.notes)
		}
		return plan.notes[0]
	}
	if a, b := note(maxLegacyFileURLs+1), note(maxLegacyFileURLs+2); a != b {
		t.Errorf("note changes with the count: %q vs %q", a, b)
	}

	var l logOnce
	if !l.first("a") || l.first("a") {
		t.Error("a message is not logged exactly once")
	}
	for i := 0; i < 5*maxSitemapLogged; i++ {
		l.first(fmt.Sprint(i))
	}
	if len(l.seen) > maxSitemapLogged {
		t.Errorf("seen grew to %d", len(l.seen))
	}
}
