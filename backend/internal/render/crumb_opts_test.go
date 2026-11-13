package render

import (
	"os"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/seo"
)

func TestCrumbOptsZero(t *testing.T) {
	t.Parallel()
	var nilView *PageView
	if got := nilView.CrumbOpts(); got != (CrumbOpts{}) {
		t.Errorf("nil view = %+v", got)
	}
	if got := (&PageView{}).CrumbOpts(); got != (CrumbOpts{}) {
		t.Errorf("no SEO = %+v", got)
	}
	v := &PageView{SEO: &seo.Settings{Breadcrumbs: seo.Breadcrumbs{Separator: "›", HideHome: true, HideCurrent: true}}}
	if got := v.CrumbOpts(); got != (CrumbOpts{Sep: "›", HideHome: true, HideCurrent: true}) {
		t.Errorf("set = %+v", got)
	}
}

func TestCrumbOptsLocaleRoot(t *testing.T) {
	t.Parallel()
	fr := &PageView{Segment: "fr", LocaleName: "Francais"}
	if !fr.CrumbOpts().Shaped {
		t.Error("prefixed locale without SEO settings is not Shaped")
	}
	fr.SEO = &seo.Settings{}
	if !fr.CrumbOpts().Shaped {
		t.Error("prefixed locale with SEO settings is not Shaped")
	}
	for name, v := range map[string]*PageView{
		"one locale": {},
		"unprefixed": {LocaleName: "English"},
		"shared":     {Segment: "fr", LocaleName: "Francais", Shared: true},
	} {
		if v.CrumbOpts().Shaped {
			t.Errorf("%s is Shaped", name)
		}
	}
}

func TestSingleDocLocaleRootCrumb(t *testing.T) {
	t.Parallel()
	e := newTestEngine(t, Options{Data: docData{}})
	v := hierarchyView()
	v.EntityID, v.SchemaSlug, v.Kind, v.Path = "e9", "doc", KindSingle, "/fr/docs/themes/layouts"
	v.Entity = &EntityView{ID: "e9", Title: "Layouts", Schema: "doc"}
	v.Segment, v.LocaleName = "fr", "Francais"
	v.Breadcrumb = []Crumb{{Name: "Francais", URL: "/fr"}, {Name: "Docs", URL: "/fr/docs"}, {Name: "Layouts", URL: "/fr/docs/themes/layouts"}}
	out := renderToString(t, e, v)
	if !strings.Contains(out, `<a href="/fr">Francais</a>`) {
		t.Errorf("locale root crumb missing from the visible trail:\n%s", mainInner(t, out))
	}
}

func TestSingleDocCrumbOptions(t *testing.T) {
	t.Parallel()
	e := newTestEngine(t, Options{Data: docData{}})
	build := func(b seo.Breadcrumbs) *PageView {
		v := hierarchyView()
		v.EntityID, v.SchemaSlug, v.Kind, v.Path = "e9", "doc", KindSingle, "/docs/themes/layouts"
		v.Entity = &EntityView{ID: "e9", Title: "Layouts", Schema: "doc"}
		v.SEO = &seo.Settings{Breadcrumbs: b}
		return v
	}
	base := renderToString(t, e, build(seo.Breadcrumbs{}))
	if strings.Contains(base, "data-sep") || !strings.Contains(base, `aria-current="page"`) {
		t.Errorf("default trail changed:\n%s", mainInner(t, base))
	}
	out := renderToString(t, e, build(seo.Breadcrumbs{Separator: "›"}))
	if !strings.Contains(out, `<li data-sep="›">`) {
		t.Errorf("separator missing:\n%s", mainInner(t, out))
	}
	out = renderToString(t, e, build(seo.Breadcrumbs{HideCurrent: true}))
	if strings.Contains(out, `<span aria-current="page">Layouts</span>`) {
		t.Errorf("current crumb still shown:\n%s", mainInner(t, out))
	}
}

func TestBreadcrumbWidgetOptions(t *testing.T) {
	t.Parallel()
	trail := []Crumb{{Name: "Home", URL: "/"}, {Name: "Blog", URL: "/blog"}, {Name: "Post", URL: "/blog/p"}}
	render := func(b seo.Breadcrumbs, c []Crumb) string {
		v := minimalView()
		v.Doc = dataDoc(t, "breadcrumbs")
		v.Breadcrumb = c
		v.SEO = &seo.Settings{Breadcrumbs: b}
		return renderWithData(t, nil, v, false)
	}
	if out := render(seo.Breadcrumbs{}, trail); !strings.Contains(out, ">Home<") || !strings.Contains(out, ">Post<") || strings.Contains(out, "data-sep") {
		t.Errorf("default: %s", out)
	}
	out := render(seo.Breadcrumbs{HideHome: true, Separator: "»"}, trail)
	if strings.Contains(out, ">Home<") || !strings.Contains(out, `<li data-sep="»"><span aria-current="page">Post`) {
		t.Errorf("home hidden: %s", out)
	}
	out = render(seo.Breadcrumbs{HideHome: true, HideCurrent: true}, trail)
	if strings.Contains(out, ">Home<") || strings.Contains(out, ">Post<") || !strings.Contains(out, `<a href="/blog">Blog</a>`) {
		t.Errorf("both hidden: %s", out)
	}
	if out := render(seo.Breadcrumbs{HideHome: true, HideCurrent: true}, trail[:1]); !strings.Contains(out, ">Home<") {
		t.Errorf("a one-crumb trail must survive: %s", out)
	}
}

