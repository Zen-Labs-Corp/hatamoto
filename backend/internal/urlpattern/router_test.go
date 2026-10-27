package urlpattern

import (
	"reflect"
	"testing"
)

func testRouter(t *testing.T) *Router {
	t.Helper()
	r, err := NewRouter([]SchemaRoute{
		{SchemaSlug: "page", Pattern: "/{slug}"},
		{SchemaSlug: "post", Aliases: []string{"blog", "article"}, Pattern: "/post/{slug}", SitemapGroup: "post"},
		{SchemaSlug: "doc", Pattern: "/docs/{slug}", Overrides: []Override{
			{Attribute: "format", Equals: "report", Pattern: "/reports/{slug}", SitemapGroup: "misc"},
		}},
		{SchemaSlug: "person", Pattern: "/people/{slug}"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestRouterResolve(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	type want struct {
		schema   string
		vars     Vars
		override bool
	}
	tests := []struct {
		name string
		path string
		want []want
	}{
		{"neutral literal", "/post/x", []want{{"post", Vars{Slug: "x"}, false}}},
		{"root page", "/x", []want{{"page", Vars{Slug: "x"}, false}}},
		{"literal section beats nothing", "/docs/x", []want{{"doc", Vars{Slug: "x"}, false}}},
		{"more literals first", "/people/x", []want{{"person", Vars{Slug: "x"}, false}}},
		{"override candidate", "/reports/x", []want{{"doc", Vars{Slug: "x"}, true}}},
		{"unknown", "/nope/a/b/c", nil},
		{"root", "/", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var got []want
			for _, c := range r.Resolve(tc.path) {
				got = append(got, want{c.SchemaSlug, c.Vars, c.Override != nil})
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %+v want %+v", got, tc.want)
			}
		})
	}
}

func TestRouterResolveOrdering(t *testing.T) {
	t.Parallel()
	r, err := NewRouter([]SchemaRoute{
		{SchemaSlug: "zeta", Pattern: "/{slug}"},
		{SchemaSlug: "alpha", Pattern: "/{parent}/{slug}"},
		{SchemaSlug: "sec", Pattern: "/docs/{parent}/{slug}"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := []string{}
	for _, c := range r.Resolve("/docs") {
		got = append(got, c.SchemaSlug)
	}
	if !reflect.DeepEqual(got, []string{"zeta", "alpha"}) {
		t.Fatalf("/docs: %v", got)
	}
	got = nil
	for _, c := range r.Resolve("/docs/x") {
		got = append(got, c.SchemaSlug)
	}
	// sec (1 literal) first, then alpha (parent skipped); zeta needs one segment.
	if !reflect.DeepEqual(got, []string{"sec", "alpha"}) {
		t.Fatalf("/docs/x: %v", got)
	}
}

func TestRouterAliases(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	for _, slug := range []string{"post", "blog", "article"} {
		if c, ok := r.CanonicalSchema(slug); !ok || c != "post" {
			t.Errorf("CanonicalSchema(%s) = %q,%v", slug, c, ok)
		}
		if got := r.SchemaSlugs(slug); !reflect.DeepEqual(got, []string{"post", "blog", "article"}) {
			t.Errorf("SchemaSlugs(%s) = %v", slug, got)
		}
		u, ok, err := r.URLFor(slug, nil, Vars{Slug: "x"})
		if err != nil || !ok || u != "/post/x" {
			t.Errorf("URLFor(%s) = %q,%v,%v", slug, u, ok, err)
		}
	}
	if _, ok := r.CanonicalSchema("nope"); ok {
		t.Error("unknown schema should not resolve")
	}
	if r.SchemaSlugs("nope") != nil {
		t.Error("unknown schema should have no slugs")
	}
	// Candidates carry the alias set.
	c := r.Resolve("/post/x")
	if len(c) != 1 || !reflect.DeepEqual(c[0].SchemaSlugs, []string{"post", "blog", "article"}) {
		t.Errorf("candidate = %+v", c)
	}
}

func TestRouterOwnSlugBeatsAlias(t *testing.T) {
	t.Parallel()
	r, err := NewRouter([]SchemaRoute{
		{SchemaSlug: "a", Aliases: []string{"b"}, Pattern: "/a/{slug}"},
		{SchemaSlug: "b", Pattern: "/b/{slug}"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if c, _ := r.CanonicalSchema("b"); c != "b" {
		t.Errorf("own slug must win over alias, got %q", c)
	}
}

func TestRouterURLFor(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	tests := []struct {
		name   string
		schema string
		attrs  map[string]string
		vars   Vars
		want   string
		known  bool
		err    bool
	}{
		{"page", "page", nil, Vars{Slug: "x"}, "/x", true, false},
		{"page missing slug", "page", nil, Vars{}, "", true, true},
		{"override applies", "doc", map[string]string{"format": "report"}, Vars{Slug: "x"}, "/reports/x", true, false},
		{"override not applying", "doc", map[string]string{"format": "memo"}, Vars{Slug: "x"}, "/docs/x", true, false},
		{"no attrs", "doc", nil, Vars{Slug: "x"}, "/docs/x", true, false},
		{"unknown schema", "nope", nil, Vars{Slug: "x"}, "", false, false},
	}
	for _, tc := range tests {
		got, known, err := r.URLFor(tc.schema, tc.attrs, tc.vars)
		if got != tc.want || known != tc.known || (err != nil) != tc.err {
			t.Errorf("%s: got %q,%v,%v", tc.name, got, known, err)
		}
	}
	tg, _ := r.Lookup("doc", map[string]string{"format": "report"})
	if tg.SitemapGroup != "misc" || tg.Override == nil {
		t.Errorf("override target = %+v", tg)
	}
	tg, _ = r.Lookup("post", nil)
	if tg.SitemapGroup != "post" || tg.Override != nil {
		t.Errorf("base target = %+v", tg)
	}
}

func TestNewRouterErrors(t *testing.T) {
	t.Parallel()
	if _, err := NewRouter([]SchemaRoute{{SchemaSlug: "x", Pattern: "bad"}}); err == nil {
		t.Error("bad base pattern should fail")
	}
	if _, err := NewRouter([]SchemaRoute{{SchemaSlug: "x", Pattern: "/{slug}", Overrides: []Override{{Attribute: "a", Equals: "b", Pattern: "bad"}}}}); err == nil {
		t.Error("bad override pattern should fail")
	}
	r, err := NewRouter(nil)
	if err != nil || len(r.Resolve("/x")) != 0 {
		t.Errorf("empty router: %v", err)
	}
}
