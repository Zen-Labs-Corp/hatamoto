package builder

import (
	"encoding/json"
	"html"
	"sort"
	"strings"

	"github.com/microcosm-cc/bluemonday"
)

// Limits of a diff report.
const (
	maxDiffChanges = 200
	maxHintRunes   = 40
	maxValueRunes  = 120
)

// Node change operations.
const (
	DiffAdded   = "added"
	DiffRemoved = "removed"
	DiffMoved   = "moved"
	DiffChanged = "changed"
)

// DocDiff is the per-node difference between two docs, matched by node id.
type DocDiff struct {
	Changes   []NodeChange
	Truncated int // changes beyond the cap, not listed
}

// NodeChange is one node that differs. Added and removed nodes are reported
// by their root only (Subtree counts the descendants that went with them).
type NodeChange struct {
	ID    string
	T     string // kind in the new doc (old doc for removed)
	Label string // palette label of T, T itself when it has none
	Hint  string // a short text of the node, "" when it has none
	Op    string // DiffAdded | DiffRemoved | DiffMoved | DiffChanged
	// Parent is the parent id in the new doc (old doc for removed), "" = top
	// level; Index is the position among its siblings there.
	Parent  string
	Index   int
	Subtree int
	// Field changes of a node in both docs (a moved node lists them too).
	Props, Styles, Binds []FieldChange
	TypeFrom             string // the old kind when t changed
}

// FieldChange is one changed prop, style or binding. BP is set for styles.
type FieldChange struct {
	Key, BP  string
	Kind     string // DiffAdded | DiffRemoved | DiffChanged
	Old, New string // short renderings, "" when absent
	// Note qualifies the change ("formatting changed": an inline html change
	// whose text is the same).
	Note string
}

type nodeRef struct {
	n      *Node
	parent string
	index  int
}

// DiffDocs compares a (older) with b (newer) by node id. A nil doc is empty.
// Changes come in the new doc's document order, then removed roots in the old
// doc's order, capped at 200.
func DiffDocs(a, b *Doc) *DocDiff {
	ai, aorder := indexDoc(a)
	bi, border := indexDoc(b)
	labels := kindLabels()
	moved := movedSet(a, b, ai, bi)

	var out []NodeChange
	for _, id := range border {
		nb := bi[id]
		na, common := ai[id]
		if !common {
			if inAbsent(ai, nb.parent) {
				continue // inside an added parent: reported with it
			}
			out = append(out, newChange(nb, DiffAdded, labels, descendantsNotIn(nb.n, ai)))
			continue
		}
		c := newChange(nb, "", labels, 0)
		fillFields(&c, na.n, nb.n)
		switch {
		case moved[id]:
			c.Op = DiffMoved
		case c.hasFields():
			c.Op = DiffChanged
		default:
			continue
		}
		out = append(out, c)
	}
	for _, id := range aorder {
		na := ai[id]
		if _, ok := bi[id]; ok {
			continue
		}
		if inAbsent(bi, na.parent) {
			continue // inside a removed parent: reported with it
		}
		out = append(out, newChange(na, DiffRemoved, labels, descendantsNotIn(na.n, bi)))
	}

	d := &DocDiff{Changes: out}
	if len(out) > maxDiffChanges {
		d.Truncated = len(out) - maxDiffChanges
		d.Changes = out[:maxDiffChanges]
	}
	return d
}

func (c NodeChange) hasFields() bool {
	return c.TypeFrom != "" || len(c.Props) > 0 || len(c.Styles) > 0 || len(c.Binds) > 0
}

// inAbsent reports whether parent is a real node missing from other, i.e. the
// node sits inside an added (or removed) parent and is not a root.
func inAbsent(other map[string]nodeRef, parent string) bool {
	if parent == "" {
		return false
	}
	_, ok := other[parent]
	return !ok
}

func newChange(r nodeRef, op string, labels map[string]string, subtree int) NodeChange {
	label := labels[r.n.T]
	if label == "" {
		label = r.n.T
	}
	return NodeChange{
		ID: r.n.ID, T: r.n.T, Label: label, Hint: hintOf(r.n),
		Op: op, Parent: r.parent, Index: r.index, Subtree: subtree,
	}
}

func kindLabels() map[string]string {
	m := map[string]string{}
	for _, k := range Kinds() {
		m[k.Kind] = k.Label
	}
	return m
}

