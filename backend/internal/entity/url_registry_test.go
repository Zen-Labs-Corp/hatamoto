package entity

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/urlpattern"
)

func schemaWith(slug, settings string) *EntitySchema {
	s := &EntitySchema{ID: uuid.New(), Name: slug, Slug: slug}
	if settings != "" {
		s.Settings = json.RawMessage(settings)
	}
	return s
}

type fakeLister struct {
	schemas []*EntitySchema
	calls   int
}

func (f *fakeLister) List(context.Context) ([]*EntitySchema, error) {
	f.calls++
	return f.schemas, nil
}

func urlFor(t *testing.T, snap *URLSnapshot, schema string, attrs map[string]string, v urlpattern.Vars) string {
	t.Helper()
	u, ok, err := snap.Router().URLFor(schema, attrs, v)
	if err != nil || !ok {
		t.Fatalf("URLFor(%s) = %q,%v,%v", schema, u, ok, err)
	}
	return u
}

// With empty settings the registry must reproduce the core defaults.
func TestBuildURLSnapshot_Defaults(t *testing.T) {
	t.Parallel()
	snap, err := BuildURLSnapshot([]*EntitySchema{schemaWith("page", ""), schemaWith("post", `{}`), schemaWith("thing", "")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		schema string
		vars   urlpattern.Vars
		want   string
	}{
		{"page", urlpattern.Vars{Slug: "x"}, "/x"},
		{"thing", urlpattern.Vars{Slug: "x"}, "/x"},
		{"post", urlpattern.Vars{Slug: "x"}, "/blog/x"},
		{"blog", urlpattern.Vars{Slug: "x"}, "/blog/x"},
		{"article", urlpattern.Vars{Slug: "x"}, "/blog/x"},
		{"blog_post", urlpattern.Vars{Slug: "x"}, "/blog/x"},
	}
	for _, tc := range tests {
		if got := urlFor(t, snap, tc.schema, nil, tc.vars); got != tc.want {
			t.Errorf("%s: %q want %q", tc.schema, got, tc.want)
		}
	}
	post, _ := snap.Config("blog")
	if post.SchemaSlug != "post" || post.ArchivePath != "/blog" || post.SitemapGroup != "blog" || post.Explicit {
		t.Errorf("post config = %+v", post)
	}
	page, _ := snap.Config("page")
	if page.SitemapGroup != "misc" || page.ArchivePath != "" {
		t.Errorf("page config = %+v", page)
	}
	if _, ok := snap.Config("nope"); ok {
		t.Error("unknown schema should not resolve")
	}
	// /blog/x is a post, /x a page (page and thing share the default pattern).
	r := snap.Router()
	if c := r.Resolve("/blog/x"); len(c) < 1 || c[0].SchemaSlug != "post" {
		t.Errorf("resolve /blog/x = %+v", c)
	}
	if c := r.Resolve("/x"); len(c) != 2 {
		t.Errorf("resolve /x = %+v (page and thing share the default pattern)", c)
	}
}

// A legacy-style database with a `blog` row but no `post` row still gets the
// post defaults, and `post` resolves to it.
func TestBuildURLSnapshot_PostFamilyOwner(t *testing.T) {
	t.Parallel()
	snap, err := BuildURLSnapshot([]*EntitySchema{schemaWith("blog", ""), schemaWith("article", "")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Configs()) != 1 {
		t.Fatalf("expected a single family route, got %d", len(snap.Configs()))
	}
	own, _ := snap.Router().CanonicalSchema("article")
	if own != "article" && own != "blog" {
		t.Fatalf("canonical = %q", own)
	}
	if got := urlFor(t, snap, "post", nil, urlpattern.Vars{Slug: "x"}); got != "/blog/x" {
		t.Errorf("post alias URL = %q", got)
	}
}

func TestBuildURLSnapshot_ExplicitSettings(t *testing.T) {
	t.Parallel()
	snap, err := BuildURLSnapshot([]*EntitySchema{
		schemaWith("page", ""),
		schemaWith("post", `{"url_pattern":"/news/{slug}","archive_path":"/news","aliases":["story"]}`),
		schemaWith("blog", `{"url_pattern":"/journal/{slug}","sitemap_group":"journal"}`),
		schemaWith("doc", `{"url_pattern":"/docs/{slug}","overrides":[{"attribute":"format","equals":"report","url_pattern":"/reports/{slug}","sitemap_group":"misc"},{"attribute":"kind","equals":"memo","url_pattern":"/memos/{slug}"}],"sitemap":{"priority":0.4,"changefreq":"yearly"},"section_pages":true}`),
		schemaWith("person", `{"url_pattern":"/people/{parent}/{slug}","sitemap_group":"crew"}`),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	v := urlpattern.Vars{Slug: "x"}
	tests := []struct {
		schema string
		attrs  map[string]string
		vars   urlpattern.Vars
		want   string
	}{
		{"post", nil, v, "/news/x"},
		{"story", nil, v, "/news/x"},
		{"blog", nil, v, "/journal/x"}, // explicit settings opt out of the post family
		{"doc", nil, v, "/docs/x"},
		{"doc", map[string]string{"format": "report"}, v, "/reports/x"},
		{"person", nil, urlpattern.Vars{Parent: "p", Slug: "x"}, "/people/p/x"},
		{"person", nil, v, "/people/x"},
	}
	for _, tc := range tests {
		if got := urlFor(t, snap, tc.schema, tc.attrs, tc.vars); got != tc.want {
			t.Errorf("%s %v: %q want %q", tc.schema, tc.attrs, got, tc.want)
		}
	}
	doc, _ := snap.Config("doc")
	if doc.SitemapGroup != "docs" || !doc.SectionPages || !doc.Explicit ||
		doc.Sitemap.ChangeFreq != "yearly" || *doc.Sitemap.Priority != 0.4 {
		t.Errorf("doc = %+v", doc)
	}
	groups := []string{doc.Overrides[0].SitemapGroup, doc.Overrides[1].SitemapGroup}
	if !reflect.DeepEqual(groups, []string{"misc", "memos"}) {
		t.Errorf("override groups = %v", groups)
	}
	if blog, _ := snap.Config("blog"); blog.SitemapGroup != "journal" {
		t.Errorf("blog group = %q", blog.SitemapGroup)
	}
	if person, _ := snap.Config("person"); person.SitemapGroup != "crew" {
		t.Errorf("person group = %q", person.SitemapGroup)
	}
	// Explicit post (news) + explicit blog: no default family owner remains.
	if c := snap.Router().Resolve("/blog/x"); len(c) != 0 {
		t.Errorf("/blog/x should not resolve once post has its own pattern: %+v", c)
	}
}

// An explicit schema that claims a post-family slug (own slug or alias) owns
// the family: the empty default `post` must not register /post or sitemap rows.
func TestBuildURLSnapshot_ExplicitSchemaClaimsPostFamily(t *testing.T) {
	t.Parallel()
	snap, err := BuildURLSnapshot([]*EntitySchema{
		schemaWith("blog", `{"url_pattern":"/journal/{slug}","archive_path":"/journal","aliases":["post"]}`),
		schemaWith("post", ""),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if c := snap.Router().Resolve("/blog/x"); len(c) != 0 {
		t.Errorf("/blog/x must not resolve: %+v", c)
	}
	if c := snap.Router().Resolve("/journal/x"); len(c) != 1 || c[0].SchemaSlug != "blog" {
		t.Errorf("/journal/x = %+v", c)
	}
	for _, cfg := range snap.Configs() {
		if cfg.SchemaSlug == "post" {
			t.Errorf("post must not be routed or in the sitemap: %+v", cfg)
		}
	}
	if own, ok := snap.Router().CanonicalSchema("post"); !ok || own != "blog" {
		t.Errorf("archive alias post -> %q,%v; want blog", own, ok)
	}
	if cfg, _ := snap.Config("blog"); cfg.ArchivePath != "/journal" {
		t.Errorf("blog archive = %q", cfg.ArchivePath)
	}
}

func TestBuildURLSnapshot_NeutralGroupFallbacks(t *testing.T) {
	t.Parallel()
	snap, err := BuildURLSnapshot([]*EntitySchema{
		schemaWith("flat", `{"url_pattern":"/{slug}"}`),
		schemaWith("sec", `{"url_pattern":"/a/b/{slug}"}`),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if c, _ := snap.Config("flat"); c.SitemapGroup != "misc" {
		t.Errorf("flat group = %q", c.SitemapGroup)
	}
	if c, _ := snap.Config("sec"); c.SitemapGroup != "a" {
		t.Errorf("sec group = %q", c.SitemapGroup)
	}
}

func TestBuildURLSnapshot_InvalidSettingsFallBack(t *testing.T) {
	t.Parallel()
	snap, err := BuildURLSnapshot([]*EntitySchema{
		schemaWith("good", `{"url_pattern":"/g/{slug}"}`),
		schemaWith("bad", `{"url_pattern":"/nope"}`),
		schemaWith("garbage", `[1,2]`),
	}, nil)
	if err != nil {
		t.Fatalf("invalid stored settings must not fail the build: %v", err)
	}
	if got := urlFor(t, snap, "bad", nil, urlpattern.Vars{Slug: "x"}); got != "/x" {
		t.Errorf("bad = %q", got)
	}
	if got := urlFor(t, snap, "garbage", nil, urlpattern.Vars{Slug: "x"}); got != "/x" {
		t.Errorf("garbage = %q", got)
	}
	if got := urlFor(t, snap, "good", nil, urlpattern.Vars{Slug: "x"}); got != "/g/x" {
		t.Errorf("good = %q", got)
	}
}

func TestURLRegistry_CacheAndInvalidate(t *testing.T) {
	t.Parallel()
	l := &fakeLister{schemas: []*EntitySchema{schemaWith("doc", `{"url_pattern":"/docs/{slug}"}`)}}
	now := time.Unix(1000, 0)
	r := NewURLRegistry(l, nil)
	r.now = func() time.Time { return now }
	ctx := context.Background()

	u, ok, err := r.URLFor(ctx, "doc", nil, urlpattern.Vars{Slug: "x"})
	if err != nil || !ok || u != "/docs/x" {
		t.Fatalf("got %q %v %v", u, ok, err)
	}
	_, _ = r.Snapshot(ctx)
	if l.calls != 1 {
		t.Fatalf("snapshot should be cached, calls=%d", l.calls)
	}

	l.schemas = []*EntitySchema{schemaWith("doc", `{"url_pattern":"/papers/{slug}"}`)}
	if u, _, _ := r.URLFor(ctx, "doc", nil, urlpattern.Vars{Slug: "x"}); u != "/docs/x" {
		t.Fatalf("stale cache expected before invalidate, got %q", u)
	}
	r.Invalidate()
	if u, _, _ := r.URLFor(ctx, "doc", nil, urlpattern.Vars{Slug: "x"}); u != "/papers/x" {
		t.Fatalf("after invalidate got %q", u)
	}

	l.schemas = []*EntitySchema{schemaWith("doc", `{"url_pattern":"/files/{slug}"}`)}
	now = now.Add(urlRegistryTTL + time.Second)
	if u, _, _ := r.URLFor(ctx, "doc", nil, urlpattern.Vars{Slug: "x"}); u != "/files/x" {
		t.Fatalf("after TTL got %q", u)
	}
}

func TestService_SchemaChangesInvalidateURLRegistry(t *testing.T) {
	t.Parallel()
	svc, _, _ := newTestService()
	ctx := context.Background()
	reg := svc.URLRegistry()

	created, err := svc.CreateSchema(ctx, &CreateSchemaInput{
		Name: "Doc", Slug: "doc", Schema: validSchemaJSON,
		Settings: json.RawMessage(`{"url_pattern":"/docs/{slug}"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if u, ok, _ := reg.URLFor(ctx, "doc", nil, urlpattern.Vars{Slug: "x"}); !ok || u != "/docs/x" {
		t.Fatalf("after create: %q %v", u, ok)
	}

	upd := json.RawMessage(`{"url_pattern":"/papers/{slug}"}`)
	if _, err := svc.UpdateSchema(ctx, created.ID, &UpdateSchemaInput{Settings: &upd}); err != nil {
		t.Fatal(err)
	}
	if u, _, _ := reg.URLFor(ctx, "doc", nil, urlpattern.Vars{Slug: "x"}); u != "/papers/x" {
		t.Fatalf("after update: %q", u)
	}

	if err := svc.DeleteSchema(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := reg.URLFor(ctx, "doc", nil, urlpattern.Vars{Slug: "x"}); ok {
		t.Fatal("deleted schema should no longer resolve")
	}
}

func TestService_RejectsInvalidSettings(t *testing.T) {
	t.Parallel()
	svc, _, _ := newTestService()
	_, err := svc.CreateSchema(context.Background(), &CreateSchemaInput{
		Name: "Doc", Slug: "doc", Schema: validSchemaJSON, Settings: json.RawMessage(`{"url_pattern":"/x"}`),
	})
	if err == nil {
		t.Fatal("expected validation error")
	}
}

func TestService_SchemaChangeCallback(t *testing.T) {
	t.Parallel()
	svc, _, _ := newTestService()
	ctx := context.Background()
	calls := 0
	svc.SetOnSchemaChange(func() { calls++ })

	created, err := svc.CreateSchema(ctx, &CreateSchemaInput{Name: "Doc", Slug: "doc", Schema: validSchemaJSON})
	if err != nil {
		t.Fatal(err)
	}
	upd := json.RawMessage(`{"archive_path":"/docs","url_pattern":"/docs/{slug}"}`)
	if _, err := svc.UpdateSchema(ctx, created.ID, &UpdateSchemaInput{Settings: &upd}); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteSchema(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatalf("callback ran %d times, want 3 (create, update, delete)", calls)
	}
	// A failed change must not purge anything.
	if _, err := svc.CreateSchema(ctx, &CreateSchemaInput{Name: "Bad", Slug: "bad", Schema: validSchemaJSON, Settings: json.RawMessage(`{"url_pattern":"/x"}`)}); err == nil {
		t.Fatal("expected validation error")
	}
	if calls != 3 {
		t.Fatalf("callback ran for a rejected change: %d", calls)
	}
}
