package render

import (
	"bytes"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/builder"
)

// variantData serves a fixed variant list for every URL.
type variantData struct {
	fakeData
	vs []ImageVariant
}

func (d variantData) ImageVariants(string) []ImageVariant { return d.vs }

var (
	jpgs = []ImageVariant{
		{URL: "/media/a_medium.jpg", Width: 300},
		{URL: "/media/a_large.jpg", Width: 1024},
		{URL: "/media/a.jpg", Width: 3000},
	}
	webps = []ImageVariant{
		{URL: "/media/a_medium.webp", Width: 300, Type: "image/webp"},
		{URL: "/media/a_large.webp", Width: 1024, Type: "image/webp"},
	}
	avifs = []ImageVariant{
		{URL: "/media/a_medium.avif", Width: 300, Type: "image/avif"},
		{URL: "/media/a_large.avif", Width: 1024, Type: "image/avif"},
	}
)

func renderPicture(t *testing.T, ds DataSource, d *builder.Doc, canvas bool) string {
	t.Helper()
	e, err := New(Options{Data: ds})
	if err != nil {
		t.Fatal(err)
	}
	v := minimalView()
	v.Doc, v.Canvas = d, canvas
	var buf bytes.Buffer
	if err := e.RenderPage(&buf, v); err != nil {
		t.Fatal(err)
	}
	return mainInner(t, buf.String())
}

func picImageDoc(p map[string]any) *builder.Doc {
	p["src"] = "/media/a.jpg"
	if p["alt"] == nil {
		p["alt"] = "A"
	}
	return &builder.Doc{V: 2, Nodes: []*builder.Node{{ID: "i", T: "image", P: p}}}
}

func concatVariants(vs ...[]ImageVariant) []ImageVariant {
	var out []ImageVariant
	for _, v := range vs {
		out = append(out, v...)
	}
	return out
}

func TestPictureWithAVIFAndWebP(t *testing.T) {
	out := renderPicture(t, variantData{vs: concatVariants(jpgs, webps, avifs)}, picImageDoc(map[string]any{}), false)
	avif := `<source type="image/avif" srcset="/media/a_medium.avif 300w, /media/a_large.avif 1024w" sizes="(max-width:1024px) 100vw, 1024px">`
	webp := `<source type="image/webp" srcset="/media/a_medium.webp 300w, /media/a_large.webp 1024w" sizes="(max-width:1024px) 100vw, 1024px">`
	if !strings.Contains(out, "<picture>"+avif+webp+"<img ") || !strings.HasSuffix(strings.TrimSpace(out), "</picture>") {
		t.Fatalf("picture markup wrong: %s", out)
	}
	if !strings.Contains(out, "hm-w-img") || !strings.Contains(out, `alt="A"`) ||
		!strings.Contains(out, `loading="lazy" decoding="async"`) ||
		!strings.Contains(out, `srcset="/media/a_medium.jpg 300w, /media/a_large.jpg 1024w, /media/a.jpg 3000w"`) {
		t.Errorf("img lost attributes: %s", out)
	}
}

func TestPictureWebPOnly(t *testing.T) {
	out := renderPicture(t, variantData{vs: concatVariants(jpgs, webps)}, picImageDoc(map[string]any{}), false)
	if strings.Contains(out, "image/avif") || !strings.Contains(out, `<source type="image/webp"`) {
		t.Errorf("webp only: %s", out)
	}
}

func TestNoModernCopiesRendersPlainImg(t *testing.T) {
	out := renderPicture(t, variantData{vs: jpgs}, picImageDoc(map[string]any{}), false)
	if strings.Contains(out, "<picture") || strings.Contains(out, "<source") || !strings.HasPrefix(out, "<img ") {
		t.Errorf("expected the plain img: %s", out)
	}
}

func TestModernSourceNarrowerThanFallbackIsLeftOut(t *testing.T) {
	// Only the 300 px copy is smaller as WebP; the large one stayed JPEG.
	out := renderPicture(t, variantData{vs: concatVariants(jpgs, webps[:1])}, picImageDoc(map[string]any{}), false)
	if strings.Contains(out, "<source") {
		t.Errorf("a source that tops out at 300w must not shadow the 1024w fallback: %s", out)
	}
}

