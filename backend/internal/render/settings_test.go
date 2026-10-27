package render

import (
	"regexp"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/builder"
)

// The settings page (anchors, links, captions, lightbox, gallery) has public
// and canvas goldens like every widget fixture; its markup is checked in detail below.
func TestSettingsGolden(t *testing.T) {
	d := loadWidgetDoc(t, "settings")
	pub := mainInner(t, renderDocView(t, widgetFixtureView("settings"), d, false))
	can := mainInner(t, renderDocView(t, widgetFixtureView("settings"), d, true))
	compareWidgetGolden(t, "settings", "public", pub)
	compareWidgetGolden(t, "settings", "canvas", can)
	if strings.Contains(pub, "data-hm-node") || strings.Contains(pub, " style=") {
		t.Error("public markup carries editor markers or inline style/handlers")
	}
}

func TestLinkAttrs(t *testing.T) {
	for _, c := range []struct {
		p    map[string]any
		want string
	}{
		{map[string]any{}, ``},
		{map[string]any{"target": "_blank"}, ` target="_blank" rel="noopener noreferrer"`},
		{map[string]any{"target": "_blank", "rel": "ugc nofollow"}, ` target="_blank" rel="nofollow ugc noopener noreferrer"`},
		{map[string]any{"rel": "sponsored ugc sponsored"}, ` rel="sponsored ugc"`},
		{map[string]any{"rel": "noopener bogus nofollow"}, ` rel="nofollow"`},
		{map[string]any{"target": "_blank", "rel": "noopener"}, ` target="_blank" rel="noopener noreferrer"`},
		{map[string]any{"target": "_self"}, ``},
	} {
		if got := linkAttrs(&builder.Node{P: c.p}); got != c.want {
			t.Errorf("%v: got %q, want %q", c.p, got, c.want)
		}
	}
}

func renderSettingsPage(t *testing.T, canvas bool) string {
	t.Helper()
	return mainInner(t, renderDocView(t, minimalView(), loadWidgetDoc(t, "settings"), canvas))
}

func TestSettingsMarkup(t *testing.T) {
	out := renderSettingsPage(t, false)
	for _, want := range []string{
		// anchors, names and tooltips
		`<section class="hm-n hm-c hm-n-hero" id="pricing" aria-label="Pricing plans" title="Our plans">`,
		// button link options (ISC-4, ISC-5)
		`href="#pricing" target="_blank" rel="nofollow sponsored noopener noreferrer">Buy</a>`,
		`aria-label="Read the docs" href="/docs" rel="nofollow ugc">Docs</a>`,
		// bare image keeps today's markup, plus id and title, never aria
		`<img class="hm-n hm-w-img hm-n-plain" id="plain-img" title="Plain tip" src="/media/a.jpg" alt="Plain" loading="lazy" decoding="async">`,
		// caption (ISC-7)
		`<figure class="hm-n hm-w-fig hm-n-cap"><img class="hm-w-img" src="/media/b.jpg" alt="Captioned" loading="lazy" decoding="async"><figcaption class="hm-w-cap">Our studio, 2026</figcaption></figure>`,
		// image link
		`<a class="hm-n hm-w-imglink hm-n-link" href="/gallery" target="_blank" rel="sponsored noopener noreferrer"><img class="hm-w-img"`,
		// link + caption: the link inside the figure carries the name
		`<figure class="hm-n hm-w-fig hm-n-linkcap"><a class="hm-w-imglink" href="/story" aria-label="Read the story"><img class="hm-w-img"`,
		// lightbox: link to the original, data-hm-lightbox
		`<a class="hm-w-imglink" href="/media/e.jpg" data-hm-lightbox><img class="hm-w-img" src="/media/e.jpg" alt="Opens large"`,
		`<a class="hm-n hm-w-imglink hm-n-lightdeco" aria-label="Enlarge the photo" href="/media/f.jpg" data-hm-lightbox>`,
		// container link (ISC-9)
		`<a class="hm-n hm-c hm-c--link hm-n-card" id="more" href="/more" target="_blank" rel="nofollow noopener noreferrer">`,
		// gallery images are lightbox links through their parent
		`<a class="hm-n hm-w-imglink hm-n-ga" href="/media/g1.jpg" data-hm-lightbox>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("markup lacks %s", want)
		}
	}
	// A linked container with interactive content falls back to a plain box.
	if strings.Contains(out, `hm-n-badcard" href`) || !strings.Contains(out, `<div class="hm-n hm-c hm-n-badcard">`) {
		t.Error("linked container with a button is not a plain div")
	}
	if strings.Contains(out, "ignored on a bare image") {
		t.Error("aria rendered on a bare image")
	}
}

