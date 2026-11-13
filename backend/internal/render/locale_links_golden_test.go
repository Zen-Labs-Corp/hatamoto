package render

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/jsonx"
)

// localeLinksView is a page of the prefixed locale fr on a site with en, fr
// and es. The fixture holds the hero sections whose url-kind fields the
// site setting rewrites.
func localeLinksView(t *testing.T, keep bool) *PageView {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "locale-links", "hero.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fx struct {
		Sections []sectionFixture `json:"sections"`
	}
	if err := jsonx.Unmarshal(raw, &fx); err != nil {
		t.Fatal(err)
	}
	v := minimalView()
	v.Locale, v.Segment, v.LocaleName, v.KeepLocale = "fr", "fr", "Francais", keep
	v.LocaleShared = func(p string) bool { return strings.HasPrefix(p, "/media") }
	v.Locales = []LocaleSummary{{Tag: "en", Default: true}, {Tag: "fr", Segment: "fr"}, {Tag: "es", Segment: "es"}}
	v.Alternates = []Alternate{
		{Locale: "en", Lang: "en", Name: "English", Href: "https://example.test/", Default: true},
		{Locale: "fr", Lang: "fr", Name: "Francais", Href: "https://example.test/fr", Current: true},
		{Locale: "es", Lang: "es", Name: "Spanish", Href: "https://example.test/es"},
	}
	for _, sf := range fx.Sections {
		v.Sections = append(v.Sections, Section{ID: sf.ID, Type: sf.Type, Content: sf.Content})
	}
	return v
}

func renderMain(t *testing.T, e *Engine, v *PageView) string {
	t.Helper()
	var buf bytes.Buffer
	if err := e.RenderPage(&buf, v); err != nil {
		t.Fatal(err)
	}
	return mainInner(t, buf.String())
}

func TestGoldenLocaleLinksHero(t *testing.T) {
	e, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	on := renderMain(t, e, localeLinksView(t, true))
	compareGolden(t, "locale-links-hero-on", on)
	for _, want := range []string{`href="/fr/contact"`, `href="/fr/pricing"`, `href="https://example.org/source"`, `href="/fr/docs"`, `href="/media/guide.pdf"`, `href="#top"`} {
		if !strings.Contains(on, want) {
			t.Errorf("setting on: missing %s", want)
		}
	}
	if strings.Contains(on, "/fr/fr/") || strings.Contains(on, "/fr/media") {
		t.Errorf("setting on: double prefix or shared path prefixed:\n%s", on)
	}

	off := renderMain(t, e, localeLinksView(t, false))
	compareGolden(t, "locale-links-hero-off", off)
	// Off is byte-identical to the render without any locale.
	plain := localeLinksView(t, false)
	plain.Locale, plain.Segment, plain.LocaleName, plain.Locales, plain.Alternates = "", "", "", nil, nil
	if got := renderMain(t, e, plain); got != off {
		t.Errorf("setting off differs from the one-locale render:\n--- locale ---\n%s\n--- plain ---\n%s", off, got)
	}
}

func TestGoldenLocaleLinksBreadcrumbLD(t *testing.T) {
	v := localeLinksView(t, false)
	v.Breadcrumb = []Crumb{
		{Name: "Francais", URL: "/fr"},
		{Name: "Guides", URL: "/fr/guides"},
		{Name: "Local SEO", URL: "/fr/guides/local-seo"},
	}
	var ld string
	for _, s := range seoHead(v, false).Scripts {
		if strings.Contains(string(s), `"BreadcrumbList"`) {
			ld = string(s)
		}
	}
	if ld == "" {
		t.Fatal("no BreadcrumbList emitted")
	}
	ld = strings.TrimSuffix(strings.TrimPrefix(ld, `<script type="application/ld+json">`), "</script>") + "\n"
	path := filepath.Join("testdata", "golden", "locale-links-breadcrumb-ld.json")
	if *update {
		if err := os.WriteFile(path, []byte(ld), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update to create)", err)
	}
	if ld != string(want) {
		t.Fatalf("golden mismatch (run with -update)\n--- got ---\n%s\n--- want ---\n%s", ld, want)
	}
	if strings.Contains(ld, `"Home"`) {
		t.Error("the trail must not contain a separate Home crumb")
	}
}
