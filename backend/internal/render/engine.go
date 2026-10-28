package render

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log/slog"
	"maps"
	"net/http"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"hatamoto.dev/core/internal/builder"
)

// Options configures an Engine.
type Options struct {
	// Layers are theme file systems layered over the embedded default, highest
	// precedence first. The server passes the active theme package (loaded from
	// Postgres); none means the built-in default theme alone.
	Layers []fs.FS
	// Settings are the active theme's settings values, exposed to templates as
	// .Settings (see SetSettings).
	Settings map[string]any
	// HeadFilter optionally lets plugins add head tags (fail-open).
	HeadFilter HeadFilter
	// Data feeds the recent, terms, menu and srcset template funcs (optional).
	Data DataSource
	// ThemeName names the theme in boot fault identifiers (see SetThemeName).
	ThemeName string
	// Refs resolves ref nodes (synced blocks) and theme parts. Without it a ref
	// renders as a missing block.
	Refs RefSource
	// ExtSources resolves plugin data sources for ext.* bindings and source
	// loops, one batch per render (nil: no external data).
	ExtSources ExtSourceFunc
	// Logger receives rebuild and plugin partial diagnostics (default slog.Default()).
	Logger *slog.Logger
}

// requiredTemplates must be defined by the theme's partials.
var requiredTemplates = []string{"head", "header", "nav", "footer", "section", "scripts"}

// fixedLayouts are the layouts a theme may ship; the embedded default ships
// all of them, so the lookup always ends somewhere.
var fixedLayouts = []string{"page", "single", "home", "archive", "taxonomy", "author", "search", "registry", "404", "error"}

// layoutPrefixes name the family layouts: single-<schema>, archive-<schema>
// and taxonomy-<kind>.
var layoutPrefixes = []string{"single-", "archive-", "taxonomy-"}

// schemaLayoutRe is the shape of a schema slug or term kind in a layout name.
var schemaLayoutRe = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

// layoutKey identifies one layout file: the chain layer (0 wins) it lives in
// and its name without "layouts/" and ".html".
type layoutKey struct {
	layer int
	name  string
}

// isLayoutName reports whether name is a layout the engine loads.
func isLayoutName(name string) bool {
	if slices.Contains(fixedLayouts, name) {
		return true
	}
	for _, p := range layoutPrefixes {
		if rest, ok := strings.CutPrefix(name, p); ok && schemaLayoutRe.MatchString(rest) {
			return true
		}
	}
	return false
}

// layoutCandidates is the template hierarchy for a view, most specific first:
//
//	single:   single-<schema> -> single -> page
//	home:     home -> page
//	archive:  archive-<schema> -> archive -> page
//	taxonomy: taxonomy-<kind> -> taxonomy -> page
//	author:   author -> page
//	search:   search -> page
//	registry: registry -> page
//
// schema is the schema slug (single, archive) or the term kind (taxonomy).
func layoutCandidates(kind, schema string) []string {
	var out []string
	specific := func(prefix string) {
		if schemaLayoutRe.MatchString(schema) {
			out = append(out, prefix+schema)
		}
	}
	switch kind {
	case KindSingle:
		specific("single-")
		out = append(out, "single")
	case KindHome:
		out = append(out, "home")
	case KindArchive:
		specific("archive-")
		out = append(out, "archive")
	case KindTaxonomy:
		specific("taxonomy-")
		out = append(out, "taxonomy")
	case KindAuthor, KindSearch:
		out = append(out, kind)
	case KindRegistry:
		// No generic page fallback: a theme's own page.html knows nothing of
		// .Registry and would win the layer-first pick over the embedded
		// default registry layout, which every chain ends with.
		return []string{kind}
	}
	return append(out, "page")
}

var sectionTypeRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// Engine renders pages from a layered theme. It is safe for concurrent use.
type Engine struct {
	opts     Options
	log      *slog.Logger
	plugins  *pluginRegistry
	settings atomic.Pointer[map[string]any]
	// assetOverride (optional) lets a request be served another engine's theme
	// assets (theme preview).
	assetOverride atomic.Pointer[func(*http.Request) http.Handler]

	// partWarned dedupes the log lines of skipped theme parts.
	partWarned sync.Map

	mu    sync.RWMutex
	chain Chain // theme layers, then the embedded default; guarded by mu
	cur   *compiled
	// failedGen is the plugin generation a rebuild already failed for: the
	// previous version keeps serving and nothing recompiles per request until
	// the registry or the theme changes again.
	failedGen int
	failed    bool

	themeName atomic.Value // string: the theme being applied, for boot fault ids
}

// SetThemeName names the theme the next SetTheme applies, so a fault it
// causes has a stable identifier.
func (e *Engine) SetThemeName(name string) { e.themeName.Store(name) }

// faultTheme is the theme name boot fault identifiers carry.
func (e *Engine) faultTheme() string {
	if n, _ := e.themeName.Load().(string); n != "" {
		return n
	}
	return "custom"
}

// compiled is one immutable parsed template set for a theme version.
type compiled struct {
	theme      *Theme
	version    string
	pluginSize int
	layers     int // chain length
	layouts    map[layoutKey]*template.Template
	assets     http.Handler
	cssName    string
	hasJS      bool
	// contentSlots maps a single*/archive* layout (a clone of the base set) to
	// the part slot wrapped around its content; empty when the theme did not
	// opt in.
	contentSlots map[*template.Template]string
}

// New builds an Engine with chain [o.Layers..., embedded default].
func New(o Options) (*Engine, error) { return newEngine(o, defaultPlugins) }

func newEngine(o Options, p *pluginRegistry) (*Engine, error) {
	e := &Engine{opts: o, plugins: p, log: o.Logger}
	if e.log == nil {
		e.log = slog.Default()
	}
	e.SetThemeName(o.ThemeName)
	e.chain = layered(o.Layers)
	e.SetSettings(o.Settings)

	c, err := e.compile(e.chain)
	if err != nil {
		return nil, err
	}
	e.cur = c
	return e, nil
}

// layered is [layers..., embedded default].
func layered(layers []fs.FS) Chain {
	c := make(Chain, 0, len(layers)+1)
	c = append(c, layers...)
	return append(c, DefaultFS())
}

// SetTheme switches the engine to a new set of theme layers without a restart.
// A theme that does not compile is refused and the current one keeps serving.
// A theme that compiles but cannot render is accepted like at boot (see
// compile); use CheckTheme first to learn about that.
func (e *Engine) SetTheme(layers ...fs.FS) error {
	chain := layered(layers)
	e.mu.Lock()
	defer e.mu.Unlock()
	c, err := e.compile(chain)
	if err != nil {
		return err
	}
	e.chain, e.cur, e.failed = chain, c, false
	return nil
}

// CheckTheme reports why layers could not serve pages: a template that does
// not parse, a missing required template, or one that fails against a fixture
// page of every layout kind. It changes nothing.
func (e *Engine) CheckTheme(layers ...fs.FS) error {
	chain := layered(layers)
	if _, err := e.compileChain(chain, e.opts.Data); err != nil {
		return err
	}
	if len(layers) > 0 {
		return e.smokeRender(chain)
	}
	return nil
}

// SetSettings replaces the theme settings templates read as .Settings.
func (e *Engine) SetSettings(m map[string]any) {
	if m == nil {
		m = map[string]any{}
	}
	e.settings.Store(&m)
}

// SectionTypes returns the section types a theme made of layers would render:
// its own, the built-in default's and installed plugins', sorted.
func (e *Engine) SectionTypes(layers ...fs.FS) ([]string, error) {
	th, err := LoadTheme(layered(layers))
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for t := range th.Sections {
		seen[t] = true
	}
	for t := range e.plugins.gated().sections {
		seen[t] = true
	}
	return sortedKeysBool(seen), nil
}

