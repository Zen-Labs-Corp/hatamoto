package render

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/builder"
)

var lightboxScript = regexp.MustCompile(`<script type="module" src="/theme/assets/builder/lightbox\.([0-9a-f]{8})\.js"></script>`)

func imageDoc(p map[string]any) *builder.Doc {
	q := map[string]any{"src": "/media/a.jpg", "alt": "A"}
	for k, v := range p {
		q[k] = v
	}
	return &builder.Doc{V: 2, Nodes: []*builder.Node{{ID: "i", T: "image", P: q}}}
}

// The island ships only with a lightbox on the page (own or through a gallery).
func TestLightboxIslandOnlyWhenUsed(t *testing.T) {
	gallery := &builder.Doc{V: 2, Nodes: []*builder.Node{{ID: "g", T: "gallery", P: map[string]any{"lightbox": true}, C: imageDoc(nil).Nodes}}}
	hidden := imageDoc(map[string]any{"lightbox": true, "hidden": true})
	for name, c := range map[string]struct {
		doc  *builder.Doc
		want int
	}{
		"plain image":      {imageDoc(nil), 0},
		"captioned image":  {imageDoc(map[string]any{"caption": "c"}), 0},
		"linked image":     {imageDoc(map[string]any{"href": "/x"}), 0},
		"lightbox image":   {imageDoc(map[string]any{"lightbox": true}), 1},
		"lightbox gallery": {gallery, 1},
		"hidden lightbox":  {hidden, 0},
		"settings page":    {loadWidgetDoc(t, "settings"), 1},
	} {
		out := renderDoc(t, nil, c.doc, false)
		if n := len(lightboxScript.FindAllString(out, -1)); n != c.want {
			t.Errorf("%s: %d lightbox scripts, want %d", name, n, c.want)
		}
		if strings.Contains(out, ".hm-lb{") != (c.want == 1) {
			t.Errorf("%s: dialog CSS present = %v, want %v", name, !(c.want == 1), c.want == 1)
		}
	}
	if out := renderDoc(t, nil, loadWidgetDoc(t, "settings"), true); strings.Contains(out, "lightbox.") || strings.Contains(out, ".hm-lb{") {
		t.Error("the canvas loads the lightbox")
	}
	// The all-widgets page has no lightbox: still zero scripts beyond tabs and video.
	if all := renderDoc(t, jsLessLayers(), loadWidgetDoc(t, "all"), false); lightboxScript.MatchString(all) {
		t.Error("the all-widgets page ships the lightbox")
	}
}

func TestLightboxIslandServedHashed(t *testing.T) {
	out := renderDoc(t, nil, imageDoc(map[string]any{"lightbox": true}), false)
	m := lightboxScript.FindStringSubmatch(out)
	if m == nil {
		t.Fatal("no lightbox script")
	}
	e, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	h := e.AssetHandler()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, AssetPrefix+IslandPrefix+"lightbox."+m[1]+".js", nil))
	if rec.Code != 200 || !bytes.Equal(rec.Body.Bytes(), lightboxJS) {
		t.Fatalf("status %d", rec.Code)
	}
	if !strings.Contains(rec.Header().Get("Cache-Control"), "immutable") || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/javascript") {
		t.Errorf("headers: %v", rec.Header())
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, AssetPrefix+IslandPrefix+"lightbox.00000000.js", nil))
	if rec.Code != 404 {
		t.Errorf("wrong hash: status %d", rec.Code)
	}
}

// The island has no inline-style, eval or network use: it builds a dialog styled by class only.
func TestLightboxIslandSource(t *testing.T) {
	js := string(lightboxJS)
	if len(js) > 4096 {
		t.Errorf("lightbox.js is %d bytes, keep it under 4096", len(js))
	}
	for _, banned := range []string{"eval(", "innerHTML", "insertAdjacentHTML", "document.write", ".style", "setAttribute(\"style\"", "fetch(", "XMLHttpRequest", "import("} {
		if strings.Contains(js, banned) {
			t.Errorf("lightbox.js uses %s", banned)
		}
	}
	for _, want := range []string{"showModal()", "URL_OK", "aria-live", "aria-describedby", "hm-lb__sr", "metaKey", "isConnected"} {
		if !strings.Contains(js, want) {
			t.Errorf("lightbox.js lacks %s", want)
		}
	}
}

// The dialog paints its own dark background: contrast tools (and the QA axe run) cannot see ::backdrop, so white text on
// a transparent dialog over a light page reads as white on white.
func TestLightboxDialogHasItsOwnBackground(t *testing.T) {
	if !strings.Contains(lightboxCSS, "background:rgba(0,0,0,.85);color:#fff}") {
		t.Errorf("the .hm-lb rule has no dark background of its own: %s", lightboxCSS)
	}
	if strings.Contains(lightboxCSS, "::backdrop{background:rgba") {
		t.Error("the darkening moved back to ::backdrop, which contrast checks cannot see")
	}
}

