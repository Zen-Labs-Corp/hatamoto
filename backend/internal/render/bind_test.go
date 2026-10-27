package render

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/builder"
)

// bindView is a view that has a value for every vocabulary row.
func bindView() *PageView {
	v := minimalView()
	v.Site = SiteView{
		Name: "Site N", Tagline: "Tag", LogoURL: "/media/logo.png", Phone: "555", Hours: "9-5",
		Address:  Address{Street: "1 Main", City: "Town", State: "ST", PostalCode: "1234", Country: "AU"},
		Settings: map[string]any{"cta": map[string]any{"label": "Go", "n": float64(3)}, "list": []any{"a"}},
	}
	v.Page.Name = "Entity Name"
	v.Entity = &EntityView{
		Title: "Entity Title", URL: "/e/url", Excerpt: "Exc", Image: "/media/a.jpg",
		Attributes:  map[string]any{"deep": map[string]any{"x": "dx"}, "count": float64(7), "ratio": 1.5, "flag": true, "obj": map[string]any{"a": 1}},
		PublishedAt: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), UpdatedAt: time.Date(2026, 4, 2, 0, 0, 0, 0, time.UTC),
		Author: &AuthorView{Name: "Ada", URL: "/author/ada"},
	}
	v.Term = &TermView{Kind: "category", Slug: "news", Name: "News", URL: "/category/news", Description: "All news"}
	v.scope = &bindScope{item: &RelatedItem{
		Title: "Item T", Name: "Item N", URL: "/i/u", Excerpt: "Item E", Image: "/media/i.jpg", Category: "Cat",
		Author: "Bo", AuthorURL: "/author/bo", PublishedAt: time.Date(2025, 12, 25, 0, 0, 0, 0, time.UTC),
		ReadingTime: 4, Attrs: map[string]any{"price": float64(12), "meta": map[string]any{"k": "mk"}},
	}}
	return v
}

func TestResolveBindEveryRow(t *testing.T) {
	e := newTestEngine(t, Options{})
	v := bindView()
	want := map[string]string{
		"entity.name": "Entity Name", "entity.title": "Entity Title", "entity.excerpt": "Exc", "entity.image": "/media/a.jpg",
		"entity.url": "/e/url", "entity.published_at": "1 March 2026", "entity.updated_at": "2 April 2026",
		"entity.author.name": "Ada", "entity.author.url": "/author/ada",
		"entity.attributes.deep.x": "dx", "entity.attributes.count": "7", "entity.attributes.ratio": "1.5",
		"entity.attributes.flag": "true", "entity.attributes.obj": "", "entity.attributes.missing": "",
		"site.name": "Site N", "site.tagline": "Tag", "site.logo": "/media/logo.png", "site.phone": "555", "site.hours": "9-5",
		"site.address.street": "1 Main", "site.address.city": "Town", "site.address.state": "ST",
		"site.address.postal_code": "1234", "site.address.country": "AU",
		"site.settings.cta.label": "Go", "site.settings.cta.n": "3", "site.settings.list": "",
		"term.name": "News", "term.slug": "news", "term.url": "/category/news", "term.description": "All news", "term.kind": "category",
		"loop.item.title": "Item T", "loop.item.name": "Item N", "loop.item.url": "/i/u", "loop.item.excerpt": "Item E",
		"loop.item.image": "/media/i.jpg", "loop.item.category": "Cat", "loop.item.author": "Bo",
		"loop.item.author_url": "/author/bo", "loop.item.published_at": "25 December 2025", "loop.item.reading_time": "4 min read",
		"loop.item.attributes.price": "12", "loop.item.attributes.meta.k": "mk",
	}
	seen := map[string]bool{}
	for src, w := range want {
		if got, _ := e.resolveBind(v, src); got != w {
			t.Errorf("%s = %q, want %q", src, got, w)
		}
		seen[src] = true
	}
	for _, s := range builder.BindSources() { // a new vocabulary row needs a resolver and a case here
		if strings.HasSuffix(s.Source, ">") {
			continue
		}
		if !seen[s.Source] {
			t.Errorf("vocabulary row %s has no resolve test", s.Source)
		}
	}
}

