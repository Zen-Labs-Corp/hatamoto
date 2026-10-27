package render

import (
	"bytes"
	"fmt"
	"html/template"
	"strings"

	"hatamoto.dev/core/internal/builder"
)

// Theme parts: a theme that opts in (theme.json builder.parts) lets the owner
// build its header, footer and single/archive content wrapper in the builder.
// The theme's own partial is re-registered under hm-theme-<name> and the
// original name becomes a wrapper that prints the part when one is active and
// falls through to the theme's own markup otherwise, so a theme without the
// opt-in is never touched and an opted-in one without a part renders the same
// bytes as before.

// slotMarker is what a slot node prints publicly; the part's markup is split
// at it into the pieces before and after the theme content.
const slotMarker = "<!--hm-slot-->"

// slotCanvasCSS styles the canvas placeholder of a slot node.
const slotCanvasCSS = `.hm-slot{padding:var(--hm-space-4,1rem);border:1px dashed currentColor;opacity:.7;text-align:center}`

// partOut is the rendered markup of the active parts (pageData.Part).
type partOut struct {
	Header, Footer, Before, After template.HTML
	// Replace drops the theme's own content markup between Before and After
	// (an archive part whose slot has replace set).
	Replace bool
}

// partPrefixes are the id prefixes of a part's nodes, so they never collide
// with the page's own (or a block's).
var partPrefixes = map[string]string{
	builder.SlotHeader: "ph_", builder.SlotFooter: "pf_",
	builder.SlotSingle: "ps_", builder.SlotArchive: "pa_",
}

const (
	headerWrapper  = `{{define "header"}}{{if .Part.Header}}{{.Part.Header}}{{else}}{{template "hm-theme-header" .}}{{end}}{{end}}`
	footerWrapper  = `{{define "footer"}}{{if .Part.Footer}}{{.Part.Footer}}{{else}}{{template "hm-theme-footer" .}}{{end}}{{end}}`
	contentWrapper = `{{define "content"}}{{.Part.Before}}{{if not .Part.Replace}}{{template "hm-theme-content" .}}{{end}}{{.Part.After}}{{end}}`
)

// wrapPartial re-registers the template name as hm-theme-<name> and redefines
// name with wrapper. It runs before the set is first executed.
func wrapPartial(t *template.Template, name, wrapper string) error {
	orig := t.Lookup(name)
	if orig == nil || orig.Tree == nil {
		return fmt.Errorf("theme parts: template %q is not defined", name)
	}
	if _, err := t.AddParseTree("hm-theme-"+name, orig.Tree.Copy()); err != nil {
		return fmt.Errorf("theme parts: %w", err)
	}
	if _, err := t.New("hm-part-" + name).Parse(wrapper); err != nil {
		return fmt.Errorf("theme parts: %w", err)
	}
	return nil
}

// contentSlot is the part slot a layout name is wrapped for: the single and
// archive families. Every other layout (page, home, taxonomy, author, search,
// 404, error) is never wrapped.
func contentSlot(layout string) string {
	switch {
	case layout == "single" || strings.HasPrefix(layout, "single-"):
		return builder.SlotSingle
	case layout == "archive" || strings.HasPrefix(layout, "archive-"):
		return builder.SlotArchive
	}
	return ""
}

// activeSlots are the slots to render for this layout: opted in by the theme
// and assigned by the site. None for a canvas of a part (NoParts) or when the
// engine has no RefSource.
func (e *Engine) activeSlots(c *compiled, tmpl *template.Template, v *PageView) []string {
	if v.NoParts || e.opts.Refs == nil || len(c.theme.BuilderParts) == 0 || len(v.Site.Design.Parts) == 0 {
		return nil
	}
	var out []string
	for _, s := range []string{builder.SlotHeader, builder.SlotFooter, c.contentSlots[tmpl]} {
		if s != "" && c.theme.BuilderParts[s] && v.Site.Design.Parts[s] != "" {
			out = append(out, s)
		}
	}
	return out
}

// renderParts renders the active parts of the page. A part that is missing,
// of another slot, unusable (single/archive without exactly one slot), empty
// or that fails to render is skipped: the theme's own markup shows instead,
// never a blank header. The second result holds the parts' CSS and kinds, to
// merge into the page's.
func (e *Engine) renderParts(c *compiled, tmpl *template.Template, v *PageView) (partOut, *builderCSS) {
	var out partOut
	slots := e.activeSlots(c, tmpl, v)
	if len(slots) == 0 {
		return out, nil
	}
	// Load every part first so one batched call serves all of them.
	tops := map[string][]*builder.Node{}
	var all []*builder.Node
	for _, slot := range slots {
		if ns, ok := e.loadPart(c, v, slot, v.Site.Design.Parts[slot]); ok {
			tops[slot] = ns
			all = append(all, ns...)
		}
	}
	e.prefetchExt(v, all...)
	e.prefetchImages(all...)
	pc := newBuilderCSS(c, v, false)
	used := false
	for _, slot := range slots {
		ns, loaded := tops[slot]
		if !loaded {
			continue
		}
		html, replace, ok := e.renderPart(c, v, slot, v.Site.Design.Parts[slot], ns, &pc)
		if !ok {
			continue
		}
		switch slot {
		case builder.SlotHeader:
			out.Header = template.HTML(html) // #nosec G203 -- node markup, every value escaped
		case builder.SlotFooter:
			out.Footer = template.HTML(html) // #nosec G203 -- as above
		default:
			before, after, _ := strings.Cut(html, slotMarker)
			out.Before, out.After = template.HTML(before), template.HTML(after) // #nosec G203 -- as above
			out.Replace = replace && slot == builder.SlotArchive
		}
		used = true
	}
	if !used {
		return partOut{}, nil
	}
	pc.compilePresets(v.Site.Design)
	return out, &pc
}

