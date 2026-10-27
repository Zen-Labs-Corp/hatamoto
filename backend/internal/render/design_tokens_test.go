package render

import (
	"bytes"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"hatamoto.dev/core/internal/builder"
)

// optInTheme is a theme layer that turns on the design tokens.
func optInTheme() []fs.FS {
	return []fs.FS{fstest.MapFS{"theme.json": {Data: []byte(`{"name":"tok","version":"1.0.0","builder":{"tokens":true}}`)}}}
}

func testDesign() builder.Design {
	return builder.Design{Tokens: builder.Tokens{
		Color: map[string]string{"accent": "#112233"},
		Space: map[string]string{"4": "1.25rem", "2": "0.75rem"},
	}}
}

func tokenDoc() *builder.Doc {
	return &builder.Doc{V: 2, Nodes: []*builder.Node{{
		ID: "a", T: "container",
		S: map[string]map[string]string{"base": {"pt": "space-4", "fs": "lg", "bg": "accent"}},
		C: []*builder.Node{{ID: "h", T: "heading", S: map[string]map[string]string{"base": {"fs": "xl"}}, P: map[string]any{"text": "x"}}},
	}}}
}

func renderDesign(t *testing.T, layers []fs.FS, d *builder.Doc, design builder.Design, canvas bool) string {
	t.Helper()
	e, err := New(Options{Layers: layers})
	if err != nil {
		t.Fatal(err)
	}
	v := minimalView()
	v.Doc, v.Canvas, v.Site.Design = d, canvas, design
	var buf bytes.Buffer
	if err := e.RenderPage(&buf, v); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// A2: a theme that did not opt in renders byte-identical whatever the site design holds.
func TestDesignIgnoredWithoutOptIn(t *testing.T) {
	nowFunc = func() time.Time { return time.Date(2031, 6, 1, 0, 0, 0, 0, time.UTC) }
	defer func() { nowFunc = time.Now }()
	for name, doc := range map[string]*builder.Doc{"tokens": tokenDoc(), "native": nativeDoc(), "mixed": mixedDoc()} {
		for _, canvas := range []bool{false, true} {
			plain := renderDesign(t, nil, doc, builder.Design{}, canvas)
			with := renderDesign(t, nil, doc, testDesign(), canvas)
			if plain != with {
				t.Errorf("%s canvas=%v: design changed a non-opted-in page", name, canvas)
			}
			if !canvas && (strings.Contains(with, "--hm-accent:#112233") || strings.Contains(with, "--hm-space-2:") || strings.Contains(with, "--hm-fs-")) {
				t.Errorf("%s canvas=%v: token output without opt-in", name, canvas)
			}
		}
	}
	if out := renderDesign(t, nil, tokenDoc(), testDesign(), false); !strings.Contains(out, "font-size:1.25rem;") {
		t.Fatal("fs is not the literal without opt-in")
	}
}

// The v1 golden set with a non-empty design on the default theme: no golden edited.
func TestDesignDoesNotTouchGoldens(t *testing.T) {
	e, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	a := builder.FromSections([]builder.Section{{ID: "a", Type: "content", Content: map[string]any{"body": "<p>one</p>"}}})
	v1, v2 := minimalView(), minimalView()
	v1.Doc, v2.Doc, v2.Site.Design = a, a, testDesign()
	var x, y bytes.Buffer
	if err := e.RenderPage(&x, v1); err != nil {
		t.Fatal(err)
	}
	if err := e.RenderPage(&y, v2); err != nil {
		t.Fatal(err)
	}
	if x.String() != y.String() {
		t.Fatal("design changed a v1-shaped page")
	}
}

func TestOptInTokenBlock(t *testing.T) {
	out := renderDesign(t, optInTheme(), tokenDoc(), testDesign(), false)
	want := `<style id="hm-builder-css">:root{--hm-accent:#112233;--hm-space-2:0.75rem;--hm-space-4:1.25rem;}` + BaseCSS
	if !strings.Contains(out, want) {
		t.Fatalf("token block missing or misplaced:\n%s", out)
	}
	if strings.Count(out, ":root{--hm-accent") != 1 {
		t.Fatal("token block not emitted exactly once")
	}
	if !strings.Contains(out, "font-size:var(--hm-fs-lg,1.25rem);") || !strings.Contains(out, "font-size:var(--hm-fs-xl,1.5rem);") {
		t.Fatalf("fs not compiled to var():\n%s", out)
	}
}

func TestOptInNoBlockWhenDefaults(t *testing.T) {
	for _, d := range []builder.Design{{}, {Tokens: builder.DefaultTokens()}} {
		out := renderDesign(t, optInTheme(), tokenDoc(), d, false)
		if strings.Contains(out, `id="hm-builder-css">:root{`) {
			t.Fatalf("empty :root block emitted:\n%s", out)
		}
		if !strings.Contains(out, "font-size:var(--hm-fs-lg,1.25rem);") {
			t.Fatal("opt-in alone must compile fs to var()")
		}
	}
}

func TestInvalidTokenNotEmitted(t *testing.T) {
	d := builder.Design{Tokens: builder.Tokens{Color: map[string]string{"accent": "red;}</style><script>"}, Space: map[string]string{"4": "1px;}", "99": "1rem"}}}
	out := renderDesign(t, optInTheme(), tokenDoc(), d, false)
	if strings.Contains(out, `id="hm-builder-css">:root{`) || strings.Contains(out, "<script>") {
		t.Fatalf("invalid token emitted:\n%s", out)
	}
}

func TestCanvasTokenBlockAndPatchOpts(t *testing.T) {
	e, err := New(Options{Layers: optInTheme()})
	if err != nil {
		t.Fatal(err)
	}
	v := minimalView()
	v.Doc, v.Canvas, v.Site.Design = tokenDoc(), true, testDesign()
	var buf bytes.Buffer
	if err := e.RenderPage(&buf, v); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, `<style id="hm-builder-base">:root{--hm-accent:#112233;`) {
		t.Fatalf("canvas base lacks the token block:\n%s", out)
	}
	if !strings.Contains(out, `<style data-hm-css="a">.hm-n-a{background-color:var(--hm-accent,#c8102e);padding-top:var(--hm-space-4,1rem);font-size:var(--hm-fs-lg,1.25rem);}`) &&
		!strings.Contains(out, `data-hm-css="a"`) {
		t.Fatal("per-node tag missing")
	}
	// the patch path goes through Engine.NodeCSS and gets the page's mode
	if css := e.NodeCSS(tokenDoc().Nodes[0]); !strings.Contains(css["a"], "var(--hm-fs-lg,1.25rem)") || !strings.Contains(css["h"], "var(--hm-fs-xl,1.5rem)") {
		t.Fatalf("NodeCSS ignores the opt-in: %v", css)
	}
	plain, _ := New(Options{})
	if css := plain.NodeCSS(tokenDoc().Nodes[0]); strings.Contains(css["a"], "--hm-fs-") {
		t.Fatalf("NodeCSS used vars without opt-in: %v", css)
	}
	if strings.Count(out, "--hm-accent:#112233") != 1 {
		t.Fatal("token block must be in the base tag only")
	}
}