func sortedKeysBool(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// current returns the compiled set, rebuilding when the plugin registry
// changed.
func (e *Engine) current() *compiled {
	e.mu.RLock()
	c, failed, failedGen := e.cur, e.failed, e.failedGen
	e.mu.RUnlock()

	gen := e.plugins.size()
	if gen == c.pluginSize || (failed && failedGen == gen) {
		return c
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cur != c { // someone else rebuilt
		return e.cur
	}
	next, err := e.compile(e.chain)
	if err != nil {
		e.log.Error("render: theme rebuild failed, keeping previous version", "error", err)
		NoteBootFault("theme-rebuild-failed:" + e.faultTheme())
		e.failed, e.failedGen = true, gen
		return c
	}
	e.failed = false
	e.cur = next
	return next
}

// compile builds the template set for the engine's chain. A site or disk theme
// that loads but cannot render (see smokeRender) is logged and replaced by the
// embedded default templates, so a broken override never turns into a 500 on
// every page. The compiled version stays the chain's hash.
func (e *Engine) compile(chain Chain) (*compiled, error) {
	c, err := e.compileChain(chain, e.opts.Data)
	if err != nil { // structural errors (parse, missing templates) reject the theme
		return nil, err
	}
	if len(chain) > 1 {
		if err := e.smokeRender(chain); err != nil {
			e.log.Error("render: theme templates failed validation, falling back to the default theme", "error", err)
			NoteBootFault("theme-fallback:" + e.faultTheme())
			d, derr := e.compileChain(Chain{DefaultFS()}, e.opts.Data)
			if derr != nil {
				return nil, derr
			}
			d.version = chainHash(chain)
			return d, nil
		}
	}
	return c, nil
}

func (e *Engine) compileChain(chain Chain, ds DataSource) (*compiled, error) {
	th, err := LoadTheme(chain)
	if err != nil {
		return nil, err
	}
	funcs := baseFuncs()
	maps.Copy(funcs, dataFuncs(ds))
	size := e.plugins.size() // before the snapshot: a later grant change rebuilds
	snap := e.plugins.gated()
	maps.Copy(funcs, snap.funcs)

	baseSrc, err := chain.ReadFile("layouts/base.html")
	if err != nil {
		return nil, fmt.Errorf("theme has no layouts/base.html: %w", err)
	}
	root, err := template.New("base").Funcs(funcs).Parse(string(baseSrc))
	if err != nil {
		return nil, fmt.Errorf("layouts/base.html: %w", err)
	}

	partials, err := chain.Glob("partials/*.html")
	if err != nil {
		return nil, err
	}
	for _, p := range partials {
		b, err := chain.ReadFile(p)
		if err != nil {
			return nil, err
		}
		if _, err := root.New(p).Parse(string(b)); err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
	}
	// Plugin templates are vetted in scratch sets first (they may only define
	// names of their own) and only the vetted ones join the theme's set, so a
	// plugin can neither replace a theme template nor stop the theme compiling.
	pPartials, pSections := snap.vet(th.Sections, e.log)
	names := make([]string, 0, len(pPartials))
	for n := range pPartials {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if _, err := root.New(n).Parse(pPartials[n]); err != nil {
			e.log.Warn("render: skipping plugin partial", "partial", n, "error", err) // fail-open
		}
	}

	// Plugin section packs sit below the theme: a theme section of the same
	// type wins, the rest are added (vet already skipped the former).
	for typ, src := range pSections {
		th.Sections[typ] = &SectionDef{Type: typ, Template: src}
	}

	var dispatch strings.Builder
	dispatch.WriteString(`{{define "section-dispatch"}}`)
	types := make([]string, 0, len(th.Sections))
	for typ := range th.Sections {
		types = append(types, typ)
	}
	sort.Strings(types)
	first := true
	for _, typ := range types {
		if !sectionTypeRe.MatchString(typ) {
			return nil, fmt.Errorf("invalid section type name %q (want [a-z][a-z0-9_]*)", typ)
		}
		_, fromPlugin := pSections[typ]
		if _, err := root.New("sections/" + typ + ".html").Parse(th.Sections[typ].Template); err != nil {
			if fromPlugin { // fail-open: a plugin section never breaks the theme
				e.log.Warn("render: skipping plugin section", "type", typ, "error", err)
				continue
			}
			return nil, fmt.Errorf("sections/%s.html: %w", typ, err)
		}
		if root.Lookup("section-"+typ) == nil {
			if fromPlugin {
				e.log.Warn("render: skipping plugin section", "type", typ, "error", "does not define its section template")
				continue
			}
			return nil, fmt.Errorf("sections/%s.html must define \"section-%s\"", typ, typ)
		}
		if fromPlugin {
			if err := escapeProbe(root, typ); err != nil { // fail-open, as for a parse error
				e.log.Warn("render: skipping plugin section", "type", typ, "error", err)
				continue
			}
		}
		kw := "else if"
		if first {
			kw, first = "if", false
		}
		fmt.Fprintf(&dispatch, `{{%s eq .Type %q}}{{template "section-%s" .}}`, kw, typ, typ)
	}
	if !first {
		dispatch.WriteString(`{{end}}`)
	}
	dispatch.WriteString(`{{end}}`)
	if _, err := root.New("section-dispatch").Parse(dispatch.String()); err != nil {
		return nil, fmt.Errorf("section dispatch: %w", err)
	}

	for _, n := range requiredTemplates {
		if root.Lookup(n) == nil {
			return nil, fmt.Errorf("theme must define template %q (usually in partials/%s.html)", n, n)
		}
	}
	// Opted-in header and footer slots: wrap before the layouts are cloned.
	for slot, wrapper := range map[string]string{builder.SlotHeader: headerWrapper, builder.SlotFooter: footerWrapper} {
		if th.BuilderParts[slot] {
			if err := wrapPartial(root, slot, wrapper); err != nil {
				return nil, err
			}
		}
	}

	c := &compiled{
		theme:      th,
		version:    chainHash(chain),
		pluginSize: size,
		layers:     len(chain),
		layouts:    map[layoutKey]*template.Template{},
	}
	if len(th.BuilderParts) > 0 {
		c.contentSlots = map[*template.Template]string{}
	}
	// Every layer's layouts are parsed, not only the winning copy of each name:
	// the hierarchy falls through a theme's own files before the default's.
	for layer, fsys := range chain {
		files, err := fs.Glob(fsys, "layouts/*.html")
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			name := strings.TrimSuffix(path.Base(f), ".html")
			if !isLayoutName(name) {
				if name == "base" {
					continue
				}
				e.log.Warn("render: layout file is not read; rename it (single-<schema>.html, archive-<schema>.html, taxonomy-<kind>.html or a fixed layout name)", "file", f)
				continue
			}
			src, err := fs.ReadFile(fsys, f)
			if err != nil {
				return nil, err
			}
			clone, err := root.Clone()
			if err != nil {
				return nil, err
			}
			if _, err := clone.New("layouts/" + name + ".html").Parse(string(src)); err != nil {
				return nil, fmt.Errorf("layouts/%s.html: %w", name, err)
			}
			if clone.Lookup("content") == nil {
				return nil, fmt.Errorf("layouts/%s.html must define \"content\"", name)
			}
			if slot := contentSlot(name); slot != "" && th.BuilderParts[slot] {
				if err := wrapPartial(clone, "content", contentWrapper); err != nil {
					return nil, fmt.Errorf("layouts/%s.html: %w", name, err)
				}
				c.contentSlots[clone] = slot
			}
			c.layouts[layoutKey{layer, name}] = clone
		}
	}
	for _, name := range []string{"page", "404", "error"} {
		if _, ok := c.pick(name); !ok {
			return nil, fmt.Errorf("theme has no layouts/%s.html", name)
		}
	}

	_, js, cssName := buildBundle(th)
	c.cssName, c.hasJS = cssName, len(js) > 0
	c.assets = th.AssetHandler()
	return c, nil
}

// pageData is the dot of every layout: the PageView plus derived render data.
type pageData struct {
	PageView
	Head headView
	// Blocks are the prepared (ordered, visible, sanitised) sections.
	Blocks []preparedSection
	// Body is attributes.content sanitised with the rich-text policy at render
	// time; empty when the entity has none.
	Body template.HTML

	// PluginHead and PluginBody are the plugin public-asset slots: markup the
	// host inserts just before </head> and </body> through the "plugin-head" and
	// "plugin-body" partials. They carry the stylesheet and script links of
	// installed plugin packages (their assets/plugin.css and assets/plugin.js).
	PluginHead, PluginBody template.HTML

	// Settings are the active theme's settings (its settings_schema values,
	// defaults merged), read-only.
	Settings map[string]any

	// Part is the markup of the active theme parts; zero unless the theme opted
	// in and the owner assigned a part (see parts.go).
	Part partOut

	CSSHref    string
	JSHref     string
	Tokens     cssTokens
	Status     int
	StatusText string
}

// pick returns the layout for the first candidate any layer has, trying every
// candidate in the highest-precedence layer before the next layer.
func (c *compiled) pick(candidates ...string) (*template.Template, bool) {
	for layer := range c.layers {
		for _, name := range candidates {
			if t, ok := c.layouts[layoutKey{layer, name}]; ok {
				return t, true
			}
		}
	}
	return nil, false
}

// escapeProbe runs html/template's contextual escaping on the section alone, in
// a throwaway clone, without executing anything. A section the escaper refuses
// (say, a tag left open) must not reach the shared dispatch: a failed escape
// leaves the template half rewritten and a later Execute of it can panic.
func escapeProbe(root *template.Template, typ string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("escape panic: %v", r)
		}
	}()
	probe, err := root.Clone()
	if err != nil {
		return err
	}
	if _, err := probe.New("escape-probe").Parse(fmt.Sprintf(`{{if false}}{{template "section-%s" .}}{{end}}`, typ)); err != nil {
		return err
	}
	var te *template.Error
	if err := probe.ExecuteTemplate(io.Discard, "escape-probe", nil); errors.As(err, &te) {
		return err
	}
	return nil
}

