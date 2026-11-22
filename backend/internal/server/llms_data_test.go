package server

import (
	"testing"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/jsonx"
)

func TestFallbackDescriptionBuilderDocIsPlain(t *testing.T) {
	var attrs map[string]any
	doc := `{"page_doc":{"v":2,"nodes":[
		{"id":"a","t":"heading","p":{"text":"Work","level":2}},
		{"id":"b","t":"text","p":{"html":"<p>Three <strong>shops</strong> and a <a href=\"https://acme.example/p\">portfolio</a> in snake_case.</p>"}}]}}`
	if err := jsonx.Unmarshal([]byte(doc), &attrs, jsonx.StoredLoose); err != nil {
		t.Fatal(err)
	}
	const want = "Three shops and a portfolio in snake_case."
	if got := fallbackDescription(attrs, nil); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestLLMsLocaleResolverSingleLocale(t *testing.T) {
	// No locales configured: the default tag is empty, entries carry "en".
	resolve := llmsLocaleResolver(&entity.LocaleSet{})
	for _, loc := range []string{"en", "", entity.LocaleShared, "fr"} {
		if got := resolve(&entity.SitemapEntry{Locale: loc}); got != "" {
			t.Errorf("single-locale entry %q resolves to %q, want the default bucket", loc, got)
		}
	}
}

func TestLLMsLocaleResolverMulti(t *testing.T) {
	set, err := entity.NewLocaleSet("en", []entity.LocaleEntry{{Tag: "en"}, {Tag: "fr", Segment: "fr"}}, false, "")
	if err != nil {
		t.Fatal(err)
	}
	resolve := llmsLocaleResolver(set)
	for loc, want := range map[string]string{"en": "en", "fr": "fr", "": "en", entity.LocaleShared: "en"} {
		if got := resolve(&entity.SitemapEntry{Locale: loc}); got != want {
			t.Errorf("entry %q resolves to %q, want %q", loc, got, want)
		}
	}
}
