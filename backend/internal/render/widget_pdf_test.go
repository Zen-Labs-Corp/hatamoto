package render

import (
	"regexp"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/builder"
)

func TestPDFSrc(t *testing.T) {
	for src, want := range map[string]string{
		"/media/x.pdf":              "/media/x.pdf",
		" /media/sub/x.pdf ":        "/media/sub/x.pdf",
		"/media/a%20b.pdf":          "/media/a%20b.pdf",
		"/media/sub/x.pdf?x=1":      "", // a query or fragment is never kept
		"/media/x.pdf#page=2":       "",
		"https://example.org/a.pdf": "",
		"javascript:alert(1)":       "",
		"//host/media/x.pdf":        "",
		`/\host/media/x.pdf`:        "",
		"/other/x.pdf":              "",
		"/media/../x.pdf":           "",
		"media/x.pdf":               "",
		`/media/x".pdf`:             "",
		"/media/x pdf":              "",
		"":                          "",
	} {
		if got := pdfSrc(src); got != want {
			t.Errorf("pdfSrc(%q) = %q, want %q", src, got, want)
		}
	}
}

// fileSizerData is a DataSource that knows the size of one file.
type fileSizerData struct {
	sizerData
	sizes map[string]int64
}

func (f *fileSizerData) FileSize(u string) (int64, bool) { n, ok := f.sizes[u]; return n, ok }

func renderPDF(t *testing.T, ds DataSource, p map[string]any, canvas bool) string {
	t.Helper()
	e, err := New(Options{Data: ds})
	if err != nil {
		t.Fatal(err)
	}
	v := minimalView()
	v.Doc, v.Canvas = &builder.Doc{V: 2, Nodes: []*builder.Node{{ID: "p", T: "pdf", P: p}}}, canvas
	var b strings.Builder
	if err := e.RenderPage(&b, v); err != nil {
		t.Fatal(err)
	}
	return mainInner(t, b.String())
}

func TestPDFWidgetMarkup(t *testing.T) {
	out := renderPDF(t, nil, map[string]any{"src": "/media/b.pdf", "title": "Brochure"}, false)
	link, frame := strings.Index(out, `<a class="hm-pdf__link"`), strings.Index(out, `<iframe class="hm-pdf__frame"`)
	if link < 0 || frame < link {
		t.Errorf("the link must come before the frame: %s", out)
	}
	for _, want := range []string{`title="Brochure"`, `loading="lazy"`, `height="600"`, `>Download PDF</a>`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in %s", want, out)
		}
	}
	for _, bad := range []string{"<object", "<embed", " style=", "onload", "sandbox"} {
		if strings.Contains(out, bad) {
			t.Errorf("%q in %s", bad, out)
		}
	}
}

func TestPDFWidgetRefusesForeignSources(t *testing.T) {
	for _, src := range []string{"https://example.org/a.pdf", "javascript:alert(1)", "//evil.example/a.pdf", "", "/other/a.pdf"} {
		out := renderPDF(t, nil, map[string]any{"src": src}, false)
		if strings.Contains(out, "<iframe") || strings.Contains(out, "<a ") {
			t.Errorf("src %q rendered %s", src, out)
		}
	}
}

func TestPDFWidgetHeightClamp(t *testing.T) {
	h := regexp.MustCompile(`height="(\d+)"`)
	for in, want := range map[any]string{nil: "600", 5000: "2000", 50: "200", 800: "800", float64(1200): "1200"} {
		p := map[string]any{"src": "/media/b.pdf"}
		if in != nil {
			p["height"] = in
		}
		if m := h.FindStringSubmatch(renderPDF(t, nil, p, false)); m == nil || m[1] != want {
			t.Errorf("height %v -> %v, want %s", in, m, want)
		}
	}
}

func TestPDFWidgetLinkText(t *testing.T) {
	ds := &fileSizerData{sizes: map[string]int64{"/media/big.pdf": 2516582, "/media/small.pdf": 300, "/media/mid.pdf": 51200}}
	for _, c := range []struct{ src, text, want string }{
		{"/media/big.pdf", "", ">Download PDF (2.4 MB)</a>"},
		{"/media/mid.pdf", "", ">Download PDF (50 KB)</a>"},
		{"/media/small.pdf", "", ">Download PDF (1 KB)</a>"},
		{"/media/unknown.pdf", "", ">Download PDF</a>"},
		{"/media/big.pdf", "Get it", ">Get it</a>"}, // an author's text is verbatim
	} {
		p := map[string]any{"src": c.src}
		if c.text != "" {
			p["linktext"] = c.text
		}
		if out := renderPDF(t, ds, p, false); !strings.Contains(out, c.want) {
			t.Errorf("%s/%q: want %s in %s", c.src, c.text, c.want, out)
		}
	}
	// A DataSource without FileSizer adds no size.
	if out := renderPDF(t, &sizerData{}, map[string]any{"src": "/media/big.pdf"}, false); !strings.Contains(out, ">Download PDF</a>") {
		t.Errorf("no sizer: %s", out)
	}
}

func TestPDFWidgetCanvasHasNoFrame(t *testing.T) {
	out := renderPDF(t, nil, map[string]any{"src": "/media/b.pdf", "title": "Brochure"}, true)
	if strings.Contains(out, "<iframe") || !strings.Contains(out, "hm-pdf__ph") {
		t.Errorf("canvas: %s", out)
	}
}
