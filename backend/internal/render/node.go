package render

import (
	"bytes"
	"fmt"
	"html"
	"html/template"
	"maps"
	"sort"
	"strconv"
	"strings"

	"hatamoto.dev/core/internal/builder"
	"hatamoto.dev/core/internal/entity"
)

// BaseCSS is the builder's own stylesheet for containers and core widgets.
// Defaults only; every StyleSpec property overrides through .hm-n-<id>.
const BaseCSS = `.hm-c{display:flex;flex-direction:column;box-sizing:border-box;min-width:0}` +
	`.hm-c--grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr))}` +
	`.hm-c--page{max-width:var(--hm-container,72rem);margin-inline:auto;padding-inline:var(--hm-space-4,1rem)}` +
	`.hm-w-btn{display:inline-block;align-self:flex-start}` +
	`.hm-w-img{max-width:100%;height:auto;display:block}` +
	`.hm-w-spacer{height:var(--hm-space-8,2rem)}` +
	`.hm-w-divider{border:0;border-top:1px solid var(--hm-hair,#ddd);width:100%}`

// Feature CSS is emitted only for pages that use the feature (the canvas gets
// all of it), so the base sheet and every other page stay byte-identical.
const (
	figureCSS  = `.hm-w-fig{margin:0}.hm-w-cap{margin-top:var(--hm-space-2,.5rem);font-size:.875em;opacity:.8}`
	galleryCSS = `.hm-gallery :where(img){width:100%;height:auto}.hm-gallery :where(a,figure){display:block}`
	imglinkCSS = `.hm-w-imglink{display:block;color:inherit;text-decoration:none}`
	clinkCSS   = `.hm-c--link{color:inherit;text-decoration:none}`
	// lightboxCSS styles the dialog the lightbox island builds (class-only: the
	// island sets no inline style).
	lightboxCSS = `.hm-lb{box-sizing:border-box;width:100%;height:100%;max-width:none;max-height:none;padding:0;border:0;background:rgba(0,0,0,.85);color:#fff}` +
		`.hm-lb::backdrop{background:transparent}` +
		`.hm-lb__in{box-sizing:border-box;display:flex;flex-direction:column;align-items:center;justify-content:center;gap:.75rem;height:100%;padding:1rem}` +
		`.hm-lb__img{max-width:100%;max-height:calc(100vh - 9rem);object-fit:contain}` +
		`.hm-lb__cap{max-width:60rem;margin:0;text-align:center}` +
		`.hm-lb__sr{position:absolute;width:1px;height:1px;overflow:hidden;clip:rect(0 0 0 0);white-space:nowrap}` +
		`html:has(dialog.hm-lb[open]){overflow:hidden}` +
		`.hm-lb__bar{display:flex;align-items:center;gap:.75rem}` +
		`.hm-lb__btn{padding:.5rem .9rem;border:1px solid currentColor;border-radius:.25rem;background:rgba(0,0,0,.5);color:inherit;font:inherit;cursor:pointer}` +
		`.hm-lb__btn:focus-visible{outline:3px solid #fff;outline-offset:2px}` +
		`.hm-lb[open]{animation:hm-lb-in .15s ease}@keyframes hm-lb-in{from{opacity:0}to{opacity:1}}` +
		`@media (prefers-reduced-motion:reduce){.hm-lb[open]{animation:none}}`
)

// nativeSentinel fills preparedSection.Comment of a native block: the default
// section partial then prints CommentHTML (the node markup) with no wrapper,
// and isolateSections skips it.
const nativeSentinel = "native"

// builderCSS is the per-page CSS of a doc: the base sheet plus each styled node.
type builderCSS struct {
	css    map[string]string
	canvas bool
	native bool // the doc has at least one native node
	// kinds are the widget kinds present in the doc; their CSS and islands
	// are emitted, no others.
	kinds map[string]bool
	// fig, imglink and clink say the doc has a captioned image, a linked image
	// (or lightbox) and a linked container: each adds its feature CSS.
	fig, imglink, clink, lightbox bool
	// tokens is the ":root{...}" design-token block, "" when the theme did not
	// opt in or no token differs from its default.
	tokens string
	opts   builder.CSSOpts
	// presets are the preset names the doc's visible native nodes reference.
	presets map[string]bool
	// ref is set when the doc holds a ref that cannot render: the canvas then
	// gets its placeholder style.
	ref bool
	// slot is set when the doc holds a slot node (a theme part's canvas).
	slot bool
	// missing is set when the doc holds a plugin widget that is not installed
	// (the canvas then gets its placeholder style).
	missing bool
	// presetCSS is the rules of the presets to emit: the used ones publicly,
	// every one in the canvas (a patch may apply a preset the page did not use).
	presetCSS string
}

