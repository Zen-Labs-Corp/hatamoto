package render

import (
	"bytes"
	"context"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"hatamoto.dev/core/internal/budgetfile"
	"hatamoto.dev/core/internal/builder"
)

const (
	partHeader = "11111111-0000-4000-8000-00000000000a"
	partFooter = "11111111-0000-4000-8000-00000000000b"
	partSingle = "11111111-0000-4000-8000-00000000000c"
	partArch   = "11111111-0000-4000-8000-00000000000d"
)

// fakeParts serves blocks and parts from maps; a part is only returned for its slot.
type fakeParts struct {
	blocks fakeRefs
	parts  map[string]partDef
	boom   bool
}

type partDef struct {
	slot string
	doc  *builder.Doc
}

func (f fakeParts) Block(ctx context.Context, id string) (*builder.Doc, bool) {
	return f.blocks.Block(ctx, id)
}

func (f fakeParts) Part(_ context.Context, slot, id string) (*builder.Doc, bool) {
	if f.boom {
		panic("part lookup exploded")
	}
	p, ok := f.parts[id]
	if !ok || p.slot != slot {
		return nil, false
	}
	return p.doc, true
}

func slotNode(id string) *builder.Node { return &builder.Node{ID: id, T: "slot"} }

func bar(id, text string) *builder.Node {
	return &builder.Node{ID: id, T: "container", P: map[string]any{"tag": "div"},
		S: map[string]map[string]string{"base": {"ta": "center"}},
		C: []*builder.Node{{ID: id + "t", T: "text", P: map[string]any{"text": text}}}}
}

func standardParts() fakeParts {
	return fakeParts{parts: map[string]partDef{
		partHeader: {builder.SlotHeader, docOf(bar("hd", "PART-HEADER"))},
		partFooter: {builder.SlotFooter, docOf(bar("ft", "PART-FOOTER"))},
		partSingle: {builder.SlotSingle, docOf(&builder.Node{ID: "wrap", T: "container", P: map[string]any{"tag": "div"},
			C: []*builder.Node{bar("top", "SINGLE-BEFORE"), slotNode("sl"), bar("bot", "SINGLE-AFTER")}})},
		partArch: {builder.SlotArchive, docOf(bar("a1", "ARCHIVE-BEFORE"), slotNode("sl"), bar("a2", "ARCHIVE-AFTER"))},
	}}
}

func assigned(v *PageView, m map[string]string) *PageView {
	v.Site.Design.Parts = m
	return v
}

var allAssigned = map[string]string{
	builder.SlotHeader: partHeader, builder.SlotFooter: partFooter,
	builder.SlotSingle: partSingle, builder.SlotArchive: partArch,
}

func themeOptIn(slots string) fs.FS {
	return fstest.MapFS{"theme.json": {Data: []byte(`{"name":"parts","version":"1.0.0","builder":{"parts":` + slots + `}}`)}}
}