func (e *Engine) execute(w io.Writer, candidates []string, v *PageView, status int) error {
	return e.executeWith(e.current(), w, candidates, v, status)
}

func (e *Engine) executeWith(c *compiled, w io.Writer, candidates []string, v *PageView, status int) error {
	tmpl, ok := c.pick(candidates...)
	if !ok {
		return fmt.Errorf("render: no layout among %v", candidates)
	}
	settings := *e.settings.Load()
	d := &pageData{
		PageView:   *v,
		CSSHref:    AssetPrefix + c.cssName,
		Tokens:     sanitizeTokens(v.Site.Tokens),
		Status:     status,
		StatusText: http.StatusText(status),
		Settings:   settings,
	}
	var docCSS builderCSS
	v.anchors = newAnchorSet()
	if v.Doc != nil {
		d.Blocks, docCSS = e.docBlocks(c, v)
	} else {
		d.Blocks = prepareSections(c.theme, v.Sections, v.Canvas)
	}
	if parts, pc := e.renderParts(c, tmpl, v); pc != nil {
		d.Part = parts
		docCSS.merge(*pc)
		docCSS.compilePresets(v.Site.Design)
	}
	if lp := v.loopPaged; lp != nil {
		d.Head = e.pagedHead(v, lp)
	} else {
		d.Head = buildHeadView(requestCtx(v), v, e.opts.HeadFilter)
	}
	// Editor frames (preview and canvas) run no page scripts: no plugin
	// site.scripts, island, hm.js or head-filter script tag, so nothing in the
	// frame can drive the editor. Stylesheets and JSON-LD data blocks stay.
	// Exceptions the gate cannot cover: sanitiser gaps and scripts a third-party
	// theme hardcodes; the sandbox CSP and the bridge port are the backstop.
	editor := v.Preview || v.Canvas
	d.PluginHead, d.PluginBody = pluginAssetMarkup(!editor)
	d.PluginHead += docCSS.head()
	var islands template.HTML
	if !editor {
		islands = docCSS.islands()
	} else {
		d.Head.Ext = dropScriptSrc(d.Head.Ext)
	}
	d.PluginBody += islands
	d.Body = template.HTML(RewriteEntityLinks(string(sanitisedHTML(v.Attrs["content"])), v.EntityLinks)) // #nosec G203 -- sanitised, only href replaced
	if d.Related == nil {
		// Views with no entity (404, error, archive) still let a shared partial
		// `call .Related` safely.
		d.Related = func(int) []RelatedItem { return nil }
	}
	if d.Series == nil {
		d.Series = func() *SeriesView { return nil }
	}
	if d.PostsByAuthor == nil {
		d.PostsByAuthor = func(_, page int) PostsPage { return PostsPage{Page: max(page, 1), Pages: 1} }
	}
	if d.PageNum < 1 {
		d.PageNum = 1
	}
	for i := range d.Blocks {
		finishBlock(&d.Blocks[i], v, d.Settings)
	}
	if c.hasJS && !editor {
		d.JSHref = AssetPrefix + "hm.js"
	}
	e.isolateSections(v.Ctx, tmpl, d.Blocks)
	var buf bytes.Buffer // render fully first so a template error never emits a partial page
	if err := safeExecute(&buf, tmpl, "base", d); err != nil {
		return fmt.Errorf("render %s: %w", candidates[0], err)
	}
	out := buf.Bytes()
	if docCSS.pending() && !bytes.Contains(out, []byte(docCSS.marker())) {
		out = spliceHead(out, docCSS.head()) // the theme never printed plugin-head
	}
	if islands != "" && !bytes.Contains(out, []byte(islands)) {
		out = spliceBody(out, islands) // the theme never printed plugin-body
	}
	_, err := w.Write(out)
	return err
}

