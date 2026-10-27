package render

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"html"

	"hatamoto.dev/core/internal/builder"
)

// RefSource resolves the entities a doc embeds by reference. The server
// implements it on the entity store; render never imports entity.
type RefSource interface {
	// Block returns the doc of a block entity. It is false for an unknown id
	// and for an entity that is not a block (a page id, a part id); the status
	// does not matter, blocks are always drafts.
	Block(ctx context.Context, id string) (*builder.Doc, bool)
	// Part returns the doc of a theme_part entity whose slot is slot.
	Part(ctx context.Context, slot, id string) (*builder.Doc, bool)
}

// refState is the budget of one render: the block ids being expanded (a ref to
// one of them is a cycle), the expanded node count, and the docs already read.
type refState struct {
	stack []string
	nodes int
	memo  map[string]*builder.Doc
}

// Reasons a ref renders as a placeholder instead of its block.
const (
	refMissing = "missing"
	refCycle   = "cycle"
	refDepth   = "depth"
	refLimit   = "limit"
)

var refLabels = map[string]string{
	refMissing: "Missing block",
	refCycle:   "Block loop",
	refDepth:   "Blocks nested too deep",
	refLimit:   "Block limit reached",
}

// hasRef reports whether n or a visible descendant is a ref node.
func hasRef(n *builder.Node) bool {
	if n == nil || nodeHidden(n) {
		return false
	}
	if n.T == "ref" {
		return true
	}
	for _, c := range n.C {
		if hasRef(c) {
			return true
		}
	}
	return false
}

// hasKind reports whether n or a visible descendant has kind t.
func hasKind(n *builder.Node, t string) bool {
	if n == nil || nodeHidden(n) {
		return false
	}
	if n.T == t {
		return true
	}
	for _, c := range n.C {
		if hasKind(c, t) {
			return true
		}
	}
	return false
}

// hasRefFailure reports whether an expanded tree holds a ref placeholder.
func hasRefFailure(n *builder.Node) bool {
	if n == nil {
		return false
	}
	if str(n.P, "refx") != "" {
		return true
	}
	for _, c := range n.C {
		if hasRefFailure(c) {
			return true
		}
	}
	return false
}

// expandRefs returns top with every ref node replaced by a wrapper container
// (id of the ref, p.ref = block id, s kept) holding a copy of the block's nodes
// under prefixed ids. It returns top itself when no ref is inside, so a doc
// without refs renders byte for byte as before. A ref that cannot be expanded
// (missing, cycle, too deep, over the node budget) becomes a wrapper with
// p.refx = why and no children; render turns it into a comment or placeholder.
func (e *Engine) expandRefs(ctx context.Context, top *builder.Node, st *refState) *builder.Node {
	if !hasRef(top) {
		return top
	}
	if top.T == "ref" {
		return e.expandRef(ctx, top, st)
	}
	cp := *top
	cp.C = make([]*builder.Node, len(top.C))
	for i, c := range top.C {
		cp.C[i] = e.expandRefs(ctx, c, st)
	}
	return &cp
}

func (e *Engine) expandRef(ctx context.Context, n *builder.Node, st *refState) *builder.Node {
	id, _ := n.P["block"].(string)
	w := &builder.Node{ID: n.ID, T: "container", S: n.S, P: map[string]any{"tag": "div", "ref": id}}
	// Only preset is copied: the ref wrapper is not the author's labelled node,
	// so navlabel (editor-only) is deliberately not carried to the output.
	if p := builder.NodePreset(n); p != "" {
		w.P["preset"] = p
	}
	fail := func(why string) *builder.Node {
		w.P["refx"] = why
		return w
	}
	if e.opts.Refs == nil || id == "" {
		return fail(refMissing)
	}
	for _, s := range st.stack {
		if s == id {
			return fail(refCycle)
		}
	}
	if len(st.stack) >= builder.RefMaxDepth {
		return fail(refDepth)
	}
	doc, seen := st.memo[id]
	if !seen {
		var ok bool
		if doc, ok = e.opts.Refs.Block(ctx, id); !ok {
			doc = nil
		}
		if st.memo == nil {
			st.memo = map[string]*builder.Doc{}
		}
		st.memo[id] = doc
	}
	if doc == nil {
		return fail(refMissing)
	}
	if st.nodes += countNodes(doc.Nodes); st.nodes > builder.RefMaxNodes {
		return fail(refLimit)
	}
	st.stack = append(st.stack, id)
	defer func() { st.stack = st.stack[:len(st.stack)-1] }()
	for _, c := range builder.PrefixIDs(doc.Nodes, refPrefix(id)) {
		w.C = append(w.C, e.expandRefs(ctx, c, st))
	}
	return w
}

// refPrefix is the id prefix of a block's nodes: deterministic, so a block used
// twice shares its node CSS, and distinct per block, so its ids never collide
// with the page's.
func refPrefix(blockID string) string {
	h := sha256.Sum256([]byte(blockID))
	return "b" + hex.EncodeToString(h[:4]) + "_"
}

func countNodes(ns []*builder.Node) int {
	c := 0
	for _, n := range ns {
		if n != nil {
			c += 1 + countNodes(n.C)
		}
	}
	return c
}

// renderRef writes an expanded ref wrapper. The block's nodes render without
// canvas markers: the ref is one selectable unit, its inner ids are not in the
// client doc.
func (e *Engine) renderRef(b *bytes.Buffer, c *compiled, v *PageView, n *builder.Node, canvas bool) {
	if why := str(n.P, "refx"); why != "" {
		if !canvas {
			b.WriteString("<!-- hm-ref: " + why + " -->")
			return
		}
		b.WriteString(`<div class="hm-n hm-ref-miss hm-n-` + n.ID + `" ` + nodeMarker(n.ID) + " " + kindMarker("ref") + ">" + html.EscapeString(refLabels[why]) + "</div>")
		return
	}
	tag := str(n.P, "tag")
	if !containerTags[tag] {
		tag = "div"
	}
	marked := *n
	marked.T = "ref"
	b.WriteString("<" + tag + " ")
	nodeAttrs(b, nil, &marked, "hm-c", canvas)
	b.WriteString(">")
	for _, ch := range n.C {
		e.renderNode(b, c, v, ch, false)
	}
	b.WriteString("</" + tag + ">")
}

// refCanvasCSS styles the canvas placeholder of a ref that cannot render.
const refCanvasCSS = `.hm-ref-miss{padding:var(--hm-space-4,1rem);border:1px dashed currentColor;opacity:.7}`
