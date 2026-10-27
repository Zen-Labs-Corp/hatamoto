package render

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"hatamoto.dev/core/internal/builder"
)

const packSchema = `{"type":"object","additionalProperties":false,
	"x-hm":{"label":"Promo banner","children":true},
	"properties":{"title":{"type":"string"}}}`

const (
	packKind   = "promo_banner"
	packHTML   = `<div {{.Attrs}}><h3>{{.P.title}}</h3>{{.Children}}</div>`
	packCSS    = `.hm-promo_banner{color:red} .hm-promo_banner h3{margin:0}`
	packIsland = `document.querySelectorAll(".hm-promo_banner").forEach(function(e){e.dataset.on="1"});`
)

// withPackWidget registers the promo_banner widget in builder and render and
// removes it when the test ends.
func withPackWidget(t *testing.T, island string) {
	t.Helper()
	w, err := builder.ParsePackWidget("promo", "banner", []byte(packSchema), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := builder.SetPackKinds([]builder.PackWidgetSchema{w}); err != nil {
		t.Fatal(err)
	}
	if err := SetPackWidgets([]PackWidget{{Kind: packKind, HTML: []byte(packHTML), CSS: []byte(packCSS), Island: []byte(island)}}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		builder.SetPackKinds(nil)
		SetPackWidgets(nil)
	})
}

func packDoc() *builder.Doc {
	return &builder.Doc{V: 2, Nodes: []*builder.Node{{ID: "p", T: packKind, P: map[string]any{"title": "<b>Hi</b>"},
		C: []*builder.Node{{ID: "t", T: "text", P: map[string]any{"text": "child"}}}}}}
}

func TestPackWidgetRenders(t *testing.T) {
	withPackWidget(t, packIsland)
	pub := renderDoc(t, nil, packDoc(), false)
	main := mainInner(t, pub)
	if !strings.Contains(main, `<div class="hm-n hm-promo_banner hm-n-p">`) || !strings.Contains(main, "<h3>&lt;b&gt;Hi&lt;/b&gt;</h3>") ||
		!strings.Contains(main, "child") {
		t.Errorf("public markup = %s", main)
	}
	if strings.Contains(main, "data-hm-") {
		t.Error("public page has data-hm-*")
	}
	if css := styleBody(t, pub); !strings.Contains(css, ".hm-promo_banner{color:red}.hm-promo_banner h3{margin:0}") {
		t.Errorf("scoped css missing: %.300s", css)
	}
	// CSS only when present.
	if css := styleBody(t, renderDoc(t, nil, textDoc(), false)); strings.Contains(css, "promo_banner") {
		t.Error("css of an absent pack widget emitted")
	}

	canvas := renderDoc(t, nil, packDoc(), true)
	if !strings.Contains(canvas, `data-hm-node="p" data-hm-t="promo_banner"`) {
		t.Errorf("canvas markers missing: %s", mainInner(t, canvas))
	}
	i := strings.Index(canvas, `<style id="hm-builder-base">`)
	if !strings.Contains(canvas[i:i+strings.Index(canvas[i:], "</style>")], ".hm-promo_banner{color:red}") {
		t.Error("canvas base lacks pack css")
	}
	if strings.Contains(canvas, "/theme/assets/builder/") {
		t.Error("canvas has an island")
	}
}

func TestPackIslandTagIntegrityAndServing(t *testing.T) {
	withPackWidget(t, packIsland)
	sum := sha256.Sum256([]byte(packIsland))
	integrity := "sha256-" + base64.StdEncoding.EncodeToString(sum[:])
	is := widgets().island[packKind]
	tag := `<script type="module" src="/theme/assets/builder/` + is.name + `" integrity="` + integrity + `"></script>`

	if pub := renderDoc(t, nil, packDoc(), false); strings.Count(pub, tag) != 1 {
		t.Errorf("want one island tag %s in public page", tag)
	}
	if got := renderDoc(t, nil, textDoc(), false); strings.Contains(got, "/theme/assets/builder/") {
		t.Error("page without the widget has an island")
	}
	if got := renderDoc(t, nil, packDoc(), true); strings.Contains(got, "/theme/assets/builder/") {
		t.Error("canvas has an island")
	}
	// Core islands keep the tag they always had.
	if strings.Contains(string(widgets().islandTags(map[string]bool{"tabs": true, "video": true})), "integrity") {
		t.Error("core island tag carries integrity")
	}

	e, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	h := e.AssetHandler()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, AssetPrefix+IslandPrefix+is.name, nil))
	if rec.Code != 200 || rec.Body.String() != packIsland {
		t.Fatalf("status %d body %q", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(rec.Header().Get("Cache-Control"), "immutable") {
		t.Errorf("headers %v", rec.Header())
	}
	got := sha256.Sum256(rec.Body.Bytes())
	if base64.StdEncoding.EncodeToString(got[:]) != strings.TrimPrefix(integrity, "sha256-") {
		t.Error("served body does not match the integrity digest")
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, AssetPrefix+IslandPrefix+packKind+".00000000.js", nil))
	if rec.Code != 404 {
		t.Errorf("wrong hash: status %d", rec.Code)
	}
}

