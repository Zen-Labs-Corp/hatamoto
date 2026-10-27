package render

import (
	"bytes"
	"fmt"
	"html/template"
	"io"

	"hatamoto.dev/core/internal/builder"
)

// RenderNode writes the markup of one node subtree: a fragment, not a page. It
// runs no layout, head or section preparation, so the canvas can patch a node
// without rendering the page around it. Hidden nodes write nothing.
func (e *Engine) RenderNode(w io.Writer, v *PageView, n *builder.Node, canvas bool) error {
	var b bytes.Buffer
	if n != nil {
		n = e.expandRefs(requestCtx(v), n, &refState{})
		e.prefetchExt(v, n)
	}
	e.renderNode(&b, e.current(), v, n, canvas)
	_, err := w.Write(b.Bytes())
	return err
}

// NodeCSS compiles the styles of the top-level node n and its descendants, in
// id order, for the per-node <style data-hm-css> tags of a canvas patch. It is
// canvas-only: the rules also carry the forced state selectors.
func (e *Engine) NodeCSS(n *builder.Node) map[string]string { return e.nodeCSSIn(n, nil) }

func (e *Engine) nodeCSSIn(n, parent *builder.Node) map[string]string {
	css := map[string]string{}
	builder.TreeCSSIn([]*builder.Node{n}, parent, css, builder.CSSOpts{Vars: e.current().theme.BuilderTokens, Force: true})
	return css
}

// NodeCSSFor is NodeCSS for a node of the page v under parent (nil at top
// level): a ref inside n is expanded first, so the CSS of the block's nodes is
// included, and parent names the element its parent-hover rules select on.
func (e *Engine) NodeCSSFor(v *PageView, n, parent *builder.Node) map[string]string {
	if n != nil {
		n = e.expandRefs(requestCtx(v), n, &refState{})
	}
	return e.nodeCSSIn(n, parent)
}

// Fragment is one rendered node for a canvas patch.
type Fragment struct {
	Sel  string            `json:"sel"` // "section" | "node"
	Key  string            `json:"key"`
	HTML string            `json:"html"`
	CSS  map[string]string `json:"css,omitempty"`
}

// PrefetchExt fetches the ext data of every node (refs expanded) in one batch,
// the way a page render does. A caller that renders several top nodes of one
// view calls it first, so each RenderTop finds its data already asked instead
// of spending one of the two plugin round trips a view may make.
func (e *Engine) PrefetchExt(v *PageView, nodes []*builder.Node) {
	expanded := make([]*builder.Node, 0, len(nodes))
	for _, n := range nodes {
		if n != nil && !nodeHidden(n) && builder.ValidID(n.ID) {
			expanded = append(expanded, e.expandRefs(requestCtx(v), n, &refState{}))
		}
	}
	e.prefetchExt(v, expanded...)
}

// RenderTop renders top-level node n exactly as docBlocks does inside a page:
// a v1-shaped node through the theme section partial, anything else natively.
// Canvas markers are always on. ok is false when the node renders nothing
// (hidden or blank), so a full render would not contain it either.
func (e *Engine) RenderTop(v *PageView, n *builder.Node) (Fragment, bool, error) {
	if n == nil || nodeHidden(n) || !builder.ValidID(n.ID) {
		return Fragment{}, false, nil
	}
	c := e.current()
	n = e.expandRefs(requestCtx(v), n, &refState{})
	e.prefetchExt(v, n)
	if comp, ok := builder.V1Shaped(n); ok {
		p, ok := prepareBlock(c, v, compSection(comp), true, *e.settings.Load())
		if !ok {
			return Fragment{}, false, nil
		}
		var out bytes.Buffer
		if p.Comment == "" {
			if tmpl, found := c.pick("page"); found {
				if err := execSection(tmpl, &out, p); err != nil {
					e.log.Error("render: section failed, rendered empty", "type", p.Type, "id", p.ID, "error", err)
					p.Comment = "section render failed: " + commentSafe(p.Type)
				}
			}
		}
		if p.Comment != "" {
			out.Reset()
			out.WriteString(string(p.CommentHTML()))
		}
		return Fragment{Sel: "section", Key: comp.ID, HTML: out.String()}, true, nil
	}
	var b bytes.Buffer
	e.renderNode(&b, c, v, n, true)
	if b.Len() == 0 {
		return Fragment{}, false, nil
	}
	return Fragment{Sel: "node", Key: n.ID, HTML: b.String(), CSS: e.NodeCSS(n)}, true, nil
}

// execSection runs the theme "section" partial, turning a template panic into
// an error.
func execSection(t *template.Template, w *bytes.Buffer, p preparedSection) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("section panic: %v", r)
		}
	}()
	return t.ExecuteTemplate(w, "section", p)
}