// A wrapped image keeps its picture styles on the <img> (the wrapper carries
// the node class and the box styles), and a lightbox gallery without captions
// still fills its column.
func TestWrappedImageStylesReachTheImg(t *testing.T) {
	img := &builder.Node{ID: "im", T: "image", P: map[string]any{"src": "/media/a.jpg", "alt": "A"},
		S: map[string]map[string]string{"base": {"aspect": "1/1", "fit": "cover", "radius": "md", "mt": "4"}}}
	gallery := &builder.Doc{V: 2, Nodes: []*builder.Node{{ID: "ga", T: "gallery", P: map[string]any{"lightbox": true}, C: []*builder.Node{img}}}}
	out := renderDoc(t, nil, gallery, false)
	for _, want := range []string{
		`.hm-n-im img,img.hm-n-im{aspect-ratio:1 / 1;`,    // picture keys on the img (either shape)
		`.hm-gallery :where(img){width:100%;height:auto}`, // fill rule without any caption
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	if strings.Contains(out, "}.hm-n-im{aspect-ratio") || strings.Contains(out, "}.hm-n-im{object-fit") {
		t.Errorf("picture keys leaked onto the wrapper:\n%s", out)
	}
	// A bare image carries the node class itself: the same rule reaches it.
	plain := renderDoc(t, nil, &builder.Doc{V: 2, Nodes: []*builder.Node{{ID: "im", T: "image", P: img.P, S: img.S}}}, false)
	if !strings.Contains(plain, "img.hm-n-im{aspect-ratio") || !strings.Contains(plain, `<img class="hm-n hm-w-img hm-n-im"`) {
		t.Errorf("bare image: picture rule does not reach it:\n%s", plain)
	}
}

// A data: source cannot be opened by the island: no link, no dead "#".
func TestLightboxDataSrcRendersNoLink(t *testing.T) {
	out := renderDoc(t, nil, imageDoc(map[string]any{"src": "data:image/png;base64,AA", "lightbox": true}), false)
	if strings.Contains(out, "data-hm-lightbox") || strings.Contains(out, `href="#"`) || lightboxScript.MatchString(out) {
		t.Errorf("data: lightbox rendered a link or the island:\n%s", out)
	}
}

// A lightbox on an image whose src is bound still loads the island.
func TestLightboxIslandForBoundSrc(t *testing.T) {
	doc := &builder.Doc{V: 2, Nodes: []*builder.Node{{ID: "i", T: "image", P: map[string]any{"lightbox": true}, B: map[string]string{"src": "site.logo"}}}}
	out := renderDoc(t, nil, doc, false)
	if !lightboxScript.MatchString(out) || !strings.Contains(out, ".hm-lb{") {
		t.Errorf("bound-src lightbox has no island:\n%s", out)
	}
}

// The page behind the open dialog does not scroll, and the dialog's text for screen readers has a rule that hides it visually only.
func TestLightboxLocksScrollAndHasAnAnnouncement(t *testing.T) {
	for _, want := range []string{"html:has(dialog.hm-lb[open]){overflow:hidden}", ".hm-lb__sr{position:absolute"} {
		if !strings.Contains(lightboxCSS, want) {
			t.Errorf("lightboxCSS lacks %s", want)
		}
	}
}

// Whether an image renders wrapped depends on what its caption binding
// resolves to; the picture rules must reach the <img> either way.
func TestBoundCaptionPictureStylesBothShapes(t *testing.T) {
	doc := func() *builder.Doc {
		d := imageDoc(nil)
		d.Nodes[0].B = map[string]string{"caption": "site.tagline"}
		d.Nodes[0].S = map[string]map[string]string{"base": {"aspect": "1/1", "fit": "cover"}}
		return d
	}
	for name, c := range map[string]struct{ tagline, markup string }{
		"empty caption":   {"", `<img class="hm-n hm-w-img hm-n-i"`},
		"present caption": {"Tag", `<figure class="hm-n hm-w-fig hm-n-i"`},
	} {
		v := minimalView()
		v.Site.Tagline = c.tagline
		out := renderDocView(t, v, doc(), false)
		for _, want := range []string{c.markup, `.hm-n-i img,img.hm-n-i{aspect-ratio:1 / 1;object-fit:cover;}`} {
			if !strings.Contains(out, want) {
				t.Errorf("%s: missing %q in\n%s", name, want, out)
			}
		}
	}
}
