package server

import (
	"encoding/xml"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/entity"
)

const testToday = "2030-01-01"

func sitemapSchema(slug, settings string) *entity.EntitySchema {
	s := &entity.EntitySchema{Slug: slug, Name: strings.ToUpper(slug[:1]) + slug[1:]}
	if settings != "" {
		s.Settings = []byte(settings)
	}
	return s
}

func sitemapSnap(t *testing.T, schemas ...*entity.EntitySchema) *entity.URLSnapshot {
	t.Helper()
	snap, err := entity.BuildURLSnapshot(schemas, nil)
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

// repoDefaults mirrors the priority/changefreq ListAllPublished assigns.
func repoDefaults(e *entity.SitemapEntry) *entity.SitemapEntry {
	switch e.SchemaSlug {
	case "page":
		e.Priority, e.ChangeFreq = 0.8, "monthly"
	case "post", "blog", "article":
		e.Priority, e.ChangeFreq = 0.6, "weekly"
	default:
		e.Priority, e.ChangeFreq = 0.5, "monthly"
	}
	return e
}

func entry(schema, slug, date string, mod func(*entity.SitemapEntry)) *entity.SitemapEntry {
	e := repoDefaults(&entity.SitemapEntry{Slug: slug, SchemaSlug: schema, UpdatedAt: date})
	if mod != nil {
		mod(e)
	}
	return e
}

func urlsOf(urls []URL) []string {
	out := make([]string, len(urls))
	for i, u := range urls {
		out[i] = strings.TrimPrefix(u.Loc, "http://example.test")
	}
	return out
}

func basePlanInput(snap *entity.URLSnapshot, entries ...*entity.SitemapEntry) sitemapInput {
	return sitemapInput{Snap: snap, Entries: entries, BaseURL: "http://example.test", Today: testToday}
}

func renderXML(t *testing.T, v any) string {
	t.Helper()
	rec := httptest.NewRecorder()
	if _, err := writeXMLWithXSL(rec, v); err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(rec.Body.String(), testToday, "{{today}}")
}

// goldenEntries is the fixture of testdata/sitemap: a database with empty
// schema settings, newest first like ListAllPublished.
var goldenHomepageID = uuid.MustParse("00000000-0000-0000-0000-0000000000a1")

func goldenEntries() []*entity.SitemapEntry {
	return []*entity.SitemapEntry{
		entry("note", "n2", "2025-03-13", nil),
		entry("note", "n1", "2025-03-12", nil),
		entry("blog", "legacy-post", "2025-03-11", nil),
		entry("post", "second", "2025-03-10", nil),
		entry("post", "hello", "2025-03-09", nil),
		entry("page", "landing", "2025-03-08", func(e *entity.SitemapEntry) { e.ID = goldenHomepageID }), // the configured homepage
		entry("page", "post", "2025-03-07", nil),
		entry("page", "blog", "2025-03-06", nil),
		entry("page", "home", "2025-03-05", nil),
		entry("page", "hidden", "2025-03-04", func(e *entity.SitemapEntry) { e.NoIndex = true }),
		entry("page", "shared", "2025-03-03", nil),
		entry("page", "contact", "2025-03-02", nil),
		entry("page", "about", "2025-03-01", nil),
	}
}

// TestSitemapBuilder_EmptySettingsMatchGolden proves that with empty schema
// settings the builder reproduces the golden sitemap byte for byte.
func TestSitemapBuilder_EmptySettingsMatchGolden(t *testing.T) {
	t.Parallel()
	snap := sitemapSnap(t, sitemapSchema("page", ""), sitemapSchema("post", ""), sitemapSchema("blog", ""), sitemapSchema("note", ""))
	in := basePlanInput(snap, goldenEntries()...)
	in.HomepageID = goldenHomepageID
	plan := planSitemap(in)

	golden := func(name string) string {
		b, err := os.ReadFile(filepath.Join("testdata", "sitemap", name+".xml"))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	check := func(name, got string) {
		t.Helper()
		if got != golden(name) {
			t.Errorf("%s differs from golden:\n--- got\n%s\n--- want\n%s", name, got, golden(name))
		}
	}

	check("index", renderXML(t, plan.Index()))
	for _, key := range []string{"blog", "misc"} {
		urls, ok := plan.GroupURLs(key)
		if !ok {
			t.Fatalf("group %s missing", key)
		}
		check(key, renderXML(t, URLSet{XMLNS: "http://www.sitemaps.org/schemas/sitemap/0.9", URLs: urls}))
	}
	if plan.HasGroup("note") || plan.HasGroup("post") {
		t.Error("only blog and misc exist with empty settings")
	}
	// Sanity: the golden files are well-formed XML.
	var v any
	if err := xml.Unmarshal([]byte(strings.Replace(golden("blog"), `<?xml-stylesheet type="text/xsl" href="/sitemap.xsl"?>`, "", 1)), &v); err != nil {
		t.Errorf("golden blog is not XML: %v", err)
	}
}

func TestSitemapBuilder_NeutralGroupsAndOrder(t *testing.T) {
	t.Parallel()
	snap := sitemapSnap(t,
		sitemapSchema("page", ""),
		sitemapSchema("post", ""),
		sitemapSchema("doc", `{"url_pattern":"/docs/{parent}/{slug}","archive_path":"/docs"}`),
		sitemapSchema("person", `{"url_pattern":"/people/{slug}","sitemap_group":"team"}`),
		sitemapSchema("empty", `{"url_pattern":"/nothing/{slug}"}`),
	)
	plan := planSitemap(basePlanInput(snap,
		entry("doc", "intro", "2025-02-02", func(e *entity.SitemapEntry) { e.ParentSlug = "guide" }),
		entry("doc", "root", "2025-02-01", nil),
		entry("person", "ada", "2025-03-01", nil),
		entry("post", "hello", "2025-01-01", nil),
	))

	var keys []string
	for _, s := range plan.Index().Sitemaps {
		keys = append(keys, strings.TrimPrefix(strings.TrimSuffix(s.Loc, ".xml"), "http://example.test/sitemap-"))
	}
	// groups in schema order, misc last; the empty group is not listed.
	if want := []string{"docs", "team", "blog", "misc"}; !reflect.DeepEqual(keys, want) {
		t.Fatalf("index children = %v, want %v", keys, want)
	}
	docs, _ := plan.GroupURLs("docs")
	if want := []string{"/docs", "/docs/guide/intro", "/docs/root"}; !reflect.DeepEqual(urlsOf(docs), want) {
		t.Errorf("docs = %v, want %v", urlsOf(docs), want)
	}
	if got := len(docs[0].Links); got != 0 {
		t.Errorf("core emits no alternate links, got %d", got)
	}
	team, _ := plan.GroupURLs("team")
	if want := []string{"/people/ada"}; !reflect.DeepEqual(urlsOf(team), want) {
		t.Errorf("team = %v (no archive_path => no index page)", urlsOf(team))
	}
	if plan.HasGroup("nothing") || plan.HasGroup("empty") {
		t.Error("empty group must not exist")
	}
	// lastmod of the index follows the newest entity in the group.
	for _, s := range plan.Index().Sitemaps {
		if strings.HasSuffix(s.Loc, "sitemap-team.xml") && s.LastMod != "2025-03-01" {
			t.Errorf("team lastmod = %s", s.LastMod)
		}
	}
}

func TestSitemapBuilder_SectionPage(t *testing.T) {
	t.Parallel()
	snap := sitemapSnap(t,
		sitemapSchema("page", `{"section_pages":true}`),
		sitemapSchema("doc", `{"url_pattern":"/docs/{slug}","archive_path":"/docs"}`),
		sitemapSchema("note", `{"url_pattern":"/notes/{slug}"}`),
	)
	plan := planSitemap(basePlanInput(snap,
		entry("page", "notes", "2025-04-01", nil), // landing page of /notes/*
		entry("page", "docs", "2025-04-02", nil),  // landing page of /docs/* (archive_path too)
		entry("page", "about", "2025-04-03", nil),
		entry("doc", "intro", "2025-01-01", nil),
		entry("note", "n1", "2025-01-02", nil),
	))

	notes, _ := plan.GroupURLs("notes")
	if want := []string{"/notes", "/notes/n1"}; !reflect.DeepEqual(urlsOf(notes), want) {
		t.Errorf("notes = %v, want %v (section page is the index)", urlsOf(notes), want)
	}
	if notes[0].LastMod != "2025-04-01" {
		t.Errorf("section page lastmod = %s", notes[0].LastMod)
	}
	docs, _ := plan.GroupURLs("docs")
	if want := []string{"/docs", "/docs/intro"}; !reflect.DeepEqual(urlsOf(docs), want) {
		t.Errorf("docs = %v, want one index (archive and section page coincide)", urlsOf(docs))
	}
	// Ordinary pages go to misc; the section pages do not appear there.
	if misc, _ := plan.GroupURLs("misc"); !reflect.DeepEqual(urlsOf(misc), []string{"/about"}) {
		t.Errorf("misc = %v", urlsOf(misc))
	}
	// ... and the group's lastmod in the index includes the section page.
	for _, s := range plan.Index().Sitemaps {
		if strings.HasSuffix(s.Loc, "sitemap-docs.xml") && s.LastMod != "2025-04-02" {
			t.Errorf("docs lastmod = %s", s.LastMod)
		}
	}
}

func TestSitemapBuilder_OverrideIntoMisc(t *testing.T) {
	t.Parallel()
	snap := sitemapSnap(t,
		sitemapSchema("page", ""),
		sitemapSchema("doc", `{"url_pattern":"/docs/{slug}","archive_path":"/docs","overrides":[{"attribute":"format","equals":"report","url_pattern":"/reports/{slug}","sitemap_group":"misc"}]}`),
	)
	if got := snap.OverrideAttributes(); !reflect.DeepEqual(got, []string{"format"}) {
		t.Fatalf("override attributes = %v", got)
	}
	in := basePlanInput(snap,
		entry("doc", "annual", "2025-05-01", func(e *entity.SitemapEntry) { e.Attrs = map[string]string{"format": "report"} }),
		entry("doc", "manual", "2025-05-02", func(e *entity.SitemapEntry) { e.Attrs = map[string]string{"format": "guide"} }),
	)
	in.Plugins = sitemapPluginHooks{Static: []SitemapEntry{{Path: "quiz/start", Priority: 0.7, ChangeFreq: "monthly"}}}
	plan := planSitemap(in)

	misc, _ := plan.GroupURLs("misc")
	if want := []string{"/quiz/start", "/reports/annual"}; !reflect.DeepEqual(urlsOf(misc), want) {
		t.Errorf("misc = %v, want %v", urlsOf(misc), want)
	}
	docs, _ := plan.GroupURLs("docs")
	if want := []string{"/docs", "/docs/manual"}; !reflect.DeepEqual(urlsOf(docs), want) {
		t.Errorf("docs = %v, want %v (override moved out)", urlsOf(docs), want)
	}
}

func TestSitemapBuilder_AliasesMergeIntoOneGroup(t *testing.T) {
	t.Parallel()
	// A legacy "blog" row-set and the "post" schema share the post family's
	// pattern, group and archive; "article" rides along as an alias.
	snap := sitemapSnap(t, sitemapSchema("blog", ""), sitemapSchema("article", ""))
	plan := planSitemap(basePlanInput(snap,
		entry("blog", "a", "2025-01-03", nil),
		entry("article", "b", "2025-01-02", nil),
	))
	post, ok := plan.GroupURLs("blog")
	if !ok {
		t.Fatal("blog group missing")
	}
	if want := []string{"/blog", "/blog/a", "/blog/b"}; !reflect.DeepEqual(urlsOf(post), want) {
		t.Errorf("blog = %v, want %v", urlsOf(post), want)
	}
	if plan.HasGroup("article") {
		t.Error("aliases must not get their own group")
	}

	// An explicit schema that aliases post claims it: the default post owner
	// yields, so /blog and its sitemap group disappear.
	snap = sitemapSnap(t, sitemapSchema("post", ""), sitemapSchema("blog", `{"url_pattern":"/journal/{slug}","archive_path":"/journal","aliases":["post"]}`))
	plan = planSitemap(basePlanInput(snap, entry("blog", "a", "2025-01-03", nil), entry("post", "b", "2025-01-02", nil)))
	if j, _ := plan.GroupURLs("journal"); !reflect.DeepEqual(urlsOf(j), []string{"/journal", "/journal/a", "/journal/b"}) {
		t.Errorf("journal = %v", urlsOf(j))
	}
	if plan.HasGroup("blog") {
		t.Error("blog group must not exist once an explicit schema aliases post")
	}

	// An explicit blog without a post alias leaves post on its default route.
	snap = sitemapSnap(t, sitemapSchema("post", ""), sitemapSchema("blog", `{"url_pattern":"/journal/{slug}","archive_path":"/journal"}`))
	plan = planSitemap(basePlanInput(snap, entry("blog", "a", "2025-01-03", nil), entry("post", "b", "2025-01-02", nil)))
	if j, _ := plan.GroupURLs("journal"); !reflect.DeepEqual(urlsOf(j), []string{"/journal", "/journal/a"}) {
		t.Errorf("journal = %v", urlsOf(j))
	}
	if p, _ := plan.GroupURLs("blog"); !reflect.DeepEqual(urlsOf(p), []string{"/blog", "/blog/b"}) {
		t.Errorf("blog = %v", urlsOf(p))
	}
}

func TestSitemapBuilder_SettingsAndPlugins(t *testing.T) {
	t.Parallel()
	snap := sitemapSnap(t,
		sitemapSchema("page", `{"sitemap":{"priority":0.3,"changefreq":"yearly"}}`),
		sitemapSchema("post", `{"sitemap":{"exclude":true}}`),
		sitemapSchema("case", ""),
		sitemapSchema("doc", `{"url_pattern":"/docs/{parent}/{slug}"}`),
	)
	in := basePlanInput(snap,
		entry("page", "about", "2025-01-01", nil),
		entry("post", "hidden", "2025-01-01", nil),
		entry("case", "study", "2025-01-01", nil),
		entry("doc", "leaf", "2025-01-01", func(e *entity.SitemapEntry) { e.ParentSlug = "branch" }),
		entry("doc", "bare", "2025-01-01", nil),
	)
	in.Plugins = sitemapPluginHooks{Groups: []namedArchiveGroup{{Key: "cases", ArchiveGroupConfig: ArchiveGroupConfig{PathPrefix: "cases/", SchemaSlugs: []string{"case"}, ArchiveIndexPath: "cases"}}}}
	plan := planSitemap(in)

	misc, _ := plan.GroupURLs("misc")
	if want := []string{"/about"}; !reflect.DeepEqual(urlsOf(misc), want) {
		t.Errorf("misc = %v, want %v", urlsOf(misc), want)
	}
	if misc[0].Priority != 0.3 || misc[0].ChangeFreq != "yearly" {
		t.Errorf("settings stats = %v %v", misc[0].Priority, misc[0].ChangeFreq)
	}
	docs, _ := plan.GroupURLs("docs")
	if want := []string{"/docs/branch/leaf", "/docs/bare"}; !reflect.DeepEqual(urlsOf(docs), want) {
		t.Errorf("docs = %v, want %v", urlsOf(docs), want)
	}
	if plan.HasGroup("blog") {
		t.Error("excluded schema must not produce its group")
	}
	cases, ok := plan.GroupURLs("cases")
	if !ok || !reflect.DeepEqual(urlsOf(cases), []string{"/cases", "/cases/study"}) {
		t.Errorf("plugin group = %v ok=%v", urlsOf(cases), ok)
	}
}

// An archive with nothing published in it is an empty page, so neither the
// archive nor its group is advertised.
func TestSitemapBuilder_EmptyArchiveNotListed(t *testing.T) {
	t.Parallel()
	snap := sitemapSnap(t, sitemapSchema("page", ""), sitemapSchema("post", ""))
	plan := planSitemap(basePlanInput(snap))
	if plan.HasGroup("blog") {
		t.Error("blog group listed with no published posts")
	}
	if urls, _ := plan.GroupURLs("misc"); len(urls) != 0 {
		t.Errorf("misc = %v", urlsOf(urls))
	}
	plan = planSitemap(basePlanInput(snap, entry("post", "hello", "2025-01-01", nil)))
	if got, _ := plan.GroupURLs("blog"); !reflect.DeepEqual(urlsOf(got), []string{"/blog", "/blog/hello"}) {
		t.Errorf("blog = %v", urlsOf(got))
	}
}

// The homepage entity is listed once, as "/", in misc.
func TestSitemapBuilder_HomepageListedAtRoot(t *testing.T) {
	t.Parallel()
	snap := sitemapSnap(t, sitemapSchema("page", ""))
	id := uuid.New()
	in := basePlanInput(snap,
		entry("page", "landing", "2025-01-02", func(e *entity.SitemapEntry) { e.ID = id }),
		entry("page", "about", "2025-01-01", nil),
	)
	in.HomepageID = id
	misc, _ := planSitemap(in).GroupURLs("misc")
	if want := []string{"/", "/about"}; !reflect.DeepEqual(urlsOf(misc), want) {
		t.Errorf("misc = %v, want %v", urlsOf(misc), want)
	}
	// Without a configured homepage the conventional slug "home" is the root.
	in = basePlanInput(snap, entry("page", "home", "2025-01-02", nil))
	if misc, _ := planSitemap(in).GroupURLs("misc"); !reflect.DeepEqual(urlsOf(misc), []string{"/"}) {
		t.Errorf("home slug = %v", urlsOf(misc))
	}
}

// A path a redirect rule claims answers 3xx, so it is not listed (the
// preloader walks the sitemap and would report it as a failure).
func TestSitemapBuilder_RedirectedPathsDropped(t *testing.T) {
	t.Parallel()
	snap := sitemapSnap(t, sitemapSchema("page", ""))
	in := basePlanInput(snap,
		entry("page", "platform", "2025-01-02", nil),
		entry("page", "about", "2025-01-01", nil),
	)
	in.Redirected = func(path string) bool { return path == "/platform" }
	if misc, _ := planSitemap(in).GroupURLs("misc"); !reflect.DeepEqual(urlsOf(misc), []string{"/about"}) {
		t.Errorf("misc = %v, want [/about]", urlsOf(misc))
	}
}
