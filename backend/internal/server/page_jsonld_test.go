package server

import (
	"context"
	"encoding/json/jsontext"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/person"
)

const pageLDBase = "https://example.test"

func pageLDEntity(slug, settings, attrs string) *entity.Entity {
	pub := time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)
	return &entity.Entity{
		ID: uuid.New(), Name: "Hello", Slug: "hello", PublishedAt: &pub, UpdatedAt: pub,
		Attributes: jsontext.Value(attrs),
		Schema: &entity.EntitySchema{Slug: slug, Name: slug,
			Schema: jsontext.Value(`{"type":"object"}`), Settings: jsontext.Value(settings)},
	}
}

func noBylines() *bylines {
	return &bylines{credits: map[uuid.UUID]person.Credit{}, people: map[uuid.UUID]*person.Person{},
		path: func(_ context.Context, slug string) string { return "/author/" + slug }}
}

func ldGraphTypes(doc map[string]any) []any {
	g, _ := doc["@graph"].([]map[string]any)
	var out []any
	for _, n := range g {
		out = append(out, n["@type"])
	}
	return out
}

// With no keys the document is what the generator and the canonical rewrite
// always produced.
func TestBuildPageJSONLDDefaultsUnchanged(t *testing.T) {
	ent := pageLDEntity("page", `{}`, `{"description":"d"}`)
	want, err := entity.GenerateJSONLDWithFAQ(ent, pageLDBase, nil)
	if err != nil {
		t.Fatal(err)
	}
	entity.ApplyCanonicalToJSONLD(want, "page", pageLDBase+"/hello")
	got, noBC, err := buildPageJSONLD(context.Background(), ent, pageLDBase, "/hello", nil, noBylines())
	if err != nil || noBC || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v (noBC=%v err=%v)\nwant %v", got, noBC, err, want)
	}
}

// Both response branches call buildPageJSONLD, so the switches are checked here once.
func TestBuildPageJSONLDWebPageSwitch(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name, slug, settings, attrs string
		faq                         bool
		wantType                    string // single doc
		wantGraph                   []any  // or graph node types
	}{
		{"schema off drops WebPage", "page", `{"emit_webpage":"off"}`, `{}`, false, "", nil},
		{"entry on beats schema off", "page", `{"emit_webpage":"off"}`, `{"jsonld_webpage":"on"}`, false, "WebPage", nil},
		{"entry off beats schema on", "page", `{"emit_webpage":"on"}`, `{"jsonld_webpage":"off"}`, false, "", nil},
		{"on adds WebPage to a typed doc", "glossary", `{"schema_org_type":"DefinedTerm","emit_webpage":"on"}`, `{}`, false, "", []any{"DefinedTerm", "WebPage"}},
		{"off keeps a typed doc", "glossary", `{"schema_org_type":"DefinedTerm","emit_webpage":"off"}`, `{}`, false, "DefinedTerm", nil},
		{"off on a FAQ graph keeps the FAQPage", "page", `{"emit_webpage":"off"}`, `{"faq":[{"question":"Q?","answer":"A."}]}`, true, "", []any{"FAQPage"}},
		{"on typed doc with FAQ appends WebPage", "glossary", `{"schema_org_type":"DefinedTerm","emit_webpage":"on"}`, `{"faq":[{"question":"Q?","answer":"A."}]}`, true, "", []any{"DefinedTerm", "FAQPage", "WebPage"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ent := pageLDEntity(tc.slug, tc.settings, tc.attrs)
			doc, _, err := buildPageJSONLD(ctx, ent, pageLDBase, "/hello", pageFAQItems(ent), noBylines())
			if err != nil {
				t.Fatal(err)
			}
			if tc.wantGraph != nil {
				if got := ldGraphTypes(doc); !reflect.DeepEqual(got, tc.wantGraph) {
					t.Fatalf("graph types %v, want %v (%v)", got, tc.wantGraph, doc)
				}
				return
			}
			if got, _ := doc["@type"].(string); got != tc.wantType {
				t.Fatalf("@type %q, want %q (%v)", got, tc.wantType, doc)
			}
		})
	}
}

func TestBuildPageJSONLDBreadcrumbSwitch(t *testing.T) {
	for _, tc := range []struct {
		settings, attrs string
		want            bool
	}{
		{`{}`, `{}`, false},
		{`{"emit_breadcrumb":"off"}`, `{}`, true},
		{`{"emit_breadcrumb":"off"}`, `{"jsonld_breadcrumb":"on"}`, false},
		{`{"emit_breadcrumb":"on"}`, `{"jsonld_breadcrumb":"off"}`, true},
	} {
		ent := pageLDEntity("page", tc.settings, tc.attrs)
		_, got, err := buildPageJSONLD(context.Background(), ent, pageLDBase, "/hello", nil, noBylines())
		if err != nil || got != tc.want {
			t.Errorf("%s %s: noBreadcrumbLD = %v, want %v (%v)", tc.settings, tc.attrs, got, tc.want, err)
		}
	}
	rr := &routeResult{Page: &PublicPageResponse{NoBreadcrumbLD: true}}
	if v := pageViewFromRoute(rr, pageLDBase, "/hello"); !v.NoBreadcrumbLD {
		t.Error("the flag must reach the PageView")
	}
}