// newBuilderCSS starts the per-page CSS of a render: the compile mode and the
// design tokens of a theme that opted in.
func newBuilderCSS(c *compiled, v *PageView, canvas bool) builderCSS {
	bc := builderCSS{css: map[string]string{}, canvas: canvas}
	if c.theme.BuilderTokens {
		bc.opts = builder.CSSOpts{Vars: true}
		bc.tokens = v.Site.Design.TokenCSS()
	}
	bc.opts.Force = canvas // forced state selectors exist in the canvas only
	return bc
}

// collectKinds records the widget kinds of a subtree. A disabled node renders
// nothing, so it and its subtree add no CSS or island.
func (b *builderCSS) collectKinds(n *builder.Node) {
	if n == nil || nodeHidden(n) {
		return
	}
	if p := builder.NodePreset(n); p != "" {
		if b.presets == nil {
			b.presets = map[string]bool{}
		}
		b.presets[p] = true
	}
	b.collectFeatures(n)
	if _, ok := widgets().tmpl[n.T]; ok {
		if b.kinds == nil {
			b.kinds = map[string]bool{}
		}
		b.kinds[n.T] = true
	} else if builder.IsPackKind(n.T) {
		b.missing = true
		return // an uninstalled widget drops its children
	}
	for _, c := range n.C {
		b.collectKinds(c)
	}
}

// collectFeatures flags the element settings of n that need their own CSS.
func (b *builderCSS) collectFeatures(n *builder.Node) {
	set := func(key string) bool { return str(n.P, key) != "" || n.B[key] != "" }
	switch n.T {
	case "image":
		b.fig = b.fig || set("caption")
		b.imglink = b.imglink || set("href") || n.P["lightbox"] == true
		b.markLightbox(n.P["lightbox"] == true && (n.B["src"] != "" || builder.LightboxSrc(str(n.P, "src"))))
	case "gallery":
		b.imglink = b.imglink || n.P["lightbox"] == true
		b.markLightbox(n.P["lightbox"] == true)
	case "container":
		b.clink = b.clink || set("href")
	}
}

// markLightbox records that the page has a lightbox: its dialog CSS and island.
func (b *builderCSS) markLightbox(on bool) {
	if !on {
		return
	}
	b.lightbox = true
	if b.kinds == nil {
		b.kinds = map[string]bool{}
	}
	b.kinds[lightboxKind] = true
}

// featureCSS is the CSS of the element settings in use (all of it in the canvas).
func (b builderCSS) featureCSS() string {
	var w strings.Builder
	if b.canvas || b.fig {
		w.WriteString(figureCSS)
	}
	if b.canvas || b.imglink || b.fig {
		w.WriteString(imglinkCSS + galleryCSS) // wrapped gallery images still fill their column
	}
	if b.canvas || b.clink {
		w.WriteString(clinkCSS)
	}
	if b.lightbox && !b.canvas { // the canvas never builds the dialog
		w.WriteString(lightboxCSS)
	}
	return w.String()
}

// kindCSS is the CSS of the widget kinds present, in registry order. The
// canvas gets every kind: a patch may add a widget the page did not have.
func (b builderCSS) kindCSS() string {
	ws := widgets()
	var w strings.Builder
	for _, k := range ws.kinds {
		if b.canvas || b.kinds[k] {
			w.WriteString(ws.css[k])
		}
	}
	return w.String()
}

