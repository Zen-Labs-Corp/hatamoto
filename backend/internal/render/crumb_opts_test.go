package render

import (
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
