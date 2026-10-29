package server

import (
	"reflect"
	"testing"

	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/core/internal/taxonomy"
)

func termMeta() render.PageMeta {
	return render.PageMeta{Name: "Guides", Title: "Guides"}
}

func TestTermSEOMetaAppliesSavedValues(t *testing.T) {
	t.Parallel()
	m := termMeta()
	termSEOMeta(&m, &taxonomy.TermSEO{MetaTitle: " Guides for you ", MetaDescription: "All guides.", OGImage: "/img/g.png", NoIndex: true})
	want := render.PageMeta{Name: "Guides", Title: "Guides for you", TitleSet: true,
		Description: "All guides.", DescriptionSet: true, OGImage: "/img/g.png", NoIndex: true}
	if !reflect.DeepEqual(m, want) {
		t.Errorf("got %+v\nwant %+v", m, want)
	}
}

func TestTermSEOMetaNothingSavedChangesNothing(t *testing.T) {
	t.Parallel()
	for name, s := range map[string]*taxonomy.TermSEO{
		"nil":        nil,
		"zero":       {},
		"whitespace": {MetaTitle: "  ", MetaDescription: " ", CanonicalURL: " "},
	} {
		m := termMeta()
		termSEOMeta(&m, s)
		termSEOCanonical(&m, s, "https://x.test")
		if !reflect.DeepEqual(m, termMeta()) {
			t.Errorf("%s: meta changed: %+v", name, m)
		}
	}
}

func TestTermSEOCanonical(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"/guides":              "https://x.test/guides",
		"https://other.test/g": "https://other.test/g",
		"javascript:alert(1)":  "https://x.test/guides/",
		"//evil.test":          "https://x.test/guides/",
	} {
		m := termMeta()
		m.CanonicalURL = "https://x.test/guides/"
		termSEOCanonical(&m, &taxonomy.TermSEO{CanonicalURL: in}, "https://x.test")
		if m.CanonicalURL != want {
			t.Errorf("%q: canonical = %q, want %q", in, m.CanonicalURL, want)
		}
	}
}