func partsEngine(t *testing.T, src RefSource, layers ...fs.FS) *Engine {
	t.Helper()
	e, err := New(Options{Layers: layers, Refs: src})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func page(t *testing.T, e *Engine, v *PageView) string {
	t.Helper()
	var buf bytes.Buffer
	if err := e.RenderPage(&buf, v); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func views() map[string]func() *PageView {
	return map[string]func() *PageView{
		"page": func() *PageView { v := minimalView(); v.Doc = nativeDoc(); return v },
		"single": func() *PageView {
			v := minimalView()
			v.Kind, v.SchemaSlug, v.Doc = KindSingle, "post", nativeDoc()
			return v
		},
		"archive": func() *PageView {
			v := minimalView()
			v.Kind, v.SchemaSlug = KindArchive, "post"
			return v
		},
	}
}

// ISC-17: a theme that did not opt in renders byte for byte the same whatever
// parts are assigned; so does an opted-in theme with nothing assigned.
func TestPartsNonOptedAndUnassignedAreByteIdentical(t *testing.T) {
	plain, _ := New(Options{})
	for name, mk := range views() {
		want := page(t, plain, mk())
		if got := page(t, partsEngine(t, standardParts()), assigned(mk(), allAssigned)); got != want {
			t.Errorf("%s: non-opted theme changed with parts assigned", name)
		}
		opted := partsEngine(t, standardParts(), themeOptIn(`["header","footer","single","archive"]`))
		if got := page(t, opted, mk()); got != want {
			t.Errorf("%s: opted-in theme without assignment changed", name)
		}
		if got := page(t, opted, assigned(mk(), map[string]string{})); got != want {
			t.Errorf("%s: opted-in theme with an empty assignment changed", name)
		}
	}
}

func TestPartsHeaderAndFooterReplaceThemePartials(t *testing.T) {
	e := partsEngine(t, standardParts(), themeOptIn(`["header","footer"]`))
	got := page(t, e, assigned(views()["page"](), allAssigned))
	for _, want := range []string{"PART-HEADER", "PART-FOOTER"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s", want)
		}
	}
	if strings.Contains(got, `class="hm-header"`) || strings.Contains(got, `class="hm-footer"`) {
		t.Error("the theme's own header/footer still printed")
	}
	if strings.Contains(got, "SINGLE-BEFORE") {
		t.Error("single part used although the theme opted in to header and footer only")
	}
	only := partsEngine(t, standardParts(), themeOptIn(`["header"]`))
	got = page(t, only, assigned(views()["page"](), allAssigned))
	if !strings.Contains(got, "PART-HEADER") || strings.Contains(got, "PART-FOOTER") || !strings.Contains(got, `class="hm-footer"`) {
		t.Error("an unopted slot must keep the theme's partial")
	}
}

func TestPartsSingleAndArchiveWrapUnchangedContent(t *testing.T) {
	e := partsEngine(t, standardParts(), themeOptIn(`["single","archive"]`))
	plain, _ := New(Options{})
	for name, tc := range map[string]struct{ before, after string }{
		"single":  {"SINGLE-BEFORE", "SINGLE-AFTER"},
		"archive": {"ARCHIVE-BEFORE", "ARCHIVE-AFTER"},
	} {
		mk := views()[name]
		orig := mainInner(t, page(t, plain, mk()))
		got := mainInner(t, page(t, e, assigned(mk(), allAssigned)))
		i, j, k := strings.Index(got, tc.before), strings.Index(got, strings.TrimSpace(orig)), strings.Index(got, tc.after)
		if i < 0 || j < i || k < j {
			t.Fatalf("%s: want before < theme content < after, got %d %d %d:\n%s", name, i, j, k, got)
		}
		if strings.Contains(got, slotMarker) {
			t.Errorf("%s: slot marker leaked", name)
		}
	}
}

func TestPartsFallBackToThemeMarkup(t *testing.T) {
	plain, _ := New(Options{})
	good := standardParts()
	cases := map[string]struct {
		src  fakeParts
		slot string
		view string
		id   string
	}{
		"single without slot": {fakeParts{parts: map[string]partDef{partSingle: {builder.SlotSingle, docOf(bar("x", "NO-SLOT"))}}}, builder.SlotSingle, "single", partSingle},
		"single two slots":    {fakeParts{parts: map[string]partDef{partSingle: {builder.SlotSingle, docOf(slotNode("a"), slotNode("b"))}}}, builder.SlotSingle, "single", partSingle},
		"hidden slot":         {fakeParts{parts: map[string]partDef{partSingle: {builder.SlotSingle, docOf(&builder.Node{ID: "a", T: "slot", P: map[string]any{"hidden": true}})}}}, builder.SlotSingle, "single", partSingle},
		"wrong slot":          {fakeParts{parts: map[string]partDef{partHeader: {builder.SlotFooter, good.parts[partFooter].doc}}}, builder.SlotHeader, "page", partHeader},
		"unknown id":          {fakeParts{}, builder.SlotHeader, "page", partHeader},
		"renders nothing":     {fakeParts{parts: map[string]partDef{partHeader: {builder.SlotHeader, docOf(&builder.Node{ID: "h", T: "text", P: map[string]any{"text": "x", "hidden": true}})}}}, builder.SlotHeader, "page", partHeader},
		"lookup panics":       {fakeParts{boom: true}, builder.SlotHeader, "page", partHeader},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			e := partsEngine(t, c.src, themeOptIn(`["header","footer","single","archive"]`))
			mk := views()[c.view]
			want := page(t, plain, mk())
			if got := page(t, e, assigned(mk(), map[string]string{c.slot: c.id})); got != want {
				t.Errorf("did not fall back to the theme markup")
			}
		})
	}
}