// A section list reaches the page as FAQPage: from page_doc and legacy sections.
func TestPageFAQItemsFromSections(t *testing.T) {
	doc := `{"page_doc":{"v":2,"nodes":[{"id":"a","t":"component","p":{"faq":[{"question":"Where?","answer":"<p>Here.</p>"}]}}]}}`
	legacy := `{"page_sections":[{"type":"faq","content":{"faq":[{"question":"When?","answer":"Now."}]}}]}`
	for name, attrs := range map[string]string{"page_doc": doc, "page_sections": legacy} {
		ent := pageLDEntity("page", `{}`, attrs)
		items := pageFAQItems(ent)
		if len(items) != 1 {
			t.Fatalf("%s: items %v", name, items)
		}
		out, _, err := buildPageJSONLD(context.Background(), ent, pageLDBase, "/hello", items, noBylines())
		if err != nil {
			t.Fatal(err)
		}
		if got := ldGraphTypes(out); !reflect.DeepEqual(got, []any{"WebPage", "FAQPage"}) {
			t.Errorf("%s: graph %v", name, got)
		}
	}
	// The schema's off switch still wins.
	ent := pageLDEntity("page", `{"faq_from_content":false}`, doc)
	if items := pageFAQItems(ent); items != nil {
		t.Errorf("faq_from_content=false: %v", items)
	}
}

func TestApplyAuthorFrom(t *testing.T) {
	ctx := context.Background()
	pub := &person.Person{ID: uuid.New(), Slug: "ada", Name: "Ada Lovelace", Public: true}
	priv := &person.Person{ID: uuid.New(), Slug: "bob", Name: "Bob", Public: false}
	lookup := func(_ context.Context, ref string) *person.Person {
		switch ref {
		case "ada":
			return pub
		case "bob":
			return priv
		}
		return nil
	}
	st := entity.SchemaURLSettings{AuthorFrom: "writer"}
	run := func(b *bylines, ent *entity.Entity, doc map[string]any) map[string]any {
		b.person = lookup
		b.applyAuthorFrom(ctx, ent, st, pageLDBase, doc)
		return doc
	}
	article := func() map[string]any {
		return map[string]any{"@type": "Article", "author": map[string]any{"@type": "Person", "name": "Plain"}}
	}
	ent := pageLDEntity("post", `{}`, `{"writer":"ada"}`)

	got := run(noBylines(), ent, article())["author"]
	want := map[string]any{"@type": "Person", "@id": pageLDBase + "/author/ada#person", "name": "Ada Lovelace"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("public person: %v", got)
	}
	// Inside a @graph the first node is the entity.
	g := map[string]any{"@graph": []map[string]any{article(), {"@type": "FAQPage"}}}
	run(noBylines(), ent, g)
	if first := g["@graph"].([]map[string]any)[0]; !reflect.DeepEqual(first["author"], want) {
		t.Errorf("graph author: %v", first["author"])
	}
	// Not public, unknown, or missing attribute: the string author stays.
	for _, attrs := range []string{`{"writer":"bob"}`, `{"writer":"nobody"}`, `{}`} {
		if a := run(noBylines(), pageLDEntity("post", `{}`, attrs), article())["author"]; !reflect.DeepEqual(a, map[string]any{"@type": "Person", "name": "Plain"}) {
			t.Errorf("%s: author changed to %v", attrs, a)
		}
	}
	// A byline column wins.
	by := noBylines()
	pid := uuid.New()
	by.credits[ent.ID] = person.Credit{PersonID: &pid}
	if a := run(by, ent, article())["author"]; strings.Contains(authorNameOf(a), "Ada") {
		t.Errorf("byline must win: %v", a)
	}
	// No author_from: untouched.
	b := noBylines()
	b.person = lookup
	d := article()
	b.applyAuthorFrom(ctx, ent, entity.SchemaURLSettings{}, pageLDBase, d)
	if !reflect.DeepEqual(d, article()) {
		t.Errorf("unconfigured: %v", d)
	}
}

func authorNameOf(v any) string {
	m, _ := v.(map[string]any)
	n, _ := m["name"].(string)
	return n
}
