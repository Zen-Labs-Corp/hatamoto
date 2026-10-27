package render

import (
	"bytes"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/builder"
)

func TestParseVideo(t *testing.T) {
	ok := map[string][2]string{
		"https://youtu.be/dQw4w9WgXcQ":                    {"https://www.youtube.com/watch?v=dQw4w9WgXcQ", "https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ"},
		"https://www.youtube.com/watch?v=dQw4w9WgXcQ&t=3": {"https://www.youtube.com/watch?v=dQw4w9WgXcQ", "https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ"},
		"https://m.youtube.com/shorts/dQw4w9WgXcQ":        {"https://www.youtube.com/watch?v=dQw4w9WgXcQ", "https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ"},
		"http://vimeo.com/76979871":                       {"https://vimeo.com/76979871", "https://player.vimeo.com/video/76979871"},
	}
	for in, want := range ok {
		got, found := parseVideo(in)
		if !found || got.Watch != want[0] || got.Embed != want[1] {
			t.Errorf("%s: got %+v %v", in, got, found)
		}
	}
	for _, in := range []string{
		"", "javascript:alert(1)", "https://evil.example/watch?v=dQw4w9WgXcQ", "https://youtube.com.evil.example/watch?v=dQw4w9WgXcQ",
		"https://youtu.be/short", "https://youtu.be/dQw4w9WgXcQ/extra", "https://vimeo.com/abc", "https://www.youtube.com/watch?v=<script>",
		"//youtu.be/dQw4w9WgXcQ", "data:text/html,x",
	} {
		if got, found := parseVideo(in); found {
			t.Errorf("%q accepted: %+v", in, got)
		}
	}
}

// Video renders a facade: no iframe, no script, no third-party URL but the link and data-embed.
func TestVideoFacadeMakesNoThirdPartyRequest(t *testing.T) {
	out := mainInner(t, renderDoc(t, nil, loadWidgetDoc(t, "video"), false))
	if strings.Contains(out, "<iframe") || strings.Contains(out, "<script") {
		t.Fatalf("facade must not embed: %s", out)
	}
	for _, attr := range []string{`src="`, `srcset="`, `poster="`} {
		for _, part := range strings.Split(out, attr)[1:] {
			if v := part[:strings.IndexByte(part, '"')]; !strings.HasPrefix(v, "/") {
				t.Errorf("%s%s is not same-origin", attr, v)
			}
		}
	}
	if strings.Contains(out, "javascript:") {
		t.Error("javascript: URL kept")
	}
	if strings.Contains(out, "data-embed=\"https://example.org") {
		t.Error("an unknown host got an embed")
	}
}

func TestMediaWidgetsRemoteImagesDropped(t *testing.T) {
	d := &builder.Doc{V: 2, Nodes: []*builder.Node{
		{ID: "m", T: "map", P: map[string]any{"query": "x", "image": "https://tracker.example/m.png"}},
		{ID: "v", T: "video", P: map[string]any{"url": "https://vimeo.com/1", "poster": "//tracker.example/p.png"}},
	}}
	out := mainInner(t, renderDoc(t, nil, d, false))
	if strings.Contains(out, "tracker.example") {
		t.Errorf("remote image kept: %s", out)
	}
}

func TestImageDecorativeAndSrcset(t *testing.T) {
	d := &builder.Doc{V: 2, Nodes: []*builder.Node{
		{ID: "a", T: "image", P: map[string]any{"src": "/media/a.jpg", "alt": "A", "decorative": true}},
		{ID: "b", T: "image", P: map[string]any{"src": "/media/a.jpg", "alt": "B"}},
	}}
	out := mainInner(t, renderDoc(t, nil, d, false))
	if !strings.Contains(out, `alt=""`) || strings.Contains(out, `alt="A"`) {
		t.Errorf("decorative image keeps its alt: %s", out)
	}
	if strings.Contains(out, "srcset") {
		t.Errorf("no variants, no srcset: %s", out)
	}
	e, err := New(Options{Data: fakeData{}})
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	v := minimalView()
	v.Doc = d
	if err := e.RenderPage(&buf, v); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); !strings.Contains(got, `srcset="/media/a_medium.jpg 300w, /media/a_large.jpg 1024w" sizes="(max-width:1024px) 100vw, 1024px"`) {
		t.Errorf("srcset missing: %s", got)
	}
}

func TestIslandsEmbedded(t *testing.T) {
	ws := widgets()
	for _, k := range []string{"tabs", "video"} {
		is, ok := ws.island[k]
		if !ok {
			t.Fatalf("%s has no island", k)
		}
		if n := len(is.body); n == 0 || n > 1536 {
			t.Errorf("%s island is %d bytes", k, n)
		}
		for _, bad := range []string{"eval(", "innerHTML", "document.write", "new Function"} {
			if strings.Contains(string(is.body), bad) {
				t.Errorf("%s island uses %s", k, bad)
			}
		}
	}
	// The lightbox island belongs to image and gallery, not to a widget kind.
	if _, ok := ws.island[lightboxKind]; !ok || len(ws.island) != 3 {
		t.Errorf("islands = %d, only tabs, video and the lightbox have one", len(ws.island))
	}
}