func TestPartsNoPartsAndNilSource(t *testing.T) {
	plain, _ := New(Options{})
	e := partsEngine(t, standardParts(), themeOptIn(`["header","footer"]`))
	v := assigned(views()["page"](), allAssigned)
	v.NoParts = true
	if page(t, e, v) != page(t, plain, views()["page"]()) {
		t.Error("NoParts must render the theme's own chrome")
	}
	nilSrc, err := New(Options{Layers: []fs.FS{themeOptIn(`["header"]`)}})
	if err != nil {
		t.Fatal(err)
	}
	if page(t, nilSrc, assigned(views()["page"](), allAssigned)) != page(t, plain, views()["page"]()) {
		t.Error("an engine without a RefSource must ignore assigned parts")
	}
}

// The part's CSS joins the page head once, its block refs expand, and a part
// used on a page without a doc still gets a stylesheet.
func TestPartsCSSAndBlocks(t *testing.T) {
	src := standardParts()
	src.blocks = fakeRefs{blkA: docOf(textNode("bt", "BLOCK-IN-PART"))}
	src.parts[partHeader] = partDef{builder.SlotHeader, docOf(bar("hd", "PART-HEADER"), refNode("rf", blkA))}
	e := partsEngine(t, src, themeOptIn(`["header"]`))
	v := assigned(minimalView(), map[string]string{builder.SlotHeader: partHeader}) // v1 page: no Doc
	got := page(t, e, v)
	if !strings.Contains(got, "BLOCK-IN-PART") {
		t.Error("ref inside a part not expanded")
	}
	css := styleBody(t, got)
	if n := strings.Count(css, ".hm-n-ph_hd{"); n != 1 {
		t.Errorf("part css emitted %d times:\n%s", n, css)
	}
	if strings.Count(got, `id="hm-builder-css"`) != 1 {
		t.Error("expected exactly one builder stylesheet")
	}
	if strings.Contains(got, "data-hm-node") || strings.Contains(got, "data-hm-t=") {
		t.Error("public part markup carries canvas markers")
	}
	// the page's own nodes keep their ids and rules next to the part's
	both := assigned(views()["page"](), map[string]string{builder.SlotHeader: partHeader})
	css = styleBody(t, page(t, e, both))
	if !strings.Contains(css, ".hm-n-row{") || !strings.Contains(css, ".hm-n-ph_hd{") {
		t.Errorf("page and part css must both be present:\n%s", css)
	}
}

func TestPartsLayeredThemeAndPluginPartial(t *testing.T) {
	reg := newPluginRegistry()
	if err := reg.registerPartial("acme", "card", `{{define "plugin/acme/card"}}<i>card</i>{{end}}`); err != nil {
		t.Fatal(err)
	}
	pkg := fstest.MapFS{
		"theme.json":                 {Data: []byte(`{"name":"own","version":"1.0.0","builder":{"parts":["header","single"]}}`)},
		"partials/header.html":       {Data: []byte(`{{define "header"}}<header id="own-header">{{.Site.Name}}{{template "plugin/acme/card" .}}</header>{{end}}`)},
		"layouts/single-widget.html": {Data: []byte(`{{define "content"}}<article id="own-single">{{.Page.Title}}</article>{{end}}`)},
	}
	e, err := newEngine(Options{Layers: []fs.FS{pkg}, Refs: standardParts()}, reg)
	if err != nil {
		t.Fatal(err)
	}
	mk := func() *PageView {
		v := minimalView()
		v.Kind, v.SchemaSlug = KindSingle, "widget"
		return v
	}
	plain := page(t, e, mk())
	if !strings.Contains(plain, `id="own-header"`) || !strings.Contains(plain, "<i>card</i>") || !strings.Contains(plain, `id="own-single"`) {
		t.Fatalf("own theme not rendered:\n%s", plain)
	}
	got := page(t, e, assigned(mk(), allAssigned))
	if strings.Contains(got, `id="own-header"`) || !strings.Contains(got, "PART-HEADER") {
		t.Error("header part must replace the package's header")
	}
	if !strings.Contains(got, `id="own-single"`) || !strings.Contains(got, "SINGLE-BEFORE") || strings.Index(got, "SINGLE-BEFORE") > strings.Index(got, `id="own-single"`) {
		t.Error("single part must wrap the package's layout content")
	}
	// a single schema the package has no layout for falls to the default single, also wrapped
	v := minimalView()
	v.Kind, v.SchemaSlug = KindSingle, "post"
	if got := page(t, e, assigned(v, allAssigned)); !strings.Contains(got, "SINGLE-BEFORE") {
		t.Error("default single layout of a layered theme not wrapped")
	}
	if err := e.CheckTheme(pkg); err != nil {
		t.Errorf("CheckTheme on an opted-in theme: %v", err)
	}
}