// indexDoc maps id -> position and lists ids in document order.
func indexDoc(d *Doc) (map[string]nodeRef, []string) {
	idx := map[string]nodeRef{}
	var order []string
	if d == nil {
		return idx, order
	}
	var walk func(ns []*Node, parent string)
	walk = func(ns []*Node, parent string) {
		for i, n := range ns {
			if n == nil {
				continue
			}
			if _, dup := idx[n.ID]; !dup {
				idx[n.ID] = nodeRef{n, parent, i}
				order = append(order, n.ID)
			}
			walk(n.C, n.ID)
		}
	}
	walk(d.Nodes, "")
	return idx, order
}

// descendantsNotIn counts n's descendants whose id is not in keep (nil keeps none).
func descendantsNotIn(n *Node, keep map[string]nodeRef) int {
	cnt := 0
	var walk func(ns []*Node)
	walk = func(ns []*Node) {
		for _, c := range ns {
			if c == nil {
				continue
			}
			if _, ok := keep[c.ID]; ok {
				continue // reported with its own row, with everything below it
			}
			cnt++
			walk(c.C)
		}
	}
	walk(n.C)
	return cnt
}

// movedSet returns the common ids that changed parent, plus, per parent, the
// siblings outside a longest run that kept its relative order: an insert
// between two siblings moves nobody, a true reorder moves only what jumped.
func movedSet(a, b *Doc, ai, bi map[string]nodeRef) map[string]bool {
	moved := map[string]bool{}
	for id, rb := range bi {
		if ra, ok := ai[id]; ok && ra.parent != rb.parent {
			moved[id] = true
		}
	}
	// New-doc sibling lists, keeping only nodes that stayed under this parent.
	lists := map[string][]string{}
	if b != nil {
		var walk func(ns []*Node, parent string)
		walk = func(ns []*Node, parent string) {
			for _, n := range ns {
				if n == nil {
					continue
				}
				if ra, ok := ai[n.ID]; ok && ra.parent == parent && !moved[n.ID] {
					lists[parent] = append(lists[parent], n.ID)
				}
				walk(n.C, n.ID)
			}
		}
		walk(b.Nodes, "")
	}
	// Old position among those same stayers.
	oldPos := map[string]int{}
	counter := map[string]int{}
	if a != nil {
		var walk func(ns []*Node, parent string)
		walk = func(ns []*Node, parent string) {
			for _, n := range ns {
				if n == nil {
					continue
				}
				if rb, ok := bi[n.ID]; ok && rb.parent == parent && !moved[n.ID] {
					oldPos[n.ID] = counter[parent]
					counter[parent]++
				}
				walk(n.C, n.ID)
			}
		}
		walk(a.Nodes, "")
	}
	parents := make([]string, 0, len(lists))
	for p := range lists {
		parents = append(parents, p)
	}
	sort.Strings(parents)
	for _, p := range parents {
		ids := lists[p]
		seq := make([]int, len(ids))
		for i, id := range ids {
			seq[i] = oldPos[id]
		}
		for _, i := range outsideLIS(seq) {
			moved[ids[i]] = true
		}
	}
	return moved
}

// outsideLIS returns the indexes of seq that are not part of one longest
// strictly increasing subsequence (the last of equally long ones).
func outsideLIS(seq []int) []int {
	n := len(seq)
	if n < 2 {
		return nil
	}
	length := make([]int, n)
	prev := make([]int, n)
	best := 0
	for i := range seq {
		length[i], prev[i] = 1, -1
		for j := 0; j < i; j++ {
			if seq[j] < seq[i] && length[j]+1 > length[i] {
				length[i], prev[i] = length[j]+1, j
			}
		}
		if length[i] >= length[best] {
			best = i
		}
	}
	keep := make([]bool, n)
	for i := best; i >= 0; i = prev[i] {
		keep[i] = true
	}
	var out []int
	for i := range seq {
		if !keep[i] {
			out = append(out, i)
		}
	}
	return out
}

