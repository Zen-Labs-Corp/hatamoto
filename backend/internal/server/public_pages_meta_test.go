package server

import (
	"strings"
	"testing"

	"hatamoto.dev/core/internal/entity"
)

// TestEntryPageMeta: the homepage and every other page share one head builder,
// so a page with only a description gets it as its meta description (#411).
func TestEntryPageMeta(t *testing.T) {
	t.Parallel()
	ent := &entity.Entity{Name: "南房総", Schema: &entity.EntitySchema{Slug: "page"}}
	got := entryPageMeta(ent, map[string]any{"description": "説明", "featured_image": "/f.jpg"}, "https://example.com/")
	want := PageMeta{Title: "南房総", Description: "説明", OGImage: "/f.jpg", CanonicalURL: "https://example.com/"}
	if got != want {
		t.Errorf("description only: got %+v, want %+v", got, want)
	}
	got = entryPageMeta(ent, map[string]any{"meta_title": "T", "meta_description": "M", "description": "D", "og_image": "/og.jpg"}, "c")
	want = PageMeta{Title: "T", Description: "M", OGImage: "/og.jpg", CanonicalURL: "c"}
	if got != want {
		t.Errorf("explicit values: got %+v, want %+v", got, want)
	}
}

func TestMetaDescriptionAndImage(t *testing.T) {
	t.Parallel()
	person := &entity.EntitySchema{Slug: "person"}
	post := &entity.EntitySchema{Slug: "post"}
	longBio := "<p>" + strings.Repeat("word ", 100) + "</p>"
	for _, tc := range []struct {
		name            string
		attrs           map[string]any
		schema          *entity.EntitySchema
		wantDesc, wantI string
	}{
		{"person photo and short bio", map[string]any{"photo": "/m/a.jpg", "short_bio": "Writes."}, person, "Writes.", "/m/a.jpg"},
		{"empty og_image does not block the photo", map[string]any{"og_image": "", "photo": "/m/a.jpg"}, person, "", "/m/a.jpg"},
		{"blank values are skipped", map[string]any{"meta_description": "  ", "description": "", "short_bio": "Short"}, person, "Short", ""},
		{"explicit values win", map[string]any{"og_image": "/og.jpg", "photo": "/m/a.jpg", "meta_description": "Meta", "short_bio": "Short"}, person, "Meta", "/og.jpg"},
		{"bio is plain text cut to 160 runes", map[string]any{"bio": longBio}, person, strings.TrimSpace(strings.Repeat("word ", 32)), ""},
		{"other schemas ignore person fields", map[string]any{"photo": "/m/a.jpg", "short_bio": "Short", "bio": "<p>x</p>"}, post, "", ""},
		{"other schemas skip blanks too", map[string]any{"meta_description": "", "excerpt": "Ex", "og_image": " ", "featured_image": "/f.jpg"}, post, "Ex", "/f.jpg"},
		{"no schema", map[string]any{"description": "D"}, nil, "D", ""},
	} {
		desc, img := metaDescriptionAndImage(tc.attrs, tc.schema)
		if tc.name == "bio is plain text cut to 160 runes" {
			if n := len([]rune(desc)); n > 160 || strings.ContainsAny(desc, "<>") || !strings.HasPrefix(desc, "word word") {
				t.Errorf("%s: description %q (%d runes)", tc.name, desc, n)
			}
			continue
		}
		if desc != tc.wantDesc || img != tc.wantI {
			t.Errorf("%s: got (%q, %q), want (%q, %q)", tc.name, desc, img, tc.wantDesc, tc.wantI)
		}
	}
}