// dropScriptSrc returns tags without the external script tags, as a new slice
// (the head view may be shared).
func dropScriptSrc(tags []extTag) []extTag {
	out := make([]extTag, 0, len(tags))
	for _, t := range tags {
		if t.Kind != "script-src" {
			out = append(out, t)
		}
	}
	return out
}

// finishBlock completes a prepared section with the page-level context its
// template reads: entity links resolved, form/submit state, theme settings.
func finishBlock(p *preparedSection, v *PageView, settings map[string]any) {
	p.Content, _ = rewriteLinksIn(p.Content, v.EntityLinks).(map[string]any)
	p.Page = sectionPage{Forms: v.Forms, PluginDirectory: v.PluginDirectory, ThemeDirectory: v.ThemeDirectory, Path: v.Path, Submitted: v.Submitted, FormError: v.FormError, PageNum: v.PageNum, Params: v.Params}
	p.Settings = settings
}

// prepareBlock prepares one section outside a page render (a component node).
// ok is false when the section renders nothing (hidden, blank content).
func prepareBlock(c *compiled, v *PageView, s Section, canvas bool, settings map[string]any) (p preparedSection, ok bool) {
	s.IsVisible = nil
	out := prepareSections(c.theme, []Section{s}, canvas)
	if len(out) == 0 {
		return p, false
	}
	p = out[0]
	finishBlock(&p, v, settings)
	return p, true
}