func TestLoadThemeBuilderTokensFlag(t *testing.T) {
	for manifest, want := range map[string]bool{
		`{"name":"a","builder":{"tokens":true}}`:  true,
		`{"name":"a","builder":{"tokens":false}}`: false,
		`{"name":"a","builder":{"future":1}}`:     false,
		`{"name":"a"}`:                            false,
	} {
		e, err := New(Options{Layers: []fs.FS{fstest.MapFS{"theme.json": {Data: []byte(manifest)}}}})
		if err != nil {
			t.Fatal(err)
		}
		if got := e.current().theme.BuilderTokens; got != want {
			t.Errorf("%s: BuilderTokens=%v want %v", manifest, got, want)
		}
	}
}

func TestLoadThemeStrictScriptsFlag(t *testing.T) {
	for manifest, want := range map[string]bool{
		`{"name":"a","csp":{"strictScripts":true}}`:  true,
		`{"name":"a","csp":{"strictScripts":false}}`: false,
		`{"name":"a","csp":{}}`:                      false,
		`{"name":"a"}`:                               false,
	} {
		e, err := New(Options{Layers: []fs.FS{fstest.MapFS{"theme.json": {Data: []byte(manifest)}}}})
		if err != nil {
			t.Fatal(err)
		}
		if got := e.StrictScripts(); got != want {
			t.Errorf("%s: StrictScripts=%v want %v", manifest, got, want)
		}
	}
	def, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !def.StrictScripts() {
		t.Error("the default theme must opt in to strict scripts")
	}
}
