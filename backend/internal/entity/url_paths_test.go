package entity

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"hatamoto.dev/core/internal/urlpattern"
)

func TestFlattenAttributes(t *testing.T) {
	t.Parallel()
	got := FlattenAttributes(json.RawMessage(`{"a":"x","b":true,"c":2,"d":{"n":1},"e":null}`))
	want := map[string]string{"a": "x", "b": "true", "c": "2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
	if FlattenAttributes(nil) != nil || FlattenAttributes(json.RawMessage(`[`)) != nil {
		t.Fatal("empty/invalid should be nil")
	}
}

func buildSnap(t *testing.T, schemas ...*EntitySchema) *URLSnapshot {
	t.Helper()
	snap, err := BuildURLSnapshot(schemas, nil)
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func TestLocate_Defaults(t *testing.T) {
	snap := DefaultURLSnapshot()
	tests := []struct {
		schema  string
		slug    string
		want    EntityLocation
		wantErr bool
	}{
		{schema: "post", slug: "x", want: EntityLocation{Path: "/blog/x", ArchivePath: "/blog", ArchiveName: "Post"}},
		{schema: "blog", slug: "x", want: EntityLocation{Path: "/blog/x", ArchivePath: "/blog", ArchiveName: "Post"}},
		{schema: "page", slug: "x", want: EntityLocation{Path: "/x"}},
		{schema: "page", slug: "", wantErr: true},
	}
	for _, tt := range tests {
		got, found, err := snap.Locate(tt.schema, nil, urlpattern.Vars{Slug: tt.slug})
		if tt.wantErr {
			if !errors.Is(err, urlpattern.ErrMissingVar) {
				t.Errorf("%s: err = %v, want ErrMissingVar", tt.schema, err)
			}
			continue
		}
		if err != nil || !found || got != tt.want {
			t.Errorf("%s = %+v found=%v err=%v, want %+v", tt.schema, got, found, err, tt.want)
		}
	}
	if _, found, _ := snap.Locate("doc", nil, urlpattern.Vars{Slug: "x"}); found {
		t.Error("unknown schema must not be found")
	}
}

func TestLocate_CustomPatternsOverridesAndSections(t *testing.T) {
	snap := buildSnap(t,
		schemaWith("page", `{"section_pages":true}`),
		schemaWith("doc", `{"url_pattern":"/docs/{slug}","archive_path":"/docs","overrides":[{"attribute":"format","equals":"report","url_pattern":"/reports/{slug}","sitemap_group":"misc"}]}`),
	)

	loc, _, err := snap.Locate("doc", nil, urlpattern.Vars{Slug: "intro"})
	if err != nil || loc.Path != "/docs/intro" || loc.ArchivePath != "/docs" {
		t.Errorf("doc = %+v err=%v", loc, err)
	}
	// An override moves the entity and drops the section crumb.
	loc, _, err = snap.Locate("doc", map[string]string{"format": "report"}, urlpattern.Vars{Slug: "intro"})
	if err != nil || loc.Path != "/reports/intro" || loc.ArchivePath != "" {
		t.Errorf("override = %+v err=%v", loc, err)
	}
	// A page named like a section prefix is that section's landing page.
	loc, _, _ = snap.Locate("page", nil, urlpattern.Vars{Slug: "docs"})
	if loc.Path != "/docs" || !loc.Section {
		t.Errorf("section page = %+v", loc)
	}
	if loc, _, _ = snap.Locate("page", nil, urlpattern.Vars{Slug: "about"}); loc.Path != "/about" || loc.Section {
		t.Errorf("plain page = %+v", loc)
	}
}

func TestLocate_BuilderPrecedence(t *testing.T) {
	RegisterEntityURLBuilder("plugged", func(baseURL, slug string) string { return baseURL + "/plugged/" + slug })
	RegisterEntityURLBuilder("pinned", func(baseURL, slug string) string { return baseURL + "/ignored/" + slug })
	t.Cleanup(func() {
		entityURLBuildersMu.Lock()
		delete(entityURLBuilders, "plugged")
		delete(entityURLBuilders, "pinned")
		entityURLBuildersMu.Unlock()
	})

	snap := buildSnap(t, schemaWith("plugged", ""), schemaWith("pinned", `{"url_pattern":"/real/{slug}"}`), schemaWith("page", ""))

	// Builder beats the default pattern ...
	if loc, _, _ := snap.Locate("plugged", nil, urlpattern.Vars{Slug: "x"}); loc.Path != "/plugged/x" {
		t.Errorf("builder over default: %+v", loc)
	}
	// ... and applies even when the schema is not in the snapshot ...
	if loc, found, _ := DefaultURLSnapshot().Locate("plugged", nil, urlpattern.Vars{Slug: "x"}); !found || loc.Path != "/plugged/x" {
		t.Errorf("builder without schema: %+v found=%v", loc, found)
	}
	// ... but explicit settings beat the builder.
	if loc, _, _ := snap.Locate("pinned", nil, urlpattern.Vars{Slug: "x"}); loc.Path != "/real/x" {
		t.Errorf("explicit over builder: %+v", loc)
	}
}

func TestEntityPath(t *testing.T) {
	snap := buildSnap(t, schemaWith("page", ""), schemaWith("doc", `{"url_pattern":"/docs/{parent}/{slug}"}`))

	ent := &Entity{Slug: "x", Schema: schemaWith("doc", "")}
	if got := snap.EntityPath(ent, []string{"guide"}); got != "/docs/guide/x" {
		t.Errorf("parent path = %q", got)
	}
	if got := snap.EntityPath(ent, nil); got != "/docs/x" {
		t.Errorf("no parent = %q", got)
	}
	if got := snap.EntityPath(&Entity{Slug: "", Schema: schemaWith("doc", "")}, nil); got != "/" {
		t.Errorf("empty slug falls back to /slug, got %q", got)
	}
	if got := snap.EntityPath(&Entity{Slug: "x"}, nil); got != "/x" {
		t.Errorf("nil schema = %q", got)
	}
	if !snap.NeedsAncestors("doc", nil) || snap.NeedsAncestors("page", nil) {
		t.Error("NeedsAncestors")
	}
}

type staticLister struct{ schemas []*EntitySchema }

func (l staticLister) List(context.Context) ([]*EntitySchema, error) { return l.schemas, nil }

func TestBuildEntityURLForJSONLD_UsesRegistry(t *testing.T) {
	ent := &Entity{Slug: "hello", Schema: schemaWith("blog", "")}
	if got := BuildEntityURLForJSONLD(ent, "https://e.com"); got != "https://e.com/blog/hello" {
		t.Fatalf("defaults = %q", got)
	}

	SetURLRegistry(NewURLRegistry(staticLister{[]*EntitySchema{schemaWith("blog", `{"url_pattern":"/journal/{slug}"}`)}}, nil))
	t.Cleanup(func() { SetURLRegistry(nil) })
	if got := BuildEntityURLForJSONLD(ent, "https://e.com"); got != "https://e.com/journal/hello" {
		t.Errorf("configured = %q", got)
	}
	doc := map[string]any{}
	buildArticleJSONLD(doc, ent, map[string]any{}, "https://e.com")
	if doc["url"] != "https://e.com/journal/hello" || doc["@id"] != "https://e.com/journal/hello#article" {
		t.Errorf("article url = %v / %v", doc["url"], doc["@id"])
	}
}

func TestVarsForAndTimezone(t *testing.T) {
	t.Parallel()
	snap, err := BuildURLSnapshot([]*EntitySchema{
		schemaWith("page", `{"url_pattern":"/{path}"}`),
		schemaWith("post", `{"url_pattern":"/{year}/{month}/{day}/{slug}"}`),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// 2024-05-31 20:00 UTC is already 1 June in Sydney.
	when := time.Date(2024, 5, 31, 20, 0, 0, 0, time.UTC)
	post := &Entity{Slug: "hello-world", PublishedAt: &when, Schema: schemaWith("post", ""), CreatedAt: when.AddDate(-1, 0, 0)}
	if got := snap.EntityPath(post, nil); got != "/2024/05/31/hello-world" {
		t.Errorf("UTC date url = %q", got)
	}
	syd, err := time.LoadLocation("Australia/Sydney")
	if err != nil {
		t.Skip("no tzdata")
	}
	snap.loc = syd
	if got := snap.EntityPath(post, nil); got != "/2024/06/01/hello-world" {
		t.Errorf("site time zone date url = %q", got)
	}
	// A draft has no published_at yet: created_at stands in.
	post.PublishedAt = nil
	if got := snap.EntityPath(post, nil); got != "/2023/06/01/hello-world" {
		t.Errorf("draft date url = %q", got)
	}

	page := &Entity{Slug: "local", Schema: schemaWith("page", "")}
	if got := snap.EntityPath(page, []string{"services", "seo"}); got != "/services/seo/local" {
		t.Errorf("nested url = %q", got)
	}
	if got := snap.EntityPath(page, nil); got != "/local" {
		t.Errorf("root url = %q", got)
	}
	if !snap.NeedsAncestors("page", nil) || snap.NeedsAncestors("post", nil) {
		t.Error("NeedsAncestors")
	}
	if !snap.NeedsEntityVars("post") || !snap.NeedsEntityVars("page") || snap.NeedsEntityVars("unknown") {
		t.Error("NeedsEntityVars")
	}
}

type stubLister []*EntitySchema

func (s stubLister) List(context.Context) ([]*EntitySchema, error) { return s, nil }

func TestRegistryTimezone(t *testing.T) {
	t.Parallel()
	reg := NewURLRegistry(stubLister{schemaWith("post", `{"url_pattern":"/{year}/{slug}"}`)}, nil)
	snap, _ := reg.Snapshot(context.Background())
	if snap.Location() != time.UTC {
		t.Error("default location must be UTC")
	}
	reg.SetTimezone(func(context.Context) string { return "Europe/Paris" })
	snap, _ = reg.Snapshot(context.Background())
	if snap.Location().String() != "Europe/Paris" {
		t.Errorf("location = %v", snap.Location())
	}
	reg.SetTimezone(func(context.Context) string { return "Not/AZone" })
	snap, _ = reg.Snapshot(context.Background())
	if snap.Location() != time.UTC {
		t.Error("an unknown zone falls back to UTC")
	}
}

// The section crumb reads archive_crumb_label, else archive_title, else the
// schema name.
func TestLocate_SectionCrumbLabel(t *testing.T) {
	for name, tc := range map[string]struct{ settings, want string }{
		"label wins":          {`{"url_pattern":"/team/{slug}","archive_path":"/team","archive_title":"Meet the team","archive_crumb_label":"Team"}`, "Team"},
		"title when no label": {`{"url_pattern":"/team/{slug}","archive_path":"/team","archive_title":"Meet the team"}`, "Meet the team"},
		"blank label skipped": {`{"url_pattern":"/team/{slug}","archive_path":"/team","archive_title":"Meet the team","archive_crumb_label":"  "}`, "Meet the team"},
		"schema name last":    {`{"url_pattern":"/team/{slug}","archive_path":"/team"}`, "Person"},
	} {
		sc := schemaWith("person", tc.settings)
		sc.Name = "Person"
		snap := buildSnap(t, sc)
		loc, _, err := snap.Locate("person", nil, urlpattern.Vars{Slug: "ada"})
		if err != nil || loc.ArchiveName != tc.want || loc.ArchivePath != "/team" {
			t.Errorf("%s: %+v err=%v, want crumb %q", name, loc, err, tc.want)
		}
	}
}