// safeExecute is ExecuteTemplate that turns a template panic into an error, so
// one section cannot take the process down.
func safeExecute(w io.Writer, t *template.Template, name string, data any) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("template panic: %v", r)
		}
	}()
	return t.ExecuteTemplate(w, name, data)
}

// SectionFailedMarker prefixes the HTML comment a failed section degrades to.
// Callers use it to avoid caching a degraded page.
const SectionFailedMarker = "<!-- section render failed"

// isolateSections trial-renders every section so one that fails (malformed
// stored content) degrades to an HTML comment instead of failing the page.
func (e *Engine) isolateSections(ctx context.Context, t *template.Template, blocks []preparedSection) {
	if ctx == nil {
		ctx = context.Background()
	}
	for i := range blocks {
		if blocks[i].Comment != "" {
			continue
		}
		if err := safeExecute(io.Discard, t, "section", blocks[i]); err != nil {
			e.log.ErrorContext(ctx, "render: section failed, rendered empty", "type", blocks[i].Type, "id", blocks[i].ID, "error", err)
			blocks[i].Comment = "section render failed: " + commentSafe(blocks[i].Type)
		}
	}
}

// RenderPage renders the layout the template hierarchy picks for v (see
// layoutCandidates). A view with no Kind is a single page when it has an
// entity (v.EntityID set) and a plain page otherwise.
func (e *Engine) RenderPage(w io.Writer, v *PageView) error {
	kind := v.Kind
	if kind == "" {
		kind = KindPage
		if v.EntityID != "" {
			kind = KindSingle
		}
	}
	key := v.SchemaSlug
	if kind == KindTaxonomy && v.Term != nil {
		key = v.Term.Kind
	}
	return e.execute(w, layoutCandidates(kind, key), v, http.StatusOK)
}