// loadPart reads one part doc and returns its top-level nodes with refs
// expanded and ids prefixed. ok is false when the part is missing or cannot be
// read (logged once); nothing is rendered yet.
func (e *Engine) loadPart(c *compiled, v *PageView, slot, id string) (ns []*builder.Node, ok bool) {
	ctx := requestCtx(v)
	defer func() {
		if r := recover(); r != nil {
			e.warnPart(slot, id, fmt.Sprintf("panic: %v", r))
			ns, ok = nil, false
		}
	}()
	doc, found := e.opts.Refs.Part(ctx, slot, id)
	if !found || doc == nil {
		e.warnPart(slot, id, "part not found for this slot")
		return nil, false
	}
	st := &refState{}
	for _, top := range builder.PrefixIDs(doc.Nodes, partPrefixes[slot]) {
		if top == nil || nodeHidden(top) {
			continue
		}
		ns = append(ns, e.expandRefs(ctx, top, st))
	}
	return ns, true
}

// renderPart renders the loaded nodes of one part into HTML and adds its CSS
// and kinds to bc. ok is false when the part cannot be used (see
// renderParts); replace reports a visible slot with replace set. bc is only
// touched on success.
func (e *Engine) renderPart(c *compiled, v *PageView, slot, id string, ns []*builder.Node, bc *builderCSS) (html string, replace, ok bool) {
	defer func() {
		if r := recover(); r != nil {
			e.warnPart(slot, id, fmt.Sprintf("panic: %v", r))
			html, replace, ok = "", false, false
		}
	}()
	trial := newBuilderCSS(c, v, false)
	saved := v.anchors.clone()
	defer func() {
		if !ok {
			v.anchors = saved // a skipped part leaves its anchors free for the theme's own markup
		}
	}()
	var b bytes.Buffer
	for _, n := range ns {
		trial.native = true
		replace = replace || replacesContent(n)
		trial.collectKinds(n)
		builder.TreeCSSWith([]*builder.Node{n}, trial.css, trial.opts)
		e.renderNode(&b, c, v, n, false)
	}
	html = b.String()
	if strings.TrimSpace(html) == "" {
		e.warnPart(slot, id, "part renders nothing")
		return "", false, false
	}
	if slot == builder.SlotSingle || slot == builder.SlotArchive {
		if strings.Count(html, slotMarker) != 1 {
			e.warnPart(slot, id, "part needs exactly one visible slot")
			return "", false, false
		}
	}
	bc.merge(trial)
	return html, replace, true
}

// replacesContent reports whether a visible slot with replace set is in n.
func replacesContent(n *builder.Node) bool {
	if n == nil || nodeHidden(n) {
		return false
	}
	if n.T == "slot" {
		return n.P["replace"] == true
	}
	for _, ch := range n.C {
		if replacesContent(ch) {
			return true
		}
	}
	return false
}

// warnPart logs why a part was skipped, once per engine and part.
func (e *Engine) warnPart(slot, id, why string) {
	if _, dup := e.partWarned.LoadOrStore(slot+"/"+id+"/"+why, true); !dup {
		e.log.Warn("render: theme part skipped, using the theme's own markup", "slot", slot, "part", id, "reason", why)
	}
}

// merge adds the CSS, kinds and presets of o to b. Preset rules are compiled
// again by the caller.
func (b *builderCSS) merge(o builderCSS) {
	if b.css == nil {
		b.css = map[string]string{}
	}
	for id, rules := range o.css {
		b.css[id] = rules
	}
	for k := range o.kinds {
		if b.kinds == nil {
			b.kinds = map[string]bool{}
		}
		b.kinds[k] = true
	}
	for p := range o.presets {
		if b.presets == nil {
			b.presets = map[string]bool{}
		}
		b.presets[p] = true
	}
	b.native = b.native || o.native
	b.fig, b.imglink, b.clink, b.lightbox = b.fig || o.fig, b.imglink || o.imglink, b.clink || o.clink, b.lightbox || o.lightbox
	b.ref = b.ref || o.ref
	b.missing = b.missing || o.missing
	if b.tokens == "" {
		force := b.opts.Force
		b.tokens, b.opts = o.tokens, o.opts
		b.opts.Force = force
	}
}
