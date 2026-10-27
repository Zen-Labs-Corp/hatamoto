package entity

import (
	"encoding/json"
	"testing"
)

func TestLockedStatus(t *testing.T) {
	for _, slug := range []string{BlockSchemaSlug, PartSchemaSlug} {
		for _, attrs := range []string{`{}`, `{"public":true}`, ``} {
			if st := lockedStatus(&EntitySchema{Slug: slug, IsBuiltin: true}, json.RawMessage(attrs)); st == nil || *st != StatusDraft {
				t.Errorf("%s %q: status = %v, want draft", slug, attrs, st)
			}
		}
	}
	if st := lockedStatus(&EntitySchema{Slug: "person", IsBuiltin: true}, json.RawMessage(`{"public":true}`)); st == nil || *st != StatusPublished {
		t.Errorf("person keeps following Public: %v", st)
	}
	if st := lockedStatus(&EntitySchema{Slug: "page"}, json.RawMessage(`{}`)); st != nil {
		t.Errorf("page is free, got %v", st)
	}
	if st := lockedStatus(&EntitySchema{Slug: BlockSchemaSlug}, json.RawMessage(`{}`)); st != nil {
		t.Errorf("a site-owned block type is free, got %v", *st)
	}
	if !IsReuseSchema(&EntitySchema{Slug: "block", IsBuiltin: true}) || IsReuseSchema(&EntitySchema{Slug: "block"}) ||
		IsReuseSchema(&EntitySchema{Slug: "page", IsBuiltin: true}) || IsReuseSchema(nil) {
		t.Error("IsReuseSchema")
	}
}

func TestDocRefsBlock(t *testing.T) {
	const id = "11111111-2222-3333-4444-555555555555"
	doc := `{"page_doc":{"v":2,"nodes":[{"id":"c","t":"container","c":[{"id":"r","t":"ref","p":{"block":"` + id + `"}}]}]}}`
	if !docRefsBlock(json.RawMessage(doc), id) {
		t.Error("ref not found")
	}
	if docRefsBlock(json.RawMessage(doc), "aaaaaaaa-2222-3333-4444-555555555555") {
		t.Error("other block matched")
	}
	text := `{"page_doc":{"v":2,"nodes":[{"id":"t","t":"text","p":{"text":"` + id + `"}}]}}`
	if docRefsBlock(json.RawMessage(text), id) {
		t.Error("uuid in text matched")
	}
	for _, bad := range []string{``, `{}`, `{"page_doc":"x"}`, `{"page_doc":{"v":9}}`, `nope`} {
		if docRefsBlock(json.RawMessage(bad), id) {
			t.Errorf("%q matched", bad)
		}
	}
}