func fillFields(c *NodeChange, a, b *Node) {
	if a.T != b.T {
		c.TypeFrom = a.T
	}
	for _, k := range unionKeys(a.P, b.P) {
		if fc, ok := propChange(k, a.P, b.P); ok {
			if k == "html" && (inlineKind(a.T) || inlineKind(b.T)) {
				fc = inlineHTMLChange(fc, a.P[k], b.P[k])
			}
			c.Props = append(c.Props, fc)
		}
	}
	for _, k := range unionKeys(a.B, b.B) {
		ov, ook := a.B[k]
		nv, nok := b.B[k]
		if ook && nok && ov == nv {
			continue
		}
		c.Binds = append(c.Binds, FieldChange{Key: k, Kind: fieldKind(ook, nok), Old: ov, New: nv})
	}
	for _, bp := range bpOrder(a.S, b.S) {
		for _, k := range unionKeys(a.S[bp], b.S[bp]) {
			ov, ook := a.S[bp][k]
			nv, nok := b.S[bp][k]
			if ook && nok && ov == nv {
				continue
			}
			c.Styles = append(c.Styles, FieldChange{Key: k, BP: bp, Kind: fieldKind(ook, nok), Old: ov, New: nv})
		}
	}
}

func fieldKind(had, has bool) string {
	switch {
	case had && has:
		return DiffChanged
	case had:
		return DiffRemoved
	}
	return DiffAdded
}

func inlineKind(t string) bool { return t == "heading" || t == "text" }

// inlineHTMLChange shows the inline html of a heading or text as its text, the
// way it reads; when only the markup differs it says so in Note.
func inlineHTMLChange(fc FieldChange, ov, nv any) FieldChange {
	o, n := InlineText(renderValue(ov)), InlineText(renderValue(nv))
	if fc.Kind == DiffChanged && o == n {
		fc.Note = "formatting changed"
	}
	if fc.Kind != DiffAdded {
		fc.Old = clipRunes(o, maxValueRunes)
	}
	if fc.Kind != DiffRemoved {
		fc.New = clipRunes(n, maxValueRunes)
	}
	return fc
}

// stripTags is built once: a bluemonday policy is safe for concurrent use and
// building one allocates, and InlineText runs per node.
var stripTags = bluemonday.StrictPolicy()

// InlineText is the text of an inline html value: tags removed, entities
// decoded.
func InlineText(s string) string {
	return html.UnescapeString(stripTags.Sanitize(s))
}

// propChange compares one prop by its JSON form.
func propChange(key string, a, b map[string]any) (FieldChange, bool) {
	ov, ook := a[key]
	nv, nok := b[key]
	o, n := renderValue(ov), renderValue(nv)
	if ook && nok && o == n {
		return FieldChange{}, false
	}
	if !ook && !nok {
		return FieldChange{}, false
	}
	fc := FieldChange{Key: key, Kind: fieldKind(ook, nok)}
	if ook {
		fc.Old = clipRunes(o, maxValueRunes)
	}
	if nok {
		fc.New = clipRunes(n, maxValueRunes)
	}
	return fc, true
}

// renderValue is a string as is, anything else as compact JSON.
func renderValue(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

func unionKeys[V any](a, b map[string]V) []string {
	seen := map[string]bool{}
	var ks []string
	for _, m := range []map[string]V{a, b} {
		for k := range m {
			if !seen[k] {
				seen[k] = true
				ks = append(ks, k)
			}
		}
	}
	sort.Strings(ks)
	return ks
}

// bpOrder lists the keys of both style maps: per breakpoint base, md, sm the
// plain key then its states (sKeyOrder), others sorted after.
func bpOrder(a, b map[string]map[string]string) []string {
	all := unionKeys(a, b)
	rank := make(map[string]int, len(sKeyOrder))
	for i, k := range sKeyOrder {
		rank[k] = i
	}
	sort.SliceStable(all, func(i, j int) bool {
		ri, iok := rank[all[i]]
		rj, jok := rank[all[j]]
		switch {
		case iok && jok:
			return ri < rj
		case iok != jok:
			return iok
		}
		return all[i] < all[j]
	})
	return all
}

// hintKeys are the props that best name a node, in order.
var hintKeys = []string{"navlabel", "text", "title", "label", "alt", "name", "src"}

func hintOf(n *Node) string {
	for _, k := range hintKeys {
		if s, ok := n.P[k].(string); ok && s != "" {
			return clipRunes(s, maxHintRunes)
		}
	}
	if s, ok := n.P["html"].(string); ok && inlineKind(n.T) {
		if s = strings.TrimSpace(InlineText(s)); s != "" {
			return clipRunes(s, maxHintRunes)
		}
	}
	return ""
}

func clipRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}