func TestResolveBindEntityFallbackAndNoScope(t *testing.T) {
	e := newTestEngine(t, Options{})
	v := minimalView()
	v.Page.Name = "Draft"
	v.Attrs = map[string]any{"title": "Attr title", "excerpt": "Attr exc", "cover": "/media/c.jpg", "k": "v"}
	v.PublishedAt = time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	for src, w := range map[string]string{
		"entity.name": "Draft", "entity.title": "Attr title", "entity.excerpt": "Attr exc", "entity.image": "/media/c.jpg",
		"entity.attributes.k": "v", "entity.published_at": "2 January 2026", "entity.url": "", "entity.author.name": "",
		"loop.item.title": "", "term.name": "",
	} {
		if got, _ := e.resolveBind(v, src); got != w {
			t.Errorf("%s = %q, want %q", src, got, w)
		}
	}
}

func renderBound(t *testing.T, v *PageView, e *Engine, n *builder.Node, canvas bool) string {
	t.Helper()
	var b bytes.Buffer
	if err := e.RenderNode(&b, v, n, canvas); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestBoundEscapingMatchesLiteral(t *testing.T) {
	e := newTestEngine(t, Options{})
	v := bindView()
	v.Page.Name = `<script>alert("x")</script> & co`
	lit := renderBound(t, v, e, &builder.Node{ID: "h", T: "heading", P: map[string]any{"text": v.Page.Name}}, false)
	got := renderBound(t, v, e, &builder.Node{ID: "h", T: "heading", B: map[string]string{"text": "entity.name"}}, false)
	if got != lit || strings.Contains(got, "<script>") {
		t.Errorf("bound heading\n got %s\nwant %s", got, lit)
	}
}

func TestBoundBindingWinsOverLiteralAndDoesNotMutate(t *testing.T) {
	e := newTestEngine(t, Options{})
	v := bindView()
	n := &builder.Node{ID: "h", T: "heading", P: map[string]any{"text": "literal"}, B: map[string]string{"text": "site.name"}}
	if got := renderBound(t, v, e, n, false); !strings.Contains(got, ">Site N<") {
		t.Errorf("got %s", got)
	}
	if n.P["text"] != "literal" {
		t.Errorf("stored node mutated: %v", n.P)
	}
}

func TestBoundUnsafeURLIsEmpty(t *testing.T) {
	e := newTestEngine(t, Options{})
	v := bindView()
	v.Page.Name = "javascript:alert(1)"
	v.Site.LogoURL = "javascript:alert(1)"
	btn := renderBound(t, v, e, &builder.Node{ID: "b", T: "button", P: map[string]any{"text": "Go"}, B: map[string]string{"href": "entity.name"}}, false)
	if !strings.Contains(btn, `href="#"`) || strings.Contains(btn, "javascript") { // safeURL's answer for a refused URL, as for a literal
		t.Errorf("button: %s", btn)
	}
	img := renderBound(t, v, e, &builder.Node{ID: "i", T: "image", B: map[string]string{"src": "site.logo"}}, false)
	if !strings.Contains(img, `src="#"`) || strings.Contains(img, "javascript") {
		t.Errorf("image: %s", img)
	}
}

func TestBoundImageGetsSrcset(t *testing.T) {
	e := newTestEngine(t, Options{Data: fakeData{}})
	v := bindView()
	got := renderBound(t, v, e, &builder.Node{ID: "i", T: "image", B: map[string]string{"src": "entity.image"}}, false)
	for _, w := range []string{`src="/media/a.jpg"`, `srcset="/media/a_medium.jpg 300w, /media/a_large.jpg 1024w"`, `sizes="`} {
		if !strings.Contains(got, w) {
			t.Errorf("missing %s in %s", w, got)
		}
	}
}

func TestBoundMissingRendersEmpty(t *testing.T) {
	e := newTestEngine(t, Options{})
	v := minimalView() // no scope, no term
	for kind, n := range map[string]*builder.Node{
		"text":   {ID: "t", T: "text", P: map[string]any{"text": "stale"}, B: map[string]string{"text": "loop.item.title"}},
		"button": {ID: "b", T: "button", P: map[string]any{"text": "Go"}, B: map[string]string{"href": "term.url"}},
	} {
		got := renderBound(t, v, e, n, false)
		if strings.Contains(got, "stale") {
			t.Errorf("%s kept the literal: %s", kind, got)
		}
	}
	if got := renderBound(t, v, e, &builder.Node{ID: "t", T: "text", B: map[string]string{"text": "loop.item.title"}}, false); !strings.Contains(got, "></p>") {
		t.Errorf("want empty <p>: %s", got)
	}
	if got := renderBound(t, v, e, &builder.Node{ID: "b", T: "button", P: map[string]any{"text": "Go"}, B: map[string]string{"href": "term.url"}}, false); !strings.Contains(got, `href="#"`) {
		t.Errorf("want empty href: %s", got)
	}
}

func TestBoundCanvasPlaceholderAndNoPropMarker(t *testing.T) {
	e := newTestEngine(t, Options{})
	v := minimalView()
	v.Canvas = true
	got := renderBound(t, v, e, &builder.Node{ID: "h", T: "heading", B: map[string]string{"text": "entity.excerpt"}}, true)
	if !strings.Contains(got, "‹entity.excerpt›") || strings.Contains(got, "data-hm-prop") || !strings.Contains(got, `data-hm-node="h"`) {
		t.Errorf("placeholder: %s", got)
	}
	// a url prop gets no placeholder, a bound text shows its value
	btn := renderBound(t, v, e, &builder.Node{ID: "b", T: "button", P: map[string]any{"text": "Go"}, B: map[string]string{"href": "entity.url"}}, true)
	if !strings.Contains(btn, `href="#"`) || !strings.Contains(btn, `data-hm-prop="text"`) {
		t.Errorf("button: %s", btn)
	}
	v.Page.Name = "Shown"
	if got := renderBound(t, v, e, &builder.Node{ID: "h", T: "heading", B: map[string]string{"text": "entity.name"}}, true); !strings.Contains(got, ">Shown<") {
		t.Errorf("value: %s", got)
	}
}

// Every bindable text prop of every kind reads the bound value: a widget prep
// that read the stored props would ignore it.
func TestBoundEveryBindableKind(t *testing.T) {
	e := newTestEngine(t, Options{})
	v := bindView()
	v.Page.Name = "ZEDVALUE"
	n := 0
	for _, k := range builder.Kinds() {
		for _, p := range k.Props {
			if p.Bind != builder.BindText {
				continue
			}
			n++
			node := &builder.Node{ID: "x", T: k.Kind, B: map[string]string{p.Key: "entity.name"}}
			if k.Kind == "video" {
				node.P = map[string]any{"url": "https://www.youtube.com/watch?v=dQw4w9WgXcQ"}
			}
			got := renderBound(t, v, e, node, false)
			if !strings.Contains(got, "ZEDVALUE") {
				t.Errorf("%s.%s: bound value missing in %q", k.Kind, p.Key, got)
			}
		}
	}
	if n < 8 {
		t.Errorf("only %d bindable text props found", n)
	}
}

func TestUnboundNodeIsNotCopied(t *testing.T) {
	e := newTestEngine(t, Options{})
	n := &builder.Node{ID: "h", T: "heading", P: map[string]any{"text": "x"}}
	if e.bound(minimalView(), n) != n {
		t.Fatal("node without b was copied")
	}
	if a := testing.AllocsPerRun(100, func() { e.bound(minimalView(), n) }); a > 1 { // the view itself
		t.Errorf("allocs = %v", a)
	}
}