// RenderNotFound renders layouts/404.html (noindex, no sections).
func (e *Engine) RenderNotFound(w io.Writer, v *PageView) error {
	nv := *v
	nv.Sections, nv.Doc = nil, nil
	nv.Page.NoIndex = true
	if nv.Page.Title == "" {
		nv.Page.Title = "Page not found"
	}
	return e.execute(w, []string{"404"}, &nv, http.StatusNotFound)
}

// RenderError renders layouts/error.html for an HTTP error status.
func (e *Engine) RenderError(w io.Writer, v *PageView, status int) error {
	nv := *v
	nv.Sections, nv.Doc = nil, nil // a failing component must not render again on the error page
	nv.Page.NoIndex = true
	if nv.Page.Title == "" {
		nv.Page.Title = http.StatusText(status)
	}
	return e.execute(w, []string{"error"}, &nv, status)
}

// SectionInfo describes one section type for the admin schema form.
type SectionInfo struct {
	Type        string          `json:"type"`
	Title       string          `json:"title"`
	Description string          `json:"description,omitempty"`
	Schema      json.RawMessage `json:"schema,omitempty"`
}

// ThemeInfo is the GET /api/v1/render/theme payload.
type ThemeInfo struct {
	Name           string          `json:"name"`
	Version        string          `json:"version"`
	Sections       []SectionInfo   `json:"sections"`
	SettingsSchema json.RawMessage `json:"settings_schema,omitempty"`
}

// ThemeInfo returns the theme name, content-hash version and the schema of
// every section type, sorted by type.
func (e *Engine) ThemeInfo() ThemeInfo {
	c := e.current()
	info := ThemeInfo{Name: c.theme.Name, Version: c.version, SettingsSchema: c.theme.Settings, Sections: []SectionInfo{}}
	for typ, def := range c.theme.Sections {
		title := def.Meta.Title
		if title == "" {
			title = typ
		}
		info.Sections = append(info.Sections, SectionInfo{Type: typ, Title: title, Description: def.Meta.Description, Schema: def.Schema})
	}
	sort.Slice(info.Sections, func(i, j int) bool { return info.Sections[i].Type < info.Sections[j].Type })
	return info
}

// BuilderTokens reports whether the live theme opted in to the site design
// tokens (theme.json builder.tokens).
func (e *Engine) BuilderTokens() bool { return e.current().theme.BuilderTokens }

// StrictScripts reports whether the live theme opted in to the public policy
// without inline scripts (theme.json csp.strictScripts).
func (e *Engine) StrictScripts() bool { return e.current().theme.StrictScripts }

// BuilderParts lists the layout slots the live theme opted in to render
// builder parts for (theme.json builder.parts), in display order.
func (e *Engine) BuilderParts() []string {
	var out []string
	for _, slot := range builder.PartSlots() {
		if e.current().theme.BuilderParts[slot] {
			out = append(out, slot)
		}
	}
	return out
}

// Version is the content hash of the effective theme files; it keys the HTML cache.
func (e *Engine) Version() string { return e.current().version }

// OverrideAssets lets pick return another handler for a request's theme
// assets (nil from pick: serve the engine's own). Used by the theme preview.
func (e *Engine) OverrideAssets(pick func(*http.Request) http.Handler) { e.assetOverride.Store(&pick) }

// AssetHandler serves /theme/assets/* for the current theme version.
func (e *Engine) AssetHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if f := e.assetOverride.Load(); f != nil {
			if h := (*f)(r); h != nil {
				h.ServeHTTP(w, r)
				return
			}
		}
		e.current().assets.ServeHTTP(w, r)
	})
}

// requestCtx is the view's request context, or Background for views built
// outside a request.
func requestCtx(v *PageView) context.Context {
	if v.Ctx != nil {
		return v.Ctx
	}
	return context.Background()
}
