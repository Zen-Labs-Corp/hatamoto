package server

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/menu"
)

func TestHomepageRenderTitleAndNoIndexFromAttributes(t *testing.T) {
	// loadHomepage puts the entity name in meta.title and drops no_index.
	rr := &routeResult{Kind: resolveKindHome, Page: &PublicPageResponse{
		Entity: &entity.Entity{ID: uuid.New(), Name: "Hatamoto CMS",
			Attributes: json.RawMessage(`{"meta_title":"Hatamoto · The entity-first CMS","no_index":true}`)},
		Meta: PageMeta{Title: "Hatamoto CMS"},
	}}
	v := pageViewFromRoute(rr, "https://x.io", "/")
	if v.Page.Title != "Hatamoto · The entity-first CMS" {
		t.Errorf("title = %q", v.Page.Title)
	}
	if !v.Page.NoIndex {
		t.Error("no_index dropped")
	}
	if rr.Page.Meta.Title != "Hatamoto CMS" {
		t.Error("JSON meta must be untouched")
	}
}

func TestHomepageRenderTitleFallsBackToMeta(t *testing.T) {
	rr := &routeResult{Page: &PublicPageResponse{
		Entity: &entity.Entity{ID: uuid.New(), Name: "Home", Attributes: json.RawMessage(`{}`)},
		Meta:   PageMeta{Title: "Home"},
	}}
	if v := pageViewFromRoute(rr, "https://x.io", "/"); v.Page.Title != "Home" || v.Page.NoIndex {
		t.Errorf("got %+v", v.Page)
	}
}

func TestPageNavHrefUsesRegistry(t *testing.T) {
	snap := entity.DefaultURLSnapshot() // page: /{slug}
	home := &entity.Entity{ID: uuid.New(), Slug: "home"}
	about := &entity.Entity{ID: uuid.New(), Slug: "platform", Attributes: json.RawMessage(`{}`)}

	if h, ok := pageNavHref(context.Background(), snap, nil, home.ID, home); !ok || h != "/" {
		t.Errorf("home = %q %v", h, ok)
	}
	if h, ok := pageNavHref(context.Background(), snap, nil, home.ID, about); !ok || h != "/platform" {
		t.Errorf("page = %q %v, want /platform", h, ok)
	}
}

func TestContactFormSlugCanonicalFirst(t *testing.T) {
	for name, tc := range map[string]struct {
		content map[string]any
		want    string
	}{
		"both keys":   {map[string]any{"formSlug": "contact", "form_slug": "quote"}, "quote"},
		"alias only":  {map[string]any{"formSlug": "contact"}, "contact"},
		"empty canon": {map[string]any{"formSlug": "contact", "form_slug": ""}, "contact"},
		"non-string":  {map[string]any{"form_slug": 7.0}, ""},
	} {
		if got := contactFormSlug(tc.content); got != tc.want {
			t.Errorf("%s: %q, want %q", name, got, tc.want)
		}
	}
}

func TestMenuHrefUsesRegistry(t *testing.T) {
	homeID, otherID := uuid.New(), uuid.New()
	l := &navLinks{snap: entity.DefaultURLSnapshot(), homeID: homeID}
	str := func(s string) *string { return &s }
	for name, tc := range map[string]struct {
		it   *menu.MenuItem
		want string
	}{
		"home entity":  {&menu.MenuItem{EntityID: &homeID, EntitySlug: str("platform")}, "/"},
		"page entity":  {&menu.MenuItem{EntityID: &otherID, EntitySlug: str("about")}, "/about"},
		"post entity":  {&menu.MenuItem{EntityID: &otherID, EntitySlug: str("hello"), EntitySchemaSlug: str("post")}, "/blog/hello"},
		"explicit url": {&menu.MenuItem{URL: str("/x"), EntitySlug: str("about")}, "/x"},
		"no target":    {&menu.MenuItem{}, "#"},
	} {
		if got := l.menuHref(tc.it); got != tc.want {
			t.Errorf("%s: %q, want %q", name, got, tc.want)
		}
	}
}

