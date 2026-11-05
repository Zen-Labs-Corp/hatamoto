package render

import (
	"io/fs"
	"os"
	"strings"
	"testing"
)

var (
	hashA = strings.Repeat("a", 64)
	hashB = strings.Repeat("b", 64)
	hashC = strings.Repeat("c", 64)
)

func TestFontHeadGolden(t *testing.T) {
	t.Parallel()
	faces := []FontFace{
		{Family: "Brand Sans", Weight: 700, Style: "normal", Hash: hashB, UnicodeRange: "U+0000-00FF, U+0131"},
		{Family: "Brand Sans", Weight: 400, Style: "normal", Hash: hashA},
		{Family: "Display", Weight: 400, Style: "italic", Hash: hashC},
	}
	got := string(fontHead(faces, hashA))
	if string(fontHead([]FontFace{faces[2], faces[1], faces[0]}, hashA)) != got {
		t.Error("output depends on input order")
	}
	compareGolden(t, "fonts_head", got+"\n")
}

func TestFontHeadPreload(t *testing.T) {
	t.Parallel()
	faces := []FontFace{
		{Family: "Brand Sans", Weight: 400, Style: "normal", Hash: hashA},
		{Family: "Display", Weight: 400, Style: "normal", Hash: hashB},
	}
	if out := string(fontHead(faces, hashA)); strings.Count(out, `rel="preload"`) != 1 || !strings.Contains(out, `crossorigin>`) {
		t.Errorf("body font preload: %s", out)
	}
	for name, preload := range map[string]string{"empty": "", "unknown hash": hashC, "malformed": `"><script>`} {
		if out := string(fontHead(faces, preload)); strings.Contains(out, "preload") {
			t.Errorf("%s: preload emitted: %s", name, out)
		}
	}
	if out := string(fontHead(faces, hashB)); !strings.Contains(out, `/fonts/`+hashB+`.woff2" as="font"`) {
		t.Errorf("preload names the wrong file: %s", out)
	}
}

func TestFontHeadDropsHostileFaces(t *testing.T) {
	t.Parallel()
	good := FontFace{Family: "Good", Weight: 400, Style: "normal", Hash: hashA}
	bad := []FontFace{
		{Family: `x"}</style><script>alert(1)</script>`, Weight: 400, Style: "normal", Hash: hashB},
		{Family: "Ok", Weight: 400, Style: "normal", Hash: hashB, UnicodeRange: "U+0000;}</style><script>"},
		{Family: "Ok", Weight: 400, Style: "normal", Hash: "../../etc/passwd"},
		{Family: "Ok", Weight: 450, Style: "normal", Hash: hashB},
		{Family: "Ok", Weight: 400, Style: "normal; x:y", Hash: hashB},
	}
	out := string(fontHead(append(bad, good), hashB))
	if strings.Contains(out, "script") || strings.Contains(out, "Ok") || strings.Contains(out, "passwd") || strings.Contains(out, "preload") {
		t.Errorf("hostile face survived: %s", out)
	}
	if !strings.Contains(out, `font-family:"Good"`) {
		t.Errorf("good face lost: %s", out)
	}
	if got := fontHead(bad, hashB); got != "" {
		t.Errorf("only invalid faces gave markup: %s", got)
	}
}

func TestFontHeadEmpty(t *testing.T) {
	t.Parallel()
	if got := fontHead(nil, hashA); got != "" {
		t.Errorf("no faces gave %q", got)
	}
}

// A page with fonts differs from the same page without by the head markup
// only; a page without fonts is the baseline, byte for byte.
func TestRenderFontsInHead(t *testing.T) {
	t.Parallel()
	e := newTestEngine(t, Options{})
	plain := renderToString(t, e, minimalView())
	if strings.Contains(plain, "@font-face") || strings.Contains(plain, "/fonts/") || strings.Contains(plain, "googleapis") || strings.Contains(plain, "gstatic") {
		t.Fatal("a page without installed fonts carries font markup")
	}
	v := minimalView()
	v.Site.Fonts = []FontFace{{Family: "Brand Sans", Weight: 400, Style: "normal", Hash: hashA}}
	v.Site.FontPreload = hashA
	out := renderToString(t, e, v)
	markup := string(fontHead(v.Site.Fonts, hashA))
	if strings.Replace(out, markup, "", 1) != plain {
		t.Error("fonts changed more than the head markup")
	}
	head := out[:strings.Index(out, "</head>")]
	if !strings.Contains(head, "@font-face") || strings.Count(out, `rel="preload"`) < 1 || strings.Contains(out, "googleapis") || strings.Contains(out, "gstatic") {
		t.Errorf("head lacks the fonts:\n%s", head)
	}
	if strings.Count(out, `as="font"`) != 1 {
		t.Errorf("want exactly one font preload")
	}
}

// A community theme with its own base layout, and no plugin-head, still gets
// the fonts.
func TestRenderFontsInCommunityTheme(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "layouts/base.html", `<!doctype html><html><head><title>x</title></head><body>hi</body></html>`)
	e := newTestEngine(t, Options{Layers: []fs.FS{os.DirFS(dir)}})
	v := minimalView()
	v.Site.Fonts = []FontFace{{Family: "Brand Sans", Weight: 400, Style: "normal", Hash: hashA}}
	out := renderToString(t, e, v)
	if !strings.Contains(out, `<title>x</title><style>@font-face{font-family:"Brand Sans"`) {
		t.Errorf("community theme page lacks fonts:\n%s", out)
	}
}

// Editor frames carry the fonts too, so the canvas matches the site.
func TestRenderFontsInEditorFrames(t *testing.T) {
	t.Parallel()
	e := newTestEngine(t, Options{})
	v := minimalView()
	v.Canvas = true
	v.Site.Fonts = []FontFace{{Family: "Brand Sans", Weight: 400, Style: "normal", Hash: hashA}}
	if out := renderToString(t, e, v); !strings.Contains(out, "@font-face") {
		t.Error("canvas frame lacks the fonts")
	}
}
