package render

import (
	"reflect"
	"testing"
)

func localeURLTree() *schemaNode {
	url := &schemaNode{Type: "string", Kind: "url"}
	return &schemaNode{Type: "object", Properties: map[string]*schemaNode{
		"ctaUrl":      url,
		"mapEmbedUrl": url,
		"title":       {Type: "string"},
		"ctas": {Type: "array", Items: &schemaNode{Type: "object", Properties: map[string]*schemaNode{
			"url":   url,
			"label": {Type: "string"},
		}}},
	}}
}

func localeURLContent() map[string]any {
	return map[string]any{
		"ctaUrl":      "/contact",
		"mapEmbedUrl": "/map",
		"title":       "/not-a-link",
		"ctas": []any{
			map[string]any{"url": "/pricing", "label": "/pricing"},
			map[string]any{"url": "https://x.io"},
		},
	}
}

func frView(keep bool) *PageView {
	return &PageView{
		Segment:        "fr",
		KeepLocale:     keep,
		LocaleSegments: []string{"fr", "es"},
	}
}

func TestPrepareURLKindLocalized(t *testing.T) {
	t.Parallel()
	want := map[string]any{
		"ctaUrl":      "/fr/contact",
		"mapEmbedUrl": "/map",
		"title":       "/not-a-link",
		"ctas": []any{
			map[string]any{"url": "/fr/pricing", "label": "/pricing"},
			map[string]any{"url": "https://x.io"},
		},
	}
	if got := prepareValueFor(localeURLTree(), localeURLContent(), 0, "", false, frView(true)); !reflect.DeepEqual(got, want) {
		t.Errorf("keep on:\n got %v\nwant %v", got, want)
	}
}

func TestPrepareURLKindUntouched(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		canvas bool
		page   *PageView
	}{
		"setting off": {false, frView(false)},
		"canvas":      {true, frView(true)},
		"no page":     {false, nil},
	} {
		got := prepareValueFor(localeURLTree(), localeURLContent(), 0, "", tc.canvas, tc.page)
		if !reflect.DeepEqual(got, localeURLContent()) {
			t.Errorf("%s: stored values changed: %v", name, got)
		}
	}
}