func TestSectionsFromAttributesToleratesMalformedSiblings(t *testing.T) {
	good := `{"id":"g","type":"content","title":"Good","content":{"body":"x"},"sort_order":1}`
	for name, bad := range map[string]string{
		"string item":      `"str"`,
		"content string":   `{"type":"content","content":"x"}`,
		"content array":    `{"type":"content","content":[1]}`,
		"float sort":       `{"type":"content","sort_order":1.5}`,
		"string sort":      `{"type":"content","sort_order":"2"}`,
		"numeric title":    `{"type":"content","title":3}`,
		"numeric type":     `{"type":7}`,
		"null":             `null`,
		"visible not bool": `{"type":"content","isVisible":"no"}`,
	} {
		attrs := json.RawMessage(`{"page_sections":[` + good + `,` + bad + `]}`)
		secs := sectionsFromAttributes(attrs)
		if len(secs) == 0 || secs[0].ID != "g" || secs[0].Title != "Good" || secs[0].Content["body"] != "x" {
			t.Errorf("%s: good section lost: %+v", name, secs)
		}
	}
	secs := sectionsFromAttributes(json.RawMessage(`{"page_sections":[{"type":"content","sort_order":"2"},{"type":"content","sort_order":1.5}]}`))
	if len(secs) != 2 || secs[0].SortOrder != 2 || secs[1].SortOrder != 1 {
		t.Errorf("sort_order coercion: %+v", secs)
	}
}

func TestPageViewAttrsAndDates(t *testing.T) {
	pub := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	upd := pub.Add(48 * time.Hour)
	// A gated entity arrives with attributes already truncated by the loader;
	// the view must carry exactly that, never anything fuller.
	attrs := json.RawMessage(`{"content":"<p>teaser</p>","words":3,"page_sections":[{"id":"a","type":"hero"}]}`)

	cases := map[string]*routeResult{
		"page": {Page: &PublicPageResponse{Entity: &entity.Entity{
			ID: uuid.New(), Schema: &entity.EntitySchema{Slug: "blog"}, Attributes: attrs,
			PublishedAt: &pub, UpdatedAt: upd}}},
	}
	for name, rr := range cases {
		t.Run(name, func(t *testing.T) {
			v := pageViewFromRoute(rr, "https://x.io", "/blog/p")
			if v.SchemaSlug != "blog" {
				t.Errorf("schema = %q", v.SchemaSlug)
			}
			if v.Attrs["content"] != "<p>teaser</p>" || v.Attrs["words"] != float64(3) {
				t.Errorf("attrs = %v", v.Attrs)
			}
			if _, ok := v.Attrs["page_sections"]; ok {
				t.Error("page_sections must not be in Attrs")
			}
			if len(v.Sections) != 1 {
				t.Errorf("sections = %d", len(v.Sections))
			}
			if !v.PublishedAt.Equal(pub) || !v.UpdatedAt.Equal(upd) {
				t.Errorf("dates = %v %v", v.PublishedAt, v.UpdatedAt)
			}
			// Mutating the view must not change the stored attributes.
			v.Attrs["content"] = "changed"
			if v2 := pageViewFromRoute(rr, "https://x.io", "/blog/p"); v2.Attrs["content"] != "<p>teaser</p>" {
				t.Error("Attrs shares state with stored attributes")
			}
		})
	}

	// Unpublished / bad attributes give zero time and nil Attrs, no panic.
	rr := &routeResult{Page: &PublicPageResponse{Entity: &entity.Entity{ID: uuid.New(), Attributes: json.RawMessage(`not json`)}}}
	if v := pageViewFromRoute(rr, "", "/"); v.Attrs != nil || !v.PublishedAt.IsZero() {
		t.Errorf("got %v %v", v.Attrs, v.PublishedAt)
	}
}

func TestContentFromAttributes_InvalidDocWarnsAndRendersEmpty(t *testing.T) {
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	attrs := json.RawMessage(`{"page_doc":{"v":3,"nodes":[]},"page_sections":[{"id":"a","type":"content","content":{}}]}`)
	secs, doc := contentFromAttributes(attrs, "11111111-2222-3333-4444-555555555555")
	if doc != nil || len(secs) != 0 {
		t.Errorf("an invalid doc must render empty, not the frozen mirror: secs=%v doc=%v", secs, doc)
	}
	if !strings.Contains(logs.String(), "page_doc is invalid") || !strings.Contains(logs.String(), "11111111-2222-3333-4444-555555555555") {
		t.Errorf("no warning logged: %q", logs.String())
	}
}

func TestContentFromAttributes_CaseVariantDocKeyIgnored(t *testing.T) {
	doc := `{"v":2,"nodes":[{"id":"r","t":"container","p":{"tag":"div"}}]}`
	for _, key := range []string{"Page_Doc", "PAGE_DOC"} {
		attrs := json.RawMessage(`{"page_sections":[{"id":"a","type":"content","content":{}}],"` + key + `":` + doc + `}`)
		secs, d := contentFromAttributes(attrs, "")
		if d != nil || len(secs) != 1 || secs[0].ID != "a" {
			t.Errorf("%s rendered as the doc: secs=%v doc=%v", key, secs, d)
		}
	}
	secs, d := contentFromAttributes(json.RawMessage(`{"Page_Sections":[{"id":"a","type":"content","content":{}}]}`), "")
	if d != nil || len(secs) != 0 {
		t.Errorf("Page_Sections read as sections: %v", secs)
	}
}