func TestFeatureCSSOnlyWhenUsed(t *testing.T) {
	plain := renderDoc(t, nil, &builder.Doc{V: 2, Nodes: []*builder.Node{{ID: "i", T: "image", P: map[string]any{"src": "/a.jpg", "alt": "A"}}}}, false)
	for _, css := range []string{".hm-w-fig", ".hm-w-imglink", ".hm-c--link"} {
		if strings.Contains(plain, css) {
			t.Errorf("a plain page carries %s", css)
		}
	}
	full := renderDoc(t, nil, loadWidgetDoc(t, "settings"), false)
	for _, css := range []string{".hm-w-fig", ".hm-w-cap", ".hm-w-imglink", ".hm-c--link", ".hm-gallery :where(img)"} {
		if !strings.Contains(full, css) {
			t.Errorf("the settings page lacks %s", css)
		}
	}
	can := renderDoc(t, nil, &builder.Doc{V: 2, Nodes: []*builder.Node{{ID: "i", T: "image", P: map[string]any{"src": "/a.jpg"}}}}, true)
	if !strings.Contains(can, ".hm-c--link") || !strings.Contains(can, ".hm-w-fig") {
		t.Error("the canvas lacks the feature CSS (a patch may add the feature)")
	}
}

func TestCaptionImageCanvasMarkersOnOutermost(t *testing.T) {
	can := renderSettingsPage(t, true)
	if !strings.Contains(can, `<figure class="hm-n hm-w-fig hm-n-cap" data-hm-node="cap"`) {
		t.Errorf("canvas figure lacks the node marker:\n%s", can)
	}
	if strings.Count(can, `data-hm-node="cap"`) != 1 {
		t.Error("marker repeated on inner elements")
	}
}

// imgTag is the <img> start tag whose attributes contain marker.
func imgTag(t *testing.T, html, marker string) string {
	t.Helper()
	for _, tag := range regexp.MustCompile(`<img [^>]*>`).FindAllString(html, -1) {
		if strings.Contains(tag, marker) {
			return tag
		}
	}
	t.Fatalf("no <img> with %s", marker)
	return ""
}

// sizerData is a DataSource with media sizes and a prefetch log; the other
// DataSource methods come from the embedded nil-safe fake.
type sizerData struct {
	DataSource
	sizes    map[string][2]int
	prefetch [][]string
	asked    []string
}

func (s *sizerData) ImageSize(u string) (int, int, bool) {
	s.asked = append(s.asked, u)
	d, ok := s.sizes[u]
	return d[0], d[1], ok
}
func (s *sizerData) ImageVariants(string) []ImageVariant { return nil }
func (s *sizerData) PrefetchVariants(urls []string)      { s.prefetch = append(s.prefetch, urls) }

func TestImageWidthHeightFromMedia(t *testing.T) {
	ds := &sizerData{sizes: map[string][2]int{"/media/a.jpg": {1600, 900}, "/media/zero.jpg": {0, 900}}}
	e, err := New(Options{Data: ds})
	if err != nil {
		t.Fatal(err)
	}
	node := func(id, src string, p map[string]any) *builder.Node {
		q := map[string]any{"src": src, "alt": "A"}
		for k, v := range p {
			q[k] = v
		}
		return &builder.Node{ID: id, T: "image", P: q}
	}
	g := &builder.Node{ID: "g", T: "gallery", P: map[string]any{"lightbox": true}, C: []*builder.Node{node("gi", "/media/a.jpg", nil)}}
	d := &builder.Doc{V: 2, Nodes: []*builder.Node{
		node("known", "/media/a.jpg", nil),
		node("cap", "/media/a.jpg", map[string]any{"caption": "C"}),
		node("unknown", "/media/other.jpg", nil),
		node("zero", "/media/zero.jpg", nil),
		node("ext", "https://cdn.example.test/x.jpg", nil),
		node("data", "javascript:alert(1)", nil),
		g,
		{ID: "hid", T: "image", P: map[string]any{"src": "/media/hidden.jpg", "hidden": true}},
	}}
	v := minimalView()
	v.Doc = d
	var sb strings.Builder
	if err := e.RenderPage(&sb, v); err != nil {
		t.Fatal(err)
	}
	out := sb.String()
	if n := strings.Count(out, ` width="1600" height="900"`); n != 3 {
		t.Errorf("width/height written %d times, want 3 (known, captioned, gallery)", n)
	}
	for _, id := range []string{"unknown", "zero", "ext", "data"} {
		if tag := imgTag(t, out, "hm-n-"+id+`"`); strings.Contains(tag, "width=") {
			t.Errorf("%s has size attributes: %s", id, tag)
		}
	}
	if !strings.Contains(out, `alt="A" width="1600" height="900" loading="lazy"`) {
		t.Error("size attributes are not between alt and loading")
	}
	if len(ds.prefetch) != 1 {
		t.Fatalf("prefetch calls = %d, want one batch", len(ds.prefetch))
	}
	got := ds.prefetch[0]
	want := []string{"/media/a.jpg", "/media/a.jpg", "/media/other.jpg", "/media/zero.jpg", "https://cdn.example.test/x.jpg", "/media/a.jpg"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("prefetched %v, want the visible literal image sources %v", got, want)
	}
}

// A DataSource without ImageSizer (every existing fake) writes no size.
func TestImageWithoutSizerHasNoSize(t *testing.T) {
	d := &builder.Doc{V: 2, Nodes: []*builder.Node{{ID: "i", T: "image", P: map[string]any{"src": "/media/a.jpg", "alt": "A"}}}}
	if tag := imgTag(t, renderDoc(t, nil, d, false), `hm-n-i"`); strings.Contains(tag, "width=") {
		t.Errorf("size attributes without a sizer: %s", tag)
	}
}