func TestThemeBuilderPartsManifest(t *testing.T) {
	th, err := LoadTheme(layered([]fs.FS{themeOptIn(`["header","bogus","single","header"]`)}))
	if err != nil {
		t.Fatal(err)
	}
	if len(th.BuilderParts) != 2 || !th.BuilderParts["header"] || !th.BuilderParts["single"] {
		t.Errorf("BuilderParts = %v", th.BuilderParts)
	}
	plain, _ := LoadTheme(layered(nil))
	if plain.BuilderParts != nil {
		t.Errorf("default theme must not opt in: %v", plain.BuilderParts)
	}
}

// A part's own canvas shows a placeholder for the slot, and the public render
// of the same doc leaves the marker the wrapper splits at.
func TestSlotNodeRendering(t *testing.T) {
	e := partsEngine(t, nil)
	d := docOf(slotNode("sl"))
	if got := mainInner(t, renderWith(t, e, d, false)); !strings.Contains(got, slotMarker) {
		t.Errorf("public slot = %q", got)
	}
	can := renderWith(t, e, d, true)
	if !strings.Contains(can, `data-hm-node="sl" data-hm-t="slot"`) || !strings.Contains(can, "hm-slot{") {
		t.Errorf("canvas slot placeholder missing:\n%s", mainInner(t, can))
	}
}

// ISC-26 (render side): the all-widgets page plus three blocks and header and
// footer parts keeps the builder stylesheet within the per-page budget.
func TestBudgetPartsAndBlocksCSS(t *testing.T) {
	all := loadWidgetDoc(t, "all")
	src := standardParts()
	src.blocks = fakeRefs{
		blkA: docOf(bar("a", "one")), blkB: docOf(bar("b", "two")), blkC: docOf(bar("c", "three")),
	}
	src.parts[partHeader] = partDef{builder.SlotHeader, docOf(bar("hd", "H"), refNode("r", blkA))}
	src.parts[partFooter] = partDef{builder.SlotFooter, docOf(bar("ft", "F"), refNode("r", blkB))}
	e := partsEngine(t, src, themeOptIn(`["header","footer"]`))
	d := docOf(append(append([]*builder.Node{}, all.Nodes...), refNode("x1", blkA), refNode("x2", blkB), refNode("x3", blkC))...)
	v := assigned(minimalView(), map[string]string{builder.SlotHeader: partHeader, builder.SlotFooter: partFooter})
	v.Doc = d
	css := styleBody(t, page(t, e, v))
	gz := gzipLen(t, css)
	t.Logf("all widgets + 3 blocks + header/footer parts: %d bytes css, %d gzip (budget %d)", len(css), gz, pageCSSBudget)
	if gz > pageCSSBudget {
		t.Errorf("builder css %d B gzip > %d", gz, pageCSSBudget)
	}
	budgetfile.Check(t, "render", "page_css_parts_blocks_gzip", int64(gz), budgetfile.Metric{Unit: "bytes", Cap: pageCSSBudget, TolAbs: 1024})
}

func replaceSlot(id string) *builder.Node {
	return &builder.Node{ID: id, T: "slot", P: map[string]any{"replace": true}}
}