func TestPackWidgetUninstalledPlaceholder(t *testing.T) {
	withPackWidget(t, packIsland)
	SetPackWidgets(nil)
	builder.SetPackKinds(nil)

	pub := renderDoc(t, nil, packDoc(), false)
	main := mainInner(t, pub)
	if !strings.Contains(main, "<!-- hm-widget: promo_banner not installed -->") || strings.Contains(main, "child") || strings.Contains(main, "<h3>") {
		t.Errorf("public = %s", main)
	}
	if strings.Contains(pub, "promo_banner{") || strings.Contains(pub, "/theme/assets/builder/") {
		t.Error("css or island of an uninstalled widget")
	}
	canvas := renderDoc(t, nil, packDoc(), true)
	want := `<div class="hm-n hm-missing hm-n-p" data-hm-node="p" data-hm-t="promo_banner">Widget &#34;promo_banner&#34; is not installed</div>`
	if !strings.Contains(canvas, want) || strings.Contains(mainInner(t, canvas), "child") {
		t.Errorf("canvas = %s", mainInner(t, canvas))
	}
	if !strings.Contains(canvas, ".hm-missing{") {
		t.Error("canvas lacks the placeholder style")
	}
	// A page without an orphan has no placeholder style (goldens stay as is).
	if strings.Contains(renderDoc(t, nil, textDoc(), true), ".hm-missing{") {
		t.Error("placeholder style on a page without orphans")
	}
	// Hidden orphans render nothing.
	d := packDoc()
	d.Nodes[0].P["hidden"] = true
	if strings.Contains(mainInner(t, renderDoc(t, nil, d, false)), "hm-widget") {
		t.Error("hidden orphan rendered")
	}
}

func TestSetPackWidgetsRefusesBadAndKeepsPrevious(t *testing.T) {
	withPackWidget(t, "")
	bad := []PackWidget{{Kind: "evil_box", HTML: []byte(`<div {{.Attrs}}><script></script></div>`)}}
	if err := SetPackWidgets(bad); err == nil {
		t.Fatal("hostile widget accepted")
	}
	if _, ok := widgets().tmpl[packKind]; !ok {
		t.Error("failed SetPackWidgets dropped the previous set")
	}
	if err := SetPackWidgets([]PackWidget{{Kind: "stat", HTML: []byte(`<div {{.Attrs}}></div>`)}}); err == nil {
		t.Error("core kind accepted")
	}
	// Kinds: embedded first, then packs.
	ks := widgets().kinds
	if ks[len(ks)-1] != packKind {
		t.Errorf("kinds tail = %v", ks[len(ks)-3:])
	}
	if got := len(embeddedWidgets().kinds); got != len(ks)-1 {
		t.Errorf("embedded set mutated: %d vs %d", got, len(ks))
	}
}

// Swapping the sets while rendering must be race free (run with -race), and a
// render never sees a half-built set.
func TestPackSwapWhileRendering(t *testing.T) {
	withPackWidget(t, packIsland)
	e, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				v := minimalView()
				v.Doc = packDoc()
				var sb strings.Builder
				if err := e.RenderPage(&sb, v); err != nil {
					t.Error(err)
					return
				}
				out := sb.String()
				if !strings.Contains(out, "<h3>") && !strings.Contains(out, "not installed") {
					t.Error("neither widget nor placeholder rendered")
					return
				}
			}
		}()
	}
	w := []PackWidget{{Kind: packKind, HTML: []byte(packHTML), CSS: []byte(packCSS), Island: []byte(packIsland)}}
	for i := 0; i < 100; i++ {
		if i%2 == 0 {
			SetPackWidgets(nil)
		} else if err := SetPackWidgets(w); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	wg.Wait()
}

