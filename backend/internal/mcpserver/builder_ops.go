package mcpserver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/builder"
)

// Limits of one builder_apply_ops call.
const (
	maxOps      = 500
	maxOpsBytes = 1 << 20
)

// opIn is one op of builder_apply_ops. The core verbs (insert remove move
// setProp setStyle setBind) are the builder's own; wrap, unwrap and duplicate
// are expanded into them here, as the editor does in the browser (see
// web/admin/js/builder/store.js wrapOps/unwrapOps/duplicateOps and
// clipops.js wrapManyOps).
type opIn struct {
	Op     string   `json:"op" jsonschema:"insert, remove, move, setProp, setStyle, setBind, wrap, unwrap or duplicate"`
	ID     string   `json:"id,omitempty" jsonschema:"target node id (remove, move, setProp, setStyle, setBind, unwrap, duplicate)"`
	IDs    []string `json:"ids,omitempty" jsonschema:"wrap: adjacent sibling node ids to put in the new wrapper"`
	Parent string   `json:"parent,omitempty" jsonschema:"insert and move: parent node id; empty for the top level"`
	Index  *int     `json:"index,omitempty" jsonschema:"insert and move: position among the parent's children; omitted appends"`
	Node   object   `json:"node,omitempty" jsonschema:"insert: the node {id?,t,p?,s?,c?,b?}; wrap: the wrapper (default a container). Omitted ids are generated"`
	Key    string   `json:"key,omitempty" jsonschema:"setProp, setBind: the prop key; setStyle: the style key"`
	BP     string   `json:"bp,omitempty" jsonschema:"setStyle: breakpoint with optional state, for example base, md or sm:hover"`
	Value  any      `json:"value,omitempty" jsonschema:"setProp: any JSON value; setStyle and setBind: a string. Omit or null deletes"`
}

// genID names a node id the server generated: the op it belongs to, the path
// of the node inside that op's node ("node", "node.c.0", ...) and the id.
type genID struct {
	Op   int    `json:"op"`
	Path string `json:"path"`
	ID   string `json:"id"`
}

func newNodeID() string {
	u := uuid.New()
	return fmt.Sprintf("n%x", u[:5])
}

func checkOpLimits(ops []opIn) error {
	if len(ops) == 0 {
		return codedErr(codeInvalidPageDoc, "ops is empty")
	}
	if len(ops) > maxOps {
		return codedErr(codeLimit, "at most %d ops per call, got %d", maxOps, len(ops))
	}
	b, err := json.Marshal(ops)
	if err != nil {
		return codedErr(codeInvalidPageDoc, "ops cannot be encoded: %v", err)
	}
	if len(b) > maxOpsBytes {
		return codedErr(codeLimit, "ops are larger than %d bytes", maxOpsBytes)
	}
	return nil
}

// applyOps applies ops to doc one after the other, expanding the convenience
// verbs against the document as it is by then. It stops at the first failing
// op; doc is then partly edited, so callers pass a copy.
func applyOps(doc *builder.Doc, ops []opIn) ([]genID, error) {
	used := map[string]bool{}
	var collect func(ns []*builder.Node)
	collect = func(ns []*builder.Node) {
		for _, n := range ns {
			used[n.ID] = true
			collect(n.C)
		}
	}
	collect(doc.Nodes)
	gen := func() string {
		for {
			if id := newNodeID(); !used[id] {
				used[id] = true
				return id
			}
		}
	}
	var ids []genID
	for i, in := range ops {
		core, made, err := expandOp(doc, in, gen)
		if err != nil {
			return nil, fmt.Errorf("op %d (%s): %w", i, in.Op, err)
		}
		for _, c := range core {
			if _, err := doc.Apply(c); err != nil {
				return nil, fmt.Errorf("op %d (%s): %w", i, in.Op, err)
			}
		}
		for _, m := range made {
			m.Op = i
			ids = append(ids, m)
		}
	}
	return ids, nil
}