// compilePresets sets presetCSS from the site design: valid presets only, in
// name order, each once. A node naming a missing or invalid preset keeps its
// dead class and gets no rules.
func (b *builderCSS) compilePresets(d builder.Design) {
	names := make([]string, 0, len(d.Presets))
	for name := range d.Presets {
		if b.canvas || b.presets[name] {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	var w strings.Builder
	for _, name := range names {
		if p := d.Presets[name]; builder.ValidatePreset(name, p) == nil {
			w.WriteString(builder.PresetCSS(name, p, b.opts))
		}
	}
	b.presetCSS = w.String()
}

// islands is the script markup of the present kinds' islands. Never in the canvas.
func (b builderCSS) islands() template.HTML {
	if b.canvas || len(b.kinds) == 0 {
		return ""
	}
	return widgets().islandTags(b.kinds)
}

// pending reports whether the page needs the builder stylesheet at all: a doc
// of only v1-shaped nodes needs none (public output stays byte-identical to v1).
func (b builderCSS) pending() bool { return b.native || len(b.css) > 0 || b.canvas }

// marker is the substring that proves the theme printed head().
func (b builderCSS) marker() string {
	if b.canvas {
		return `id="hm-builder-base"`
	}
	return `id="hm-builder-css"`
}

// head returns the style markup: one <style> publicly; in the canvas the base
// sheet plus one tag per node so the editor can swap a node's rules on patch.
func (b builderCSS) head() template.HTML {
	if !b.pending() {
		return ""
	}
	ids := make([]string, 0, len(b.css))
	for id := range b.css {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var w strings.Builder
	if b.canvas {
		extra := ""
		if b.ref {
			extra += refCanvasCSS
		}
		if b.slot {
			extra += slotCanvasCSS
		}
		if b.missing {
			extra += missingCanvasCSS
		}
		w.WriteString(`<style id="hm-builder-base">` + b.tokens + BaseCSS + b.featureCSS() + b.kindCSS() + b.presetCSS + extra + `</style>`)
		for _, id := range ids {
			w.WriteString(`<style data-hm-css="` + html.EscapeString(id) + `">` + b.css[id] + `</style>`)
		}
	} else {
		w.WriteString(`<style id="hm-builder-css">` + b.tokens + BaseCSS + b.featureCSS() + b.kindCSS() + b.presetCSS)
		for _, id := range ids {
			w.WriteString(b.css[id])
		}
		w.WriteString(`</style>`)
	}
	return template.HTML(w.String()) // #nosec G203 -- CSS compiled from validated StyleSpec values, ids match ^[A-Za-z0-9_-]+$
}

// spliceHead inserts markup before the last </head>; output without one is unchanged.
func spliceHead(page []byte, tag template.HTML) []byte {
	i := bytes.LastIndex(page, []byte("</head>"))
	if i < 0 {
		return page
	}
	out := make([]byte, 0, len(page)+len(tag))
	out = append(out, page[:i]...)
	out = append(out, tag...)
	return append(out, page[i:]...)
}

// spliceBody inserts markup before the last </body>; output without one is unchanged.
func spliceBody(page []byte, tag template.HTML) []byte {
	i := bytes.LastIndex(page, []byte("</body>"))
	if i < 0 {
		return page
	}
	out := make([]byte, 0, len(page)+len(tag))
	out = append(out, page[:i]...)
	out = append(out, tag...)
	return append(out, page[i:]...)
}

func nodeHidden(n *builder.Node) bool {
	h, _ := n.P["hidden"].(bool)
	return h
}

// docBlocks turns the top-level nodes of v.Doc into the blocks the layout
// iterates. v1-shaped nodes go through the unchanged section path (byte-identical
// to v1); anything else is rendered natively and bypasses the section wrapper.
func (e *Engine) docBlocks(c *compiled, v *PageView) ([]preparedSection, builderCSS) {
	canvas := v.Canvas
	bc := newBuilderCSS(c, v, canvas)
	blocks := make([]preparedSection, 0, len(v.Doc.Nodes))
	st := &refState{}
	// Expand every ref first so one batched call serves the whole page,
	// blocks included.
	tops := make([]*builder.Node, len(v.Doc.Nodes))
	for i, top := range v.Doc.Nodes {
		if top != nil && !nodeHidden(top) {
			tops[i] = e.expandRefs(requestCtx(v), top, st)
		}
	}
	e.prefetchExt(v, tops...)
	e.prefetchImages(tops...)
	for i, top := range v.Doc.Nodes {
		if top == nil || nodeHidden(top) {
			continue
		}
		n := tops[i]
		bc.ref = bc.ref || (n != top && hasRefFailure(n))
		bc.slot = bc.slot || hasKind(n, "slot")
		if comp, ok := builder.V1Shaped(n); ok {
			blocks = append(blocks, prepareSections(c.theme, []Section{compSection(comp)}, canvas)...)
			continue
		}
		bc.native = true
		bc.collectKinds(n)
		builder.TreeCSSWith([]*builder.Node{n}, bc.css, bc.opts)
		var b bytes.Buffer
		e.renderNode(&b, c, v, n, canvas)
		if b.Len() == 0 { // renders nothing (a blank component): no sentinel comment, as RenderTop reports
			continue
		}
		blocks = append(blocks, preparedSection{
			Section: Section{ID: n.ID},
			Comment: nativeSentinel,
			Native:  template.HTML(b.String()), // #nosec G203 -- built from escaped values
		})
	}
	bc.compilePresets(v.Site.Design)
	return blocks, bc
}

func compSection(c *builder.Node) Section {
	content, _ := c.P["content"].(map[string]any)
	typ, _ := c.P["type"].(string)
	title, _ := c.P["title"].(string)
	return Section{ID: c.ID, Type: typ, Title: title, Content: content}
}

func str(p map[string]any, k string) string {
	s, _ := p[k].(string)
	return s
}

// imageSizes is the sizes attribute of an image that has resized variants: the
// page column is at most 1024px wide.
const imageSizes = "(max-width:1024px) 100vw, 1024px"

var containerTags = map[string]bool{"section": true, "div": true, "header": true, "footer": true, "main": true, "article": true, "aside": true, "nav": true}

// anchorSet tracks the anchors one page render has written, so an id is
// public once: page body, theme parts and expanded refs share it and the first
// rendered wins (render-call order). A nil set claims everything (a canvas
// patch renders one node alone).
type anchorSet struct{ seen map[string]bool }

func newAnchorSet() *anchorSet { return &anchorSet{seen: map[string]bool{}} }

// claim reports whether a may be written as an id: well formed, not reserved, not used yet.
func (s *anchorSet) claim(a string) bool {
	if !builder.ValidAnchor(a) {
		return false
	}
	if s == nil {
		return true
	}
	if s.seen[a] {
		return false
	}
	s.seen[a] = true
	return true
}

func (s *anchorSet) clone() *anchorSet {
	if s == nil {
		return nil
	}
	return &anchorSet{seen: maps.Clone(s.seen)}
}

// nodeAttrs writes the class (always), the element settings anchor (id),
// aria (aria-label) and tip (title) of kinds that take them, and the editor
// marker (canvas only). A nil v writes no element settings.
func nodeAttrs(b *bytes.Buffer, v *PageView, n *builder.Node, extra string, canvas bool) {
	preset := ""
	if p := builder.NodePreset(n); p != "" {
		preset = " hm-p-" + p
	}
	b.WriteString(`class="hm-n ` + extra + preset + ` hm-n-` + n.ID + `"`)
	if v != nil && builder.TakesElementProps(n.T) {
		if a := str(n.P, "anchor"); a != "" && !builder.PackOwns(n.T, "anchor") {
			if v.anchors.claim(a) {
				b.WriteString(` id="` + a + `"`) // ValidAnchor: [a-z0-9-]
			} else if canvas {
				b.WriteString(` data-hm-anchor-dropped="` + html.EscapeString(a) + `"`)
			}
		}
		if a := str(n.P, "aria"); a != "" && !builder.PackOwns(n.T, "aria") && builder.AriaAllowed(n) {
			b.WriteString(` aria-label="` + html.EscapeString(a) + `"`)
		}
		if t := str(n.P, "tip"); t != "" && !builder.PackOwns(n.T, "tip") {
			b.WriteString(` title="` + html.EscapeString(t) + `"`)
		}
	}
	if canvas {
		b.WriteString(" " + nodeMarker(n.ID) + " " + kindMarker(n.T))
		if builder.TakesChildren(n.T) {
			b.WriteString(" data-hm-holds") // can hold children: the canvas floors it while :empty, however it got empty (images, spacers keep their size)
		}
	}
}

// propAttr marks the text of n as inline editable; a bound text is not. A
// heading or text that carries inline markup is marked "html".
func propAttr(canvas bool, n *builder.Node) string {
	if !canvas || n.B["text"] != "" {
		return ""
	}
	if usesInlineHTML(n) {
		return ` data-hm-prop="html"`
	}
	return ` data-hm-prop="text"`
}

// usesInlineHTML reports whether a heading or text renders its html prop: it
// is set and the text is not bound (a bound text always wins).
func usesInlineHTML(n *builder.Node) bool {
	return (n.T == "heading" || n.T == "text") && n.B["text"] == "" && str(n.P, "html") != ""
}

// inlineBody is the inner markup of a heading or text: the html prop through
// the inline sanitiser (stored values are not trusted), else the escaped text.
func inlineBody(n *builder.Node) string {
	if usesInlineHTML(n) {
		return entity.SanitizeInlineHTML(str(n.P, "html"))
	}
	return html.EscapeString(str(n.P, "text"))
}

// renderNode writes the markup of one node subtree. Every prop is escaped; URLs
// are scheme-checked. A component renders its theme section template without
// the <section> wrapper; a failing one degrades to a SectionFailedMarker comment.
func (e *Engine) renderNode(b *bytes.Buffer, c *compiled, v *PageView, n *builder.Node, canvas bool) {
	e.renderNodeIn(b, c, v, n, nil, canvas)
}

// renderNodeIn is renderNode for a node whose parent is a widget (nil for any
// other parent); a widget prep can read it as widgetCtx.Parent.
func (e *Engine) renderNodeIn(b *bytes.Buffer, c *compiled, v *PageView, n, parent *builder.Node, canvas bool) {
	// RenderNode takes nodes that may not have been through Validate (canvas
	// patches); an id outside the grammar never reaches markup.
	if n == nil || nodeHidden(n) || !builder.ValidID(n.ID) {
		return
	}
	if len(n.B) > 0 {
		n = e.bound(v, n)
	}
	esc := html.EscapeString
	switch n.T {
	case "container":
		if str(n.P, "ref") != "" {
			e.renderRef(b, c, v, n, canvas)
			return
		}
		tag := str(n.P, "tag")
		if !containerTags[tag] {
			tag = "div"
		}
		extra := "hm-c"
		if str(n.P, "layout") == "grid" {
			extra += " hm-c--grid"
		}
		if str(n.P, "width") == "page" {
			extra += " hm-c--page"
		}
		link := ""
		if str(n.P, "href") != "" && builder.InteractiveIn(n) == nil {
			// A link wraps no interactive content (nested links and buttons are
			// invalid HTML); a subtree that has some (refs resolved here) renders a plain box.
			tag, extra = "a", extra+" hm-c--link"
			link = ` href="` + esc(safeURL(str(n.P, "href"))) + `"` + linkAttrs(n)
		}
		b.WriteString("<" + tag + " ")
		nodeAttrs(b, v, n, extra, canvas)
		b.WriteString(link + ">")
		for _, ch := range n.C {
			e.renderNode(b, c, v, ch, canvas)
		}
		b.WriteString("</" + tag + ">")
	case "heading":
		lv := 2
		switch f := n.P["level"].(type) {
		case float64:
			if f >= 1 && f <= 6 {
				lv = int(f)
			}
		case int:
			if f >= 1 && f <= 6 {
				lv = f
			}
		}
		h := "h" + strconv.Itoa(lv)
		b.WriteString("<" + h + " ")
		nodeAttrs(b, v, n, "hm-w-h", canvas)
		b.WriteString(propAttr(canvas, n) + ">" + inlineBody(n) + "</" + h + ">")
	case "text":
		b.WriteString("<p ")
		nodeAttrs(b, v, n, "hm-w-text", canvas)
		b.WriteString(propAttr(canvas, n) + ">" + inlineBody(n) + "</p>")
	case "button":
		b.WriteString("<a ")
		nodeAttrs(b, v, n, "hm-btn hm-w-btn", canvas)
		b.WriteString(` href="` + esc(safeURL(str(n.P, "href"))) + `"` + linkAttrs(n) + propAttr(canvas, n) + ">" + esc(str(n.P, "text")) + "</a>")
	case "image":
		e.renderImage(b, v, n, parent, canvas)
	case "spacer":
		b.WriteString("<div ")
		nodeAttrs(b, v, n, "hm-w-spacer", canvas)
		b.WriteString(` aria-hidden="true"></div>`)
	case "divider":
		b.WriteString("<hr ")
		nodeAttrs(b, v, n, "hm-w-divider", canvas)
		b.WriteString(">")
	case "component":
		e.renderComponent(b, c, v, n, canvas)
	case "slot":
		if !canvas {
			b.WriteString(slotMarker)
			break
		}
		label := "Content slot"
		if n.P["replace"] == true {
			label = "Content slot (replaces theme content)"
		}
		b.WriteString(`<div class="hm-n hm-slot hm-n-` + n.ID + `" ` + nodeMarker(n.ID) + " " + kindMarker("slot") + ">" + esc(label) + "</div>")
	default:
		e.renderWidget(b, c, v, n, parent, canvas)
	}
}

// imageSize is the intrinsic size of a local image from the data source (0, 0
// when it has none or does not know the file).
func (e *Engine) imageSize(src string) (w, h int) {
	s, ok := e.opts.Data.(ImageSizer)
	if !ok {
		return 0, 0
	}
	src = imgSrc(src)
	if !strings.HasPrefix(src, "/") && !strings.HasPrefix(src, "http") {
		return 0, 0
	}
	w, h, ok = s.ImageSize(src)
	if !ok || w <= 0 || h <= 0 {
		return 0, 0
	}
	return w, h
}

// prefetchImages loads the media rows of the literal image sources of ns in
// one lookup (it also warms the size cache), so each image does not query alone.
func (e *Engine) prefetchImages(ns ...*builder.Node) {
	p, ok := e.opts.Data.(VariantPrefetcher)
	if !ok {
		return
	}
	var urls []string
	var walk func(n *builder.Node)
	walk = func(n *builder.Node) {
		if n == nil || nodeHidden(n) {
			return
		}
		if n.T == "image" && n.B["src"] == "" {
			if u := imgSrc(str(n.P, "src")); strings.HasPrefix(u, "/") || strings.HasPrefix(u, "http") {
				urls = append(urls, u)
			}
		}
		for _, c := range n.C {
			walk(c)
		}
	}
	for _, n := range ns {
		walk(n)
	}
	if len(urls) > 0 {
		p.PrefetchVariants(urls)
	}
}

// linkAttrs is the target and rel of a link node: target only for _blank; rel
// from the chosen nofollow/sponsored/ugc tokens (deduped, fixed order) plus
// noopener noreferrer with _blank. Call it only for a node that has a link.
func linkAttrs(n *builder.Node) string {
	blank := str(n.P, "target") == "_blank"
	chosen := map[string]bool{}
	for _, t := range strings.Fields(str(n.P, "rel")) {
		if t == "nofollow" || t == "sponsored" || t == "ugc" {
			chosen[t] = true
		}
	}
	if blank {
		chosen["noopener"], chosen["noreferrer"] = true, true
	}
	var rel []string
	for _, t := range [...]string{"nofollow", "sponsored", "ugc", "noopener", "noreferrer"} {
		if chosen[t] {
			rel = append(rel, t)
		}
	}
	out := ""
	if blank {
		out = ` target="_blank"`
	}
	if len(rel) > 0 {
		out += ` rel="` + strings.Join(rel, " ") + `"`
	}
	return out
}

// without returns n with the props dropped (n itself when it has none of them).
func without(n *builder.Node, keys ...string) *builder.Node {
	has := false
	for _, k := range keys {
		_, ok := n.P[k]
		has = has || ok
	}
	if !has {
		return n
	}
	cp := *n
	cp.P = maps.Clone(n.P)
	for _, k := range keys {
		delete(cp.P, k)
	}
	return &cp
}

// renderImage writes an image. Without caption, link or lightbox it is the
// bare <img> (aria is not written there: the alt text is its name). Otherwise
// the outermost element (<figure> with a caption, else the <a>) carries the
// node's class, id, title and editor markers, and the inner <img> is plain.
func (e *Engine) renderImage(b *bytes.Buffer, v *PageView, n, parent *builder.Node, canvas bool) {
	esc := html.EscapeString
	src := str(n.P, "src")
	alt := str(n.P, "alt")
	if n.P["decorative"] == true {
		alt = ""
	}
	href, caption := str(n.P, "href"), str(n.P, "caption")
	lightbox := builder.LightboxSrc(src) && href == "" && (n.P["lightbox"] == true || (parent != nil && parent.T == "gallery" && parent.P["lightbox"] == true))
	w, h := e.imageSize(src)
	// img writes the <img>; open writes its class (and, bare, the node's attributes).
	img := func(open func()) {
		b.WriteString("<img ")
		open()
		b.WriteString(` src="` + esc(safeURL(src)) + `" alt="` + esc(alt) + `"`)
		if w > 0 {
			b.WriteString(` width="` + strconv.Itoa(w) + `" height="` + strconv.Itoa(h) + `"`)
		}
		if ss := srcset(e.opts.Data, src); ss != "" {
			b.WriteString(` srcset="` + esc(ss) + `" sizes="` + imageSizes + `"`)
		}
		b.WriteString(` loading="lazy" decoding="async">`)
	}
	plain := func() { b.WriteString(`class="hm-w-img"`) }
	if href == "" && !lightbox && caption == "" {
		img(func() { nodeAttrs(b, v, without(n, "aria"), "hm-w-img", canvas) })
		return
	}
	linkTarget := ""
	switch {
	case href != "":
		linkTarget = ` href="` + esc(safeURL(href)) + `"` + linkAttrs(n)
	case lightbox:
		linkTarget = ` href="` + esc(safeURL(src)) + `" data-hm-lightbox`
	}
	if caption == "" { // the link is the outermost element and takes the whole node's attributes
		b.WriteString("<a ")
		nodeAttrs(b, v, n, "hm-w-imglink", canvas)
		b.WriteString(linkTarget + ">")
		img(plain)
		b.WriteString("</a>")
		return
	}
	// Figure: the accessible name of the link inside is aria, else the caption
	// when the image has no alt text of its own.
	name := str(n.P, "aria")
	if name == "" && alt == "" {
		name = caption
	}
	b.WriteString("<figure ")
	if linkTarget != "" {
		nodeAttrs(b, v, without(n, "aria"), "hm-w-fig", canvas)
	} else {
		nodeAttrs(b, v, n, "hm-w-fig", canvas)
	}
	b.WriteString(">")
	if linkTarget != "" {
		b.WriteString(`<a class="hm-w-imglink"` + linkTarget)
		if name != "" {
			b.WriteString(` aria-label="` + esc(name) + `"`)
		}
		b.WriteString(">")
	}
	img(plain)
	if linkTarget != "" {
		b.WriteString("</a>")
	}
	b.WriteString(`<figcaption class="hm-w-cap">` + esc(caption) + "</figcaption></figure>")
}

// renderComponent runs the theme section template for a component node,
// without the section wrapper. Canvas output gets a display:contents element
// carrying the section and node markers so the editor can address it.
func (e *Engine) renderComponent(b *bytes.Buffer, c *compiled, v *PageView, n *builder.Node, canvas bool) {
	s := compSection(n)
	p, ok := prepareBlock(c, v, s, canvas, *e.settings.Load())
	if !ok {
		return
	}
	if p.Comment != "" { // unknown type
		b.WriteString(string(p.CommentHTML()))
		return
	}
	tmpl, ok := c.pick("page")
	if !ok {
		return
	}
	var out bytes.Buffer
	if err := execDispatch(tmpl, &out, p); err != nil {
		e.log.Error("render: section failed, rendered empty", "type", p.Type, "id", p.ID, "error", err)
		b.WriteString("<!-- section render failed: " + commentSafe(p.Type) + " -->")
		return
	}
	if canvas {
		b.WriteString(`<div style="display:contents" ` + nodeMarker(n.ID) + " " + kindMarker(p.Type) + " " + string(sectionMarker(p)) + ">")
		b.Write(out.Bytes())
		b.WriteString("</div>")
		return
	}
	b.Write(out.Bytes())
}

// execDispatch executes section-dispatch, turning a template panic into an error.
func execDispatch(t *template.Template, w *bytes.Buffer, p preparedSection) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("section panic: %v", r)
		}
	}()
	return t.ExecuteTemplate(w, "section-dispatch", p)
}