func replacingParts(slot string, nodes ...*builder.Node) fakeParts {
	return fakeParts{parts: map[string]partDef{partArch: {slot, docOf(nodes...)}, partSingle: {builder.SlotSingle, docOf(nodes...)}}}
}

// An archive part whose slot has replace prints Before and After and not the
// theme's own content; without replace the same part is the wrapped B6 shape.
func TestPartsArchiveReplaceDropsThemeContent(t *testing.T) {
	plain, _ := New(Options{})
	mk := func() *PageView {
		v := views()["archive"]()
		v.Items = []RelatedItem{{Title: "ThemeListedPost", URL: "/blog/x"}}
		return assigned(v, map[string]string{builder.SlotArchive: partArch})
	}
	orig := mainInner(t, page(t, plain, mk()))
	if !strings.Contains(orig, "ThemeListedPost") {
		t.Fatalf("fixture: theme listing missing:\n%s", orig)
	}
	opted := themeOptIn(`["archive"]`)

	keep := partsEngine(t, replacingParts(builder.SlotArchive, bar("a1", "ARCHIVE-BEFORE"), slotNode("sl"), bar("a2", "ARCHIVE-AFTER")), opted)
	got := mainInner(t, page(t, keep, mk()))
	if !strings.Contains(got, "ThemeListedPost") || !strings.Contains(got, "ARCHIVE-BEFORE") {
		t.Errorf("without replace the theme content must stay:\n%s", got)
	}

	repl := partsEngine(t, replacingParts(builder.SlotArchive, bar("a1", "ARCHIVE-BEFORE"), replaceSlot("sl"), bar("a2", "ARCHIVE-AFTER")), opted)
	got = mainInner(t, page(t, repl, mk()))
	if strings.Contains(got, "ThemeListedPost") || strings.Contains(got, slotMarker) {
		t.Errorf("replace left the theme listing or the marker:\n%s", got)
	}
	if i, k := strings.Index(got, "ARCHIVE-BEFORE"), strings.Index(got, "ARCHIVE-AFTER"); i < 0 || k < i {
		t.Errorf("part markup missing or out of order:\n%s", got)
	}

	// a loop from the archive's own items takes the listing's place
	loop := &builder.Node{ID: "lp", T: "loop", P: map[string]any{"from": "archive"}, C: []*builder.Node{
		{ID: "lh", T: "heading", B: map[string]string{"text": "loop.item.title"}}}}
	lp := partsEngine(t, replacingParts(builder.SlotArchive, replaceSlot("sl"), loop), opted)
	got = mainInner(t, page(t, lp, mk()))
	if strings.Count(got, "ThemeListedPost") != 1 || !strings.Contains(got, `class="hm-loop__item"`) {
		t.Errorf("loop listing:\n%s", got)
	}
}

// replace is honoured for archive parts only, and a hidden slot still skips the part.
func TestPartsReplaceIgnoredOutsideArchive(t *testing.T) {
	plain, _ := New(Options{})
	e := partsEngine(t, replacingParts(builder.SlotSingle, bar("a1", "SINGLE-BEFORE"), replaceSlot("sl")), themeOptIn(`["single"]`))
	mk := views()["single"]
	orig := mainInner(t, page(t, plain, mk()))
	got := mainInner(t, page(t, e, assigned(mk(), map[string]string{builder.SlotSingle: partSingle})))
	if !strings.Contains(got, "SINGLE-BEFORE") || !strings.Contains(got, strings.TrimSpace(orig)) {
		t.Errorf("single part must keep the theme content:\n%s", got)
	}
	hidden := &builder.Node{ID: "sl", T: "slot", P: map[string]any{"hidden": true, "replace": true}}
	e = partsEngine(t, replacingParts(builder.SlotArchive, bar("a1", "X"), hidden), themeOptIn(`["archive"]`))
	amk := views()["archive"]
	if got, want := page(t, e, assigned(amk(), map[string]string{builder.SlotArchive: partArch})), page(t, plain, amk()); got != want {
		t.Error("a part with a hidden slot must fall back to the theme markup")
	}
}