// expandOp turns one op into core builder ops against doc and lists the ids it
// generated. It does not change doc.
func expandOp(doc *builder.Doc, in opIn, gen func() string) ([]builder.Op, []genID, error) {
	switch in.Op {
	case "insert":
		n, err := decodeNode(in.Node)
		if err != nil {
			return nil, nil, err
		}
		if n == nil {
			return nil, nil, fmt.Errorf("insert needs a node")
		}
		made := fillIDs(n, "node", gen)
		return []builder.Op{{Op: "insert", Parent: in.Parent, Index: indexOr(doc, in, false), Node: n}}, made, nil
	case "remove":
		if err := need(in.ID, "id"); err != nil {
			return nil, nil, err
		}
		return []builder.Op{{Op: "remove", ID: in.ID}}, nil, nil
	case "move":
		if err := need(in.ID, "id"); err != nil {
			return nil, nil, err
		}
		return []builder.Op{{Op: "move", ID: in.ID, Parent: in.Parent, Index: indexOr(doc, in, true)}}, nil, nil
	case "setProp", "setStyle", "setBind":
		if err := need(in.ID, "id"); err != nil {
			return nil, nil, err
		}
		if err := need(in.Key, "key"); err != nil {
			return nil, nil, err
		}
		if in.Op == "setStyle" {
			if err := need(in.BP, "bp"); err != nil {
				return nil, nil, err
			}
		}
		v, err := json.Marshal(in.Value)
		if err != nil {
			return nil, nil, fmt.Errorf("value: %v", err)
		}
		return []builder.Op{{Op: in.Op, ID: in.ID, Key: in.Key, BP: in.BP, Value: v}}, nil, nil
	case "wrap":
		return expandWrap(doc, in, gen)
	case "unwrap":
		return expandUnwrap(doc, in)
	case "duplicate":
		return expandDuplicate(doc, in, gen)
	}
	return nil, nil, fmt.Errorf("unknown op %q", in.Op)
}

func need(v, field string) error {
	if v == "" {
		return fmt.Errorf("%s is required", field)
	}
	return nil
}

// indexOr is the op's index, or the end of the parent's children when omitted.
// A move removes the node before it inserts it, so within the same parent the
// end is one earlier.
func indexOr(doc *builder.Doc, in opIn, move bool) int {
	if in.Index != nil {
		return *in.Index
	}
	n := len(doc.Nodes)
	if in.Parent != "" {
		p, _ := doc.Find(in.Parent)
		if p == nil {
			return 0
		}
		n = len(p.C)
	}
	if move {
		if from, _, _, ok := where(doc, in.ID); ok && from == in.Parent {
			n--
		}
	}
	return n
}

// moveSubject is the node an op on id acts on: the component of a legacy (v1)
// section stands for the whole section (store.js moveSubject).
func moveSubject(doc *builder.Doc, id string) string {
	n, p := doc.Find(id)
	if n != nil && p != nil && n.T == "component" && slices.Contains(doc.Nodes, p) {
		if _, ok := builder.V1Shaped(p); ok {
			return p.ID
		}
	}
	return id
}

// topV1 reports whether id is, or is the component of, a top-level legacy
// section, which the editor never wraps or unwraps (clipops.js topV1).
func topV1(doc *builder.Doc, id string) bool {
	n, p := doc.Find(moveSubject(doc, id))
	if n == nil || p != nil {
		return false
	}
	_, ok := builder.V1Shaped(n)
	return ok
}

