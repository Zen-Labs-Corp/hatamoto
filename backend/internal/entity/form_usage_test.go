package entity

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

type fakeFormScanner struct {
	EntityRepository
	ents  []*Entity
	scans int
}

func (f *fakeFormScanner) ScanFormCandidates(_ context.Context, fn func(*Entity) bool) error {
	f.scans++
	for _, e := range f.ents {
		if !fn(e) {
			return nil
		}
	}
	return nil
}

func usageEntity(schema, name, slug string, attrs any) *Entity {
	b, _ := json.Marshal(attrs)
	return &Entity{ID: uuid.New(), Name: name, Slug: slug, Status: StatusPublished, Attributes: b, Schema: &EntitySchema{Slug: schema}}
}

func formDoc(slug string) map[string]any {
	return map[string]any{"v": 2, "nodes": []any{map[string]any{"id": "a", "t": "form", "p": map[string]any{"slug": slug}}}}
}

func TestFormPlacements(t *testing.T) {
	repo := &fakeFormScanner{ents: []*Entity{
		usageEntity("page", "Contact", "contact", map[string]any{"page_doc": formDoc("contact")}),
		usageEntity("page", "Contact us", "contact-us", map[string]any{"page_doc": formDoc("contact-us")}),
		usageEntity("page", "Legacy", "legacy", map[string]any{"page_sections": []any{
			map[string]any{"type": "contact_form", "content": map[string]any{"form_slug": "legacy-form"}}}}),
		usageEntity("page", "Mirror", "mirror", map[string]any{
			"page_doc":      formDoc("quote"),
			"page_sections": []any{map[string]any{"type": "contact_form", "content": map[string]any{"form_slug": "quote"}}}}),
		usageEntity("theme_part", "Site footer", "site-footer", map[string]any{"slot": "footer",
			"page_doc": formDoc("newsletter")}),
		usageEntity("block", "Promo", "promo", map[string]any{"page_doc": formDoc("newsletter")}),
		usageEntity("post", "News", "news", map[string]any{"body": `<p>[form slug="contact"]</p> and [format] [form slug="contact-us"x]`}),
	}}
	got, err := (&Service{entityRepo: repo}).FormPlacements(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	contact := got["contact"]
	if len(contact) != 2 || contact[0].Slug != "contact" || contact[0].Via != ViaWidget || contact[1].Slug != "news" || contact[1].Via != ViaShortcode {
		t.Fatalf("contact = %+v", contact)
	}
	if cu := got["contact-us"]; len(cu) != 1 || cu[0].Slug != "contact-us" || cu[0].Via != ViaWidget {
		t.Fatalf("contact-us confused with contact: %+v", cu)
	}
	if l := got["legacy-form"]; len(l) != 1 || l[0].Via != ViaSection {
		t.Fatalf("legacy = %+v", l)
	}
	if q := got["quote"]; len(q) != 1 || q[0].Via != ViaWidget {
		t.Fatalf("doc and frozen sections counted twice: %+v", q)
	}
	nl := got["newsletter"]
	if len(nl) != 2 {
		t.Fatalf("newsletter = %+v", nl)
	}
	for _, p := range nl {
		if p.Kind != PlacementArea || p.Slug != "" {
			t.Errorf("area keeps a path: %+v", p)
		}
	}
	if nl[0].Area != "Footer" || nl[1].Area != "Promo" {
		t.Errorf("areas = %q, %q", nl[0].Area, nl[1].Area)
	}
}

func TestFormUsageIndexCachesAndInvalidates(t *testing.T) {
	repo := &fakeFormScanner{ents: []*Entity{usageEntity("page", "A", "a", map[string]any{"page_doc": formDoc("x")})}}
	idx := NewFormUsageIndex(&Service{entityRepo: repo})
	ctx := context.Background()
	for range 3 {
		if m, err := idx.Get(ctx); err != nil || len(m["x"]) != 1 {
			t.Fatalf("get: %v %v", m, err)
		}
	}
	if repo.scans != 1 {
		t.Fatalf("scans = %d, want 1 (cached)", repo.scans)
	}
	repo.ents = nil
	idx.Invalidate()
	if m, _ := idx.Get(ctx); len(m["x"]) != 0 || repo.scans != 2 {
		t.Fatalf("after invalidate: %v scans=%d", m, repo.scans)
	}
}