// registerPackHTML replaces the pack widget's template with html.
func registerPackHTML(t *testing.T, html string) {
	t.Helper()
	w, err := builder.ParsePackWidget("promo", "banner", []byte(packSchema), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := builder.SetPackKinds([]builder.PackWidgetSchema{w}); err != nil {
		t.Fatal(err)
	}
	if err := SetPackWidgets([]PackWidget{{Kind: packKind, HTML: []byte(html)}}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		builder.SetPackKinds(nil)
		SetPackWidgets(nil)
	})
}

// A template that fails at run time leaves no half-written markup.
func TestPackWidgetFailureWritesPlaceholderOnly(t *testing.T) {
	registerPackHTML(t, `<div {{.Attrs}}>{{if ne .P.title "Sample title"}}<a title="{{index .P.list 3}}">x</a>{{end}}</div>`)
	pub := renderDoc(t, nil, packDoc(), false)
	main := mainInner(t, pub)
	if !strings.Contains(main, "<!-- hm-widget: promo_banner failed -->") || strings.Contains(main, `<a title="`) || strings.Contains(main, "hm-promo_banner hm-n-p") {
		t.Errorf("failed widget output = %s", main)
	}
	if c := renderDoc(t, nil, packDoc(), true); !strings.Contains(c, "failed to render") {
		t.Errorf("canvas has no failed box")
	}
}

// A validated template that prints more than the cap is replaced.
func TestPackWidgetOutputCap(t *testing.T) {
	registerPackHTML(t, `<div {{.Attrs}}>{{range .P}}{{.}}{{end}}</div>`)
	p := map[string]any{}
	for i := 0; i < 20; i++ {
		p["k"+string(rune('a'+i))] = strings.Repeat("x", 20<<10)
	}
	doc := &builder.Doc{V: 2, Nodes: []*builder.Node{{ID: "p", T: packKind, P: p,
		C: []*builder.Node{{ID: "t", T: "text", P: map[string]any{"text": "child"}}}}}}
	before := degraded.Load()
	main := mainInner(t, renderDoc(t, nil, doc, false))
	if degraded.Load() == before {
		t.Error("an overlong widget did not mark the render degraded")
	}
	if !strings.Contains(main, "failed -->") || len(main) > 2000 {
		t.Errorf("overlong widget output kept (%d bytes)", len(main))
	}
	// small content still renders
	doc.Nodes[0].P = map[string]any{"a": "small"}
	if main := mainInner(t, renderDoc(t, nil, doc, false)); !strings.Contains(main, "small") || strings.Contains(main, "failed") {
		t.Errorf("small widget = %s", main)
	}
}

// Children print once: the sanitised output is spliced with them a single time.
func TestPackWidgetChildrenTwiceFails(t *testing.T) {
	registerPackHTML(t, `<div {{.Attrs}}>{{.Children}}{{.Children}}</div>`)
	main := mainInner(t, renderDoc(t, nil, packDoc(), false))
	if !strings.Contains(main, "failed -->") || strings.Contains(main, "child") {
		t.Errorf("children printed twice: %s", main)
	}
}

func TestPackRangeOverNumberRefused(t *testing.T) {
	for _, tpl := range []string{
		`<div {{.Attrs}}>{{range 2000}}{{range 2000}}x{{end}}{{end}}</div>`,
		`<div {{.Attrs}}>{{range .P}}{{range 2000}}x{{end}}{{end}}</div>`,
	} {
		if err := ValidatePackWidget(packTestKind, PackFiles{HTML: []byte(tpl)}); err == nil || !strings.Contains(err.Error(), "banned-range") {
			t.Errorf("%s: err = %v", tpl, err)
		}
	}
	// a single range over a prop is fine
	if err := ValidatePackWidget(packTestKind, PackFiles{HTML: []byte(`<div {{.Attrs}}>{{range .P}}{{.}}{{end}}</div>`)}); err != nil {
		t.Error(err)
	}
}