// A custom separator must not leak onto the first printed crumb: a doc whose
// schema has no archive path starts its trail at a data-sep item.
func TestDocCrumbSeparatorSkipsFirstItem(t *testing.T) {
	t.Parallel()
	css, err := os.ReadFile("themes/default/assets/css/docs.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(css), "\n") {
		if strings.Contains(line, "li[data-sep]") && !strings.Contains(line, "li + li[data-sep]") {
			t.Errorf("separator rule matches the first crumb: %s", line)
		}
	}
}

// A saved home label, primary term or breadcrumb title must show in the
// visible doc trail without losing the sidebar parents: Start > Docs >
// [term] > Themes > Layouts.
func TestSingleDocShapedTrail(t *testing.T) {
	t.Parallel()
	e := newTestEngine(t, Options{Data: docData{}})
	build := func(b seo.Breadcrumbs, attrs map[string]any, term bool) *PageView {
		v := hierarchyView()
		v.EntityID, v.SchemaSlug, v.Kind, v.Path = "e9", "doc", KindSingle, "/docs/themes/layouts"
		v.Entity = &EntityView{ID: "e9", Title: "Layouts", Schema: "doc"}
		v.Breadcrumb = []Crumb{{Name: "Start", URL: "/"}, {Name: "Docs", URL: "/docs"}}
		if term {
			v.Breadcrumb = append(v.Breadcrumb, Crumb{Name: "Guides", URL: "/term/guides"})
		}
		v.Breadcrumb = append(v.Breadcrumb, Crumb{Name: "Layouts", URL: "/docs/themes/layouts"})
		v.Attrs = attrs
		v.SEO = &seo.Settings{Breadcrumbs: b}
		return v
	}
	// items lists the crumbs of the doc trail in order; a link is "text>href".
	items := func(t *testing.T, out string) string {
		t.Helper()
		_, rest, ok := strings.Cut(out, `aria-label="Breadcrumb"><ol>`)
		list, _, ok2 := strings.Cut(rest, "</ol>")
		if !ok || !ok2 {
			t.Fatalf("no trail:\n%s", out)
		}
		var got []string
		for _, li := range strings.Split(list, "<li")[1:] {
			if i := strings.Index(li, `href="`); i >= 0 {
				href, _, _ := strings.Cut(li[i+6:], `"`)
				_, txt, _ := strings.Cut(li[i:], "\">")
				txt, _, _ = strings.Cut(txt, "<")
				got = append(got, txt+">"+href)
				continue
			}
			_, txt, _ := strings.Cut(li, "page\">")
			txt, _, _ = strings.Cut(txt, "<")
			got = append(got, txt)
		}
		return strings.Join(got, " | ")
	}
	const base = "Docs>/docs | Themes>/docs/themes | "
	for name, tc := range map[string]struct {
		v    *PageView
		want string
	}{
		"home label": {build(seo.Breadcrumbs{HomeLabel: "Start"}, nil, false), "Start>/ | " + base + "Layouts"},
		"show term":  {build(seo.Breadcrumbs{ShowTerm: true}, nil, true), "Start>/ | Docs>/docs | Guides>/term/guides | Themes>/docs/themes | Layouts"},
		"title":      {build(seo.Breadcrumbs{}, map[string]any{"breadcrumb_title": "Layouts II"}, false), "Start>/ | " + base + "Layouts II"},
		"hide home":  {build(seo.Breadcrumbs{HomeLabel: "Start", HideHome: true}, nil, false), base + "Layouts"},
		"hide cur":   {build(seo.Breadcrumbs{HomeLabel: "Start", HideCurrent: true}, nil, false), "Start>/ | Docs>/docs | Themes>/docs/themes"},
	} {
		if got := items(t, renderToString(t, e, tc.v)); got != tc.want {
			t.Errorf("%s:\n got %s\nwant %s", name, got, tc.want)
		}
	}
	out := renderToString(t, e, build(seo.Breadcrumbs{HomeLabel: "Start", Separator: "›"}, nil, false))
	if !strings.Contains(out, `<li data-sep="›"><a href="/docs/themes">Themes</a></li>`) || strings.Contains(out, `<li data-sep="›"><a href="/">`) {
		t.Errorf("separator wrong:\n%s", mainInner(t, out))
	}
}