// decodeNode reads a node object strictly: an unknown field is an error.
func decodeNode(o object) (*builder.Node, error) {
	if o == nil {
		return nil, nil
	}
	raw, err := json.Marshal(o)
	if err != nil {
		return nil, fmt.Errorf("node: %v", err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var n builder.Node
	if err := dec.Decode(&n); err != nil {
		return nil, fmt.Errorf("node: %v", err)
	}
	return &n, nil
}

// fillIDs gives every node of n without an id a generated one and lists them.
func fillIDs(n *builder.Node, path string, gen func() string) []genID {
	var out []genID
	if n.ID == "" {
		n.ID = gen()
		out = append(out, genID{Path: path, ID: n.ID})
	}
	for i, c := range n.C {
		if c != nil {
			out = append(out, fillIDs(c, path+".c."+strconv.Itoa(i), gen)...)
		}
	}
	return out
}

// where finds id: its parent's id ("" at the top level), its index and node.
func where(doc *builder.Doc, id string) (parent string, index int, n *builder.Node, ok bool) {
	n, p := doc.Find(id)
	if n == nil {
		return "", 0, nil, false
	}
	list := doc.Nodes
	if p != nil {
		parent, list = p.ID, p.C
	}
	for i, x := range list {
		if x.ID == id {
			return parent, i, n, true
		}
	}
	return "", 0, nil, false
}

// expandWrap: insert the wrapper where the first id is, then move the ids into
// it in document order (clipops.js wrapManyOps). The ids must be adjacent
// siblings, and a ref or component stays visible to its own editor, so it is
// never wrapped.
func expandWrap(doc *builder.Doc, in opIn, gen func() string) ([]builder.Op, []genID, error) {
	if len(in.IDs) == 0 {
		return nil, nil, fmt.Errorf("wrap needs ids")
	}
	type at struct {
		id    string
		index int
	}
	var run []at
	parent := ""
	for i, id := range slices.Compact(slices.Clone(in.IDs)) {
		p, idx, n, ok := where(doc, id)
		if !ok {
			return nil, nil, fmt.Errorf("no such node %q", id)
		}
		if n.T == "ref" || n.T == "component" {
			return nil, nil, fmt.Errorf("a %s cannot be wrapped", n.T)
		}
		if topV1(doc, id) {
			return nil, nil, fmt.Errorf("a legacy section cannot be wrapped")
		}
		if i > 0 && p != parent {
			return nil, nil, fmt.Errorf("wrap needs siblings under one parent")
		}
		parent = p
		run = append(run, at{id, idx})
	}
	slices.SortFunc(run, func(a, b at) int { return a.index - b.index })
	for i := range run {
		if i > 0 && run[i].index == run[i-1].index {
			return nil, nil, fmt.Errorf("wrap ids repeat a node")
		}
		if run[i].index != run[0].index+i {
			return nil, nil, fmt.Errorf("wrap needs adjacent siblings")
		}
	}
	w, err := decodeNode(in.Node)
	if err != nil {
		return nil, nil, err
	}
	if w == nil {
		w = &builder.Node{T: "container"}
	}
	if len(w.C) > 0 {
		return nil, nil, fmt.Errorf("the wrapper must not have children of its own")
	}
	made := fillIDs(w, "node", gen)
	core := []builder.Op{{Op: "insert", Parent: parent, Index: run[0].index, Node: w}}
	for i, r := range run {
		core = append(core, builder.Op{Op: "move", ID: r.id, Parent: w.ID, Index: i})
	}
	return core, made, nil
}

// expandUnwrap: move the container's children out after it, in order, then
// remove it (store.js unwrapOps). Only a container with elements in it.
func expandUnwrap(doc *builder.Doc, in opIn) ([]builder.Op, []genID, error) {
	if err := need(in.ID, "id"); err != nil {
		return nil, nil, err
	}
	id := moveSubject(doc, in.ID)
	parent, index, n, ok := where(doc, id)
	if !ok {
		return nil, nil, fmt.Errorf("no such node %q", in.ID)
	}
	if n.T != "container" || len(n.C) == 0 {
		return nil, nil, fmt.Errorf("unwrap needs a container with elements in it")
	}
	if topV1(doc, id) {
		return nil, nil, fmt.Errorf("a legacy section cannot be unwrapped")
	}
	if parent == "" {
		for _, c := range n.C {
			if c.T == "component" {
				return nil, nil, fmt.Errorf("unwrap would leave a component at the top level")
			}
		}
	}
	var core []builder.Op
	for k, c := range n.C {
		core = append(core, builder.Op{Op: "move", ID: c.ID, Parent: parent, Index: index + 1 + k})
	}
	return append(core, builder.Op{Op: "remove", ID: n.ID}), nil, nil
}

// expandDuplicate: insert a copy with all ids new right after the original
// (store.js duplicateOps).
func expandDuplicate(doc *builder.Doc, in opIn, gen func() string) ([]builder.Op, []genID, error) {
	if err := need(in.ID, "id"); err != nil {
		return nil, nil, err
	}
	parent, index, n, ok := where(doc, moveSubject(doc, in.ID)) // a legacy section's component duplicates the section
	if !ok {
		return nil, nil, fmt.Errorf("no such node %q", in.ID)
	}
	cp := builder.FreshIDs([]*builder.Node{n}, gen)[0]
	var made []genID
	var walk func(x *builder.Node, path string)
	walk = func(x *builder.Node, path string) {
		made = append(made, genID{Path: path, ID: x.ID})
		for i, c := range x.C {
			walk(c, path+".c."+strconv.Itoa(i))
		}
	}
	walk(cp, "node")
	return []builder.Op{{Op: "insert", Parent: parent, Index: index + 1, Node: cp}}, made, nil
}