// An unresolved text binding on a plugin widget shows the canvas placeholder,
// although the widget was registered after the first render.
func TestPackWidgetBindPlaceholderInCanvas(t *testing.T) {
	renderDoc(t, nil, textDoc(), true) // an earlier render must not freeze the bind table
	schema := `{"type":"object","additionalProperties":false,"x-hm":{"label":"Promo banner"},
		"properties":{"title":{"type":"string","x-hm-bind":"text"}}}`
	w, err := builder.ParsePackWidget("promo", "banner", []byte(schema), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := builder.SetPackKinds([]builder.PackWidgetSchema{w}); err != nil {
		t.Fatal(err)
	}
	if err := SetPackWidgets([]PackWidget{{Kind: packKind, HTML: []byte(packHTML)}}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { builder.SetPackKinds(nil); SetPackWidgets(nil) })
	doc := &builder.Doc{V: 2, Nodes: []*builder.Node{{ID: "p", T: packKind, B: map[string]string{"title": "entity.excerpt"}}}}
	if got := renderDoc(t, nil, doc, true); !strings.Contains(got, "‹entity.excerpt›") {
		t.Errorf("canvas lacks the bind placeholder: %s", mainInner(t, got))
	}
}

// A template that passes the validator reaches the page only through the
// sanitiser, on the public and on the canvas path.
func TestPackWidgetRenderPathSanitises(t *testing.T) {
	registerPackHTML(t, `<div {{.Attrs}}><header data-hm-node="x">t</header><b popover>p</b></div>`)
	for _, canvas := range []bool{false, true} {
		out := mainInner(t, renderDoc(t, nil, packDoc(), canvas))
		for _, bad := range []string{"<header", `data-hm-node="x"`, "popover"} {
			if strings.Contains(out, bad) {
				t.Errorf("canvas=%v: %q survived:\n%s", canvas, bad, out)
			}
		}
		if !strings.Contains(out, "t") || !strings.Contains(out, "<b>p</b>") {
			t.Errorf("canvas=%v: safe content lost:\n%s", canvas, out)
		}
		if canvas && !strings.Contains(out, `data-hm-node="p"`) {
			t.Errorf("canvas marker missing:\n%s", out)
		}
	}
}

// Output with more than one top-level node is refused at install and at render.
func TestPackOneTopLevelElement(t *testing.T) {
	src := `<div {{.Attrs}}>x</div><a href="https://evil/">outside</a>`
	if err := ValidatePackWidget(packTestKind, PackFiles{HTML: []byte(src)}); err == nil {
		t.Error("validator accepted two top-level nodes")
	}
	if _, _, err := sanitizePackOutput([]byte(src), "", ""); err == nil {
		t.Error("sanitiser accepted two top-level nodes")
	}
}

// A panic in the validator is a refusal, and SetPackWidgets keeps the set it had.
func TestPackValidatorPanicIsRefusal(t *testing.T) {
	registerPackHTML(t, packHTML)
	packValidateHook = func() { panic("boom") }
	t.Cleanup(func() { packValidateHook = nil })
	err := ValidatePackWidget(packTestKind, PackFiles{HTML: []byte(packHTML)})
	if err == nil || !strings.Contains(err.Error(), "internal") {
		t.Fatalf("err = %v, want internal", err)
	}
	if err := SetPackWidgets([]PackWidget{{Kind: packKind, HTML: []byte(packHTML)}}); err == nil {
		t.Fatal("SetPackWidgets accepted a panicking validation")
	}
	if widgets().tmpl[packKind] == nil {
		t.Error("previous widget set was not kept")
	}
}

// navlabel is the editor's private row name: a template that ranges over .P must not print it.
func TestPackWidgetDoesNotSeeNavlabel(t *testing.T) {
	registerPackHTML(t, `<div {{.Attrs}}>{{range .P}}[{{.}}]{{end}}</div>`)
	d := packDoc()
	d.Nodes[0].P["navlabel"] = "Secret name"
	main := mainInner(t, renderDoc(t, nil, d, false))
	if strings.Contains(main, "Secret name") || !strings.Contains(main, "Hi") {
		t.Errorf("navlabel reached the pack template or props were lost: %s", main)
	}
	if d.Nodes[0].P["navlabel"] != "Secret name" {
		t.Error("the node's own props were modified")
	}
}