func TestPictureSmallSourceUsesFullCopy(t *testing.T) {
	vs := []ImageVariant{
		{URL: "/media/a_medium.jpg", Width: 300}, {URL: "/media/a.jpg", Width: 800},
		{URL: "/media/a_medium.webp", Width: 300, Type: "image/webp"}, {URL: "/media/a_full.webp", Width: 800, Type: "image/webp"},
	}
	out := renderPicture(t, variantData{vs: vs}, picImageDoc(map[string]any{}), false)
	if !strings.Contains(out, `srcset="/media/a_medium.webp 300w, /media/a_full.webp 800w"`) {
		t.Errorf("full copy missing: %s", out)
	}
}

func TestPictureCommaInURLIsEscaped(t *testing.T) {
	vs := []ImageVariant{
		{URL: "/media/a,b_large.jpg", Width: 1024},
		{URL: "/media/a,b_large.webp", Width: 1024, Type: "image/webp"},
	}
	out := renderPicture(t, variantData{vs: vs}, picImageDoc(map[string]any{}), false)
	if !strings.Contains(out, `<source type="image/webp" srcset="/media/a%2Cb_large.webp 1024w"`) {
		t.Errorf("comma not escaped: %s", out)
	}
}

func TestPictureInsideFigureAndLink(t *testing.T) {
	ds := variantData{vs: concatVariants(jpgs, webps)}
	out := renderPicture(t, ds, picImageDoc(map[string]any{"caption": "Cap", "href": "/go"}), false)
	i := strings.Index(out, "<figure ")
	if i < 0 || !strings.Contains(out, `<a class="hm-w-imglink" href="/go"><picture><source`) ||
		!strings.Contains(out, "</picture></a><figcaption") {
		t.Errorf("figure+link: %s", out)
	}
	out = renderPicture(t, ds, picImageDoc(map[string]any{"href": "/go"}), false)
	if !strings.HasPrefix(out, "<a ") || !strings.Contains(out, "><picture><source") || !strings.HasSuffix(strings.TrimSpace(out), "</picture></a>") {
		t.Errorf("link only: %s", out)
	}
}

func TestPictureCanvasKeepsMarkersOnImg(t *testing.T) {
	out := renderPicture(t, variantData{vs: concatVariants(jpgs, webps)}, picImageDoc(map[string]any{}), true)
	i := strings.Index(out, "<img ")
	if !strings.HasPrefix(out, "<picture>") || i < 0 || !strings.Contains(out[i:strings.Index(out[i:], ">")+i], "data-") {
		t.Errorf("canvas markers must stay on the img: %s", out)
	}
}

func TestSrcsetFuncIgnoresModernCopies(t *testing.T) {
	got := srcset(variantData{vs: concatVariants(jpgs, webps, avifs)}, "/media/a.jpg")
	if got != "/media/a_medium.jpg 300w, /media/a_large.jpg 1024w, /media/a.jpg 3000w" {
		t.Errorf("srcset = %q", got)
	}
	if s := pictureSources(variantData{vs: jpgs}, "/media/a.jpg", "100vw"); s != "" {
		t.Errorf("no modern copies, got %q", s)
	}
}

func TestDefaultThemeBlogPartialUsesPicture(t *testing.T) {
	plain := newTestEngine(t, Options{Data: variantData{vs: jpgs}})
	withModern := newTestEngine(t, Options{Data: variantData{vs: concatVariants(jpgs, webps)}})
	single := func() *PageView {
		v := hierarchyView()
		v.EntityID, v.SchemaSlug, v.Kind = "e1", "post", KindSingle
		v.Entity = &EntityView{ID: "e1", Title: "Hello", Slug: "hello", Schema: "post", URL: "/blog/hello", Image: "/media/a.jpg"}
		v.Attrs = map[string]any{"content": "<p>x</p>"}
		return v
	}
	archive := func() *PageView {
		v := hierarchyView()
		v.Kind, v.SchemaSlug, v.Page.Name = KindArchive, "post", "Blog"
		v.Pagination, v.Items = NewPagination("/blog", nil, 2, 2, 5), fakeItems
		return v
	}
	for name, build := range map[string]func() *PageView{"article": single, "card": archive} {
		got := mainInner(t, renderToString(t, withModern, build()))
		if !strings.Contains(got, `<picture><source type="image/webp"`) || strings.Count(got, "<picture>") != strings.Count(got, "</picture>") {
			t.Errorf("%s: no balanced picture: %s", name, got)
		}
		if !strings.Contains(got, `srcset="/media/a_medium.jpg 300w, /media/a_large.jpg 1024w`) {
			t.Errorf("%s: fallback srcset changed", name)
		}
		if got := mainInner(t, renderToString(t, plain, build())); strings.Contains(got, "<picture") || !strings.Contains(got, "srcset=") {
			t.Errorf("%s without modern copies must be today's markup: %s", name, got)
		}
	}
}
