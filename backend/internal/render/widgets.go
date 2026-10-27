package render

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"maps"
	"sort"
	"strings"
	"sync"

	"hatamoto.dev/core/internal/builder"
)

// widgetFS holds the view side of every widget kind:
// widgets/<kind>/{widget.html,widget.css,island.js?}. The schema side lives in
// builder/widgets/<kind>/schema.json; a test keeps the two kind sets equal.
//
//go:embed widgets
var widgetFS embed.FS

// widgetData is the dot of a widget template. One widget is one element:
// the template writes its single root with {{.Attrs}} on it.
type widgetData struct {
	// Attrs is class="hm-n <Class> hm-n-<id>" plus the canvas markers.
	Attrs template.HTMLAttr
	// P are the node's props as stored, X what the kind's prep function made of them.
	P        map[string]any
	Children template.HTML
	Canvas   bool
	X        any
}

// widgetCtx is what a prep function sees of the node being rendered.
type widgetCtx struct {
	E    *Engine
	View *PageView
	N    *builder.Node
	// C is the compiled theme, for a prep that renders nodes itself.
	C *compiled
	// OwnChildren makes renderWidget leave the children to the prep (a loop
	// renders them once per item); widgetData.Children stays empty.
	OwnChildren bool
	// Parent is the widget this node is a direct child of, nil otherwise.
	Parent *builder.Node
	P      map[string]any
	Canvas bool
	// Class is the kind class of the root element (hm-<kind>); a prep may
	// append modifier classes (hm-cols--3).
	Class string
}

// errWidgetEmpty is what a prep returns when its widget has nothing to show
// publicly (an unknown form, an empty menu): the node renders no markup.
var errWidgetEmpty = errors.New("widget renders nothing")

// widgetPrep turns a node's props into the template data X. It runs on every
// render, so anything user supplied (rich text, URLs) is checked here.
type widgetPrep func(w *widgetCtx) (any, error)

var (
	widgetPreps  = map[string]widgetPrep{}
	widgetPrepMu sync.Mutex
)

// registerWidget attaches the prep function of a kind; call it from init() of
// widget_<kind>.go. A kind without one renders with X nil.
func registerWidget(kind string, prep widgetPrep) {
	widgetPrepMu.Lock()
	defer widgetPrepMu.Unlock()
	widgetPreps[kind] = prep
}

// widgetSet is the loaded view side of all widgets.
type widgetSet struct {
	kinds  []string // registry order: the order builder.Kinds lists them
	tmpl   map[string]*template.Template
	prep   map[string]widgetPrep
	css    map[string]string // minified widget.css per kind
	island map[string]widgetIsland
}

var (
	defaultWidgets     *widgetSet
	defaultWidgetsOnce sync.Once
)

// widgets returns the current set: the embedded widgets plus the plugin
// widgets of SetPackWidgets. The embedded set is loaded on first use; a broken
// embedded widget is a build defect and panics, like a bad theme layout at init.
func widgets() *widgetSet {
	if ws := packSet.Load(); ws != nil {
		return ws
	}
	return embeddedWidgets()
}

func embeddedWidgets() *widgetSet {
	defaultWidgetsOnce.Do(func() {
		widgetPrepMu.Lock()
		defer widgetPrepMu.Unlock()
		ws, err := loadWidgetSet(widgetFS, widgetPreps)
		if err != nil {
			panic("render: " + err.Error())
		}
		// The lightbox belongs to no widget kind (image and gallery are native):
		// its island is served and gated by the synthetic key lightboxKind.
		ws.island[lightboxKind] = newWidgetIsland(lightboxKind, lightboxJS)
		defaultWidgets = ws
	})
	return defaultWidgets
}

// loadWidgetSet reads widgets/<kind>/ of fsys. Kinds are ordered as
// builder.Kinds orders them, then alphabetically for kinds it does not know.
func loadWidgetSet(fsys fs.FS, preps map[string]widgetPrep) (*widgetSet, error) {
	dirs, err := fs.ReadDir(fsys, "widgets")
	if err != nil {
		return nil, err
	}
	ws := &widgetSet{
		tmpl: map[string]*template.Template{}, prep: map[string]widgetPrep{},
		css: map[string]string{}, island: map[string]widgetIsland{},
	}
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		kind := d.Name()
		src, err := fs.ReadFile(fsys, "widgets/"+kind+"/widget.html")
		if err != nil {
			return nil, fmt.Errorf("widget %q: %w", kind, err)
		}
		t, err := template.New("w-" + kind).Parse(strings.TrimSpace(string(src)))
		if err != nil {
			return nil, fmt.Errorf("widget %q: %w", kind, err)
		}
		css, err := fs.ReadFile(fsys, "widgets/"+kind+"/widget.css")
		if err != nil {
			return nil, fmt.Errorf("widget %q: %w", kind, err)
		}
		ws.tmpl[kind], ws.css[kind], ws.prep[kind] = t, minifyCSS(string(css)), preps[kind]
		if js, err := fs.ReadFile(fsys, "widgets/"+kind+"/island.js"); err == nil {
			ws.island[kind] = newWidgetIsland(kind, js)
		}
		ws.kinds = append(ws.kinds, kind)
	}
	rank := map[string]int{}
	for i, k := range builder.Kinds() {
		rank[k.Kind] = i + 1
	}
	sort.Slice(ws.kinds, func(i, j int) bool {
		a, b := ws.kinds[i], ws.kinds[j]
		if rank[a] != rank[b] {
			return rank[a] < rank[b]
		}
		return a < b
	})
	return ws, nil
}

// renderWidget writes one widget node (and its children). It reports false
// when n is not a widget kind, so renderNode can leave unknown kinds blank.
func (e *Engine) renderWidget(b *bytes.Buffer, c *compiled, v *PageView, n, parent *builder.Node, canvas bool) bool {
	ws := widgets()
	t, ok := ws.tmpl[n.T]
	if !ok {
		if !builder.IsPackKind(n.T) {
			return false
		}
		writeMissingWidget(b, n, canvas)
		return true
	}
	ctx := &widgetCtx{E: e, View: v, N: n, C: c, Parent: parent, P: n.P, Canvas: canvas, Class: "hm-" + n.T}
	var x any
	if prep := ws.prep[n.T]; prep != nil {
		var err error
		if x, err = prep(ctx); errors.Is(err, errWidgetEmpty) {
			return true
		} else if err != nil {
			e.log.Error("render: widget prep failed, rendered empty", "kind", n.T, "id", n.ID, "error", err)
			return true
		}
	}
	d := widgetData{P: n.P, Canvas: canvas, X: x}
	if builder.IsPackKind(n.T) {
		// The editor's private name and the element settings (written by the
		// core as attributes) never reach a plugin template (it may range over .P).
		d.P = maps.Clone(n.P)
		for _, k := range builder.UniversalKeys[1:] {
			if !builder.PackOwns(n.T, k) {
				delete(d.P, k)
			}
		}
	}
	if len(n.C) > 0 && !ctx.OwnChildren {
		var cb bytes.Buffer
		for _, ch := range n.C {
			e.renderNodeIn(&cb, c, v, ch, n, canvas)
		}
		d.Children = template.HTML(cb.String()) // #nosec G203 -- markup of renderNode, escaped there
	}
	var ab bytes.Buffer
	nodeAttrs(&ab, v, n, ctx.Class, canvas)
	d.Attrs = template.HTMLAttr(ab.String()) // #nosec G203 -- class from the registry, id matches ^[A-Za-z0-9_-]+$
	pack := builder.IsPackKind(n.T)
	childHTML, attrHTML := string(d.Children), string(d.Attrs)
	if pack {
		// The plugin's output is sanitised; the core's attributes and the
		// rendered children stay out of it as placeholders.
		d.Attrs = template.HTMLAttr(packAttrsHolder) // #nosec G203 -- constant
		if len(d.Children) > 0 {
			d.Children = template.HTML(packChildrenHolder) // #nosec G203 -- constant
		}
	}
	// Execute into a scratch buffer: a failed or runaway template must not
	// leave half an attribute in the page. Plugin templates also run under a
	// size cap, since they have no fuel like wasm does.
	var sb bytes.Buffer
	var w io.Writer = &sb
	if pack {
		w = &cappedWriter{buf: &sb, max: maxPackOutput}
	}
	if err := t.Execute(w, d); err != nil {
		e.log.Error("render: widget failed", "kind", n.T, "id", n.ID, "error", err)
		if pack {
			NoteDegradedCtx(requestCtx(v))
		}
		writeFailedWidget(b, n, canvas)
		return true
	}
	if pack {
		out, changed, err := sanitizePackOutput(sb.Bytes(), attrHTML, childHTML)
		if err != nil {
			e.log.Error("render: widget failed", "kind", n.T, "id", n.ID, "error", err)
			NoteDegradedCtx(requestCtx(v))
			writeFailedWidget(b, n, canvas)
			return true
		}
		if changed && packSanitizeLogDue(n.T) {
			e.log.Warn("render: widget output was changed by the sanitiser; the template writes markup the validator should have refused", "kind", n.T, "id", n.ID)
		}
		b.Write(out)
		return true
	}
	b.Write(sb.Bytes())
	return true
}

// maxPackOutput caps the markup one plugin widget may write (its own, without children).
const maxPackOutput = 256 << 10

var errPackOutput = errors.New("widget output exceeds the size cap")

// cappedWriter fails a Write that would grow buf past max.
type cappedWriter struct {
	buf *bytes.Buffer
	max int
}

func (w *cappedWriter) Write(p []byte) (int, error) {
	if w.buf.Len()+len(p) > w.max {
		return 0, errPackOutput
	}
	return w.buf.Write(p)
}

// intProp reads an integer prop (float64 from JSON, int when built in code);
// anything missing or outside lo..hi gives def.
func intProp(p map[string]any, k string, def, lo, hi int) int {
	var n int
	switch f := p[k].(type) {
	case float64:
		n = int(f)
	case int:
		n = f
	default:
		return def
	}
	if n < lo || n > hi {
		return def
	}
	return n
}
