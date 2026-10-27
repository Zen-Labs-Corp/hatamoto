package builder

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
)

// Op is one edit. The editor sends these verbs; Apply returns the inverse so
// undo is just applying the inverse ops in reverse order.
type Op struct {
	Op     string          `json:"op"` // insert | move | remove | setProp | setStyle | setBind
	ID     string          `json:"id,omitempty"`
	Parent string          `json:"parent,omitempty"` // "" = top level
	Index  int             `json:"index,omitempty"`
	Node   *Node           `json:"node,omitempty"`
	Key    string          `json:"key,omitempty"`
	BP     string          `json:"bp,omitempty"`
	Value  json.RawMessage `json:"value,omitempty"` // setProp: any JSON, null deletes; setStyle, setBind: string, null deletes
}

// Apply applies op and returns its inverse. The doc must be valid before the
// call; it is valid afterwards, and unchanged when Apply returns an error (an
// op that would produce an invalid doc, or break an element-setting rule
// the doc kept before, is rolled back and reported as ErrInvalid).
func (d *Doc) Apply(op Op) (Op, error) {
	cleanBefore := d.CheckSettings() == nil
	inv, err := d.apply(op)
	if err != nil {
		return Op{}, err
	}
	err = d.Validate()
	if err == nil && cleanBefore {
		// An op may not introduce a settings violation; one that was already
		// there (a doc from before the rule) does not block other edits.
		err = d.CheckSettings()
	}
	if err != nil {
		if _, uerr := d.apply(inv); uerr != nil {
			// Unreachable: the inverse of a just-applied op always applies.
			panic(fmt.Sprintf("builder: rollback of %s failed: %v", op.Op, uerr))
		}
		return Op{}, err
	}
	return inv, nil
}

func opErr(format string, a ...any) error {
	return fmt.Errorf("builder: op: %s", fmt.Sprintf(format, a...))
}

func (d *Doc) list(parent string) (*[]*Node, error) {
	if parent == "" {
		return &d.Nodes, nil
	}
	p, _ := d.Find(parent)
	if p == nil {
		return nil, opErr("parent %q cannot hold children", parent)
	}
	// An orphan (plugin widget not installed) keeps its subtree workable.
	r := reg.Load()
	if k := r.kinds[p.T]; !k.children && !isOrphanIn(r, p) {
		return nil, opErr("parent %q cannot hold children", parent)
	}
	return &p.C, nil
}

func indexOf(ns []*Node, id string) int {
	for i, x := range ns {
		if x.ID == id {
			return i
		}
	}
	return -1
}

// contains reports whether id is n or one of its descendants.
func contains(n *Node, id string) bool {
	if n.ID == id {
		return true
	}
	for _, c := range n.C {
		if contains(c, id) {
			return true
		}
	}
	return false
}

func insertAt(l *[]*Node, i int, n *Node) {
	*l = append(*l, nil)
	copy((*l)[i+1:], (*l)[i:])
	(*l)[i] = n
}

func removeAt(l *[]*Node, i int) {
	*l = append((*l)[:i], (*l)[i+1:]...)
}

// parentID returns the id of the parent returned by Find ("" at top level).
func parentID(p *Node) string {
	if p == nil {
		return ""
	}
	return p.ID
}

func (d *Doc) apply(op Op) (Op, error) {
	switch op.Op {
	case "insert":
		if op.Node == nil {
			return Op{}, opErr("insert without node")
		}
		l, err := d.list(op.Parent)
		if err != nil {
			return Op{}, err
		}
		if op.Index < 0 || op.Index > len(*l) {
			return Op{}, opErr("index out of range")
		}
		insertAt(l, op.Index, op.Node.clone())
		return Op{Op: "remove", ID: op.Node.ID}, nil
	case "remove":
		n, p := d.Find(op.ID)
		if n == nil {
			return Op{}, opErr("no such node %q", op.ID)
		}
		l, _ := d.list(parentID(p))
		i := indexOf(*l, op.ID)
		removeAt(l, i)
		return Op{Op: "insert", Parent: parentID(p), Index: i, Node: n}, nil
	case "move":
		n, p := d.Find(op.ID)
		if n == nil {
			return Op{}, opErr("no such node %q", op.ID)
		}
		if op.Parent != "" && contains(n, op.Parent) {
			return Op{}, opErr("cannot move a node into itself")
		}
		dst, err := d.list(op.Parent)
		if err != nil {
			return Op{}, err
		}
		src, _ := d.list(parentID(p))
		i := indexOf(*src, op.ID)
		removeAt(src, i)
		if op.Index < 0 || op.Index > len(*dst) {
			insertAt(src, i, n) // restore
			return Op{}, opErr("index out of range")
		}
		insertAt(dst, op.Index, n)
		return Op{Op: "move", ID: op.ID, Parent: parentID(p), Index: i}, nil
	case "setProp":
		n, _ := d.Find(op.ID)
		if n == nil {
			return Op{}, opErr("no such node %q", op.ID)
		}
		if isOrphanIn(reg.Load(), n) {
			return Op{}, errOrphanEdit
		}
		var v any
		if err := json.Unmarshal(op.Value, &v); err != nil {
			return Op{}, opErr("bad value: %v", err)
		}
		old, had := n.P[op.Key]
		inv := Op{Op: "setProp", ID: op.ID, Key: op.Key, Value: json.RawMessage("null")}
		if had {
			raw, err := json.Marshal(old)
			if err != nil {
				return Op{}, opErr("bad existing value: %v", err)
			}
			inv.Value = raw
		}
		if v == nil {
			delete(n.P, op.Key)
			if len(n.P) == 0 {
				n.P = nil
			}
		} else {
			if n.P == nil {
				n.P = map[string]any{}
			}
			n.P[op.Key] = v
		}
		return inv, nil
	case "setBind":
		n, _ := d.Find(op.ID)
		if n == nil {
			return Op{}, opErr("no such node %q", op.ID)
		}
		if isOrphanIn(reg.Load(), n) {
			return Op{}, errOrphanEdit
		}
		var v *string
		if err := json.Unmarshal(op.Value, &v); err != nil {
			return Op{}, opErr("bind source must be a string or null")
		}
		inv := Op{Op: "setBind", ID: op.ID, Key: op.Key, Value: json.RawMessage("null")}
		if old, ok := n.B[op.Key]; ok {
			inv.Value, _ = json.Marshal(old)
		}
		if v == nil {
			delete(n.B, op.Key)
			if len(n.B) == 0 {
				n.B = nil
			}
		} else {
			if n.B == nil {
				n.B = map[string]string{}
			}
			n.B[op.Key] = *v
		}
		return inv, nil
	case "setStyle":
		n, parent := d.Find(op.ID)
		if n == nil {
			return Op{}, opErr("no such node %q", op.ID)
		}
		if _, _, ok := ParseSKey(op.BP); !ok {
			return Op{}, opErr("bad breakpoint %q", op.BP)
		}
		var v *string
		if err := json.Unmarshal(op.Value, &v); err != nil {
			return Op{}, opErr("style value must be a string or null")
		}
		if v != nil {
			if err := checkStyle(op.BP, op.Key, *v, canParentHover(parent)); err != nil {
				return Op{}, opErr("%v", err)
			}
		}
		inv := Op{Op: "setStyle", ID: op.ID, BP: op.BP, Key: op.Key, Value: json.RawMessage("null")}
		if old, ok := n.S[op.BP][op.Key]; ok {
			inv.Value, _ = json.Marshal(old)
		}
		if v == nil {
			delete(n.S[op.BP], op.Key)
			if len(n.S[op.BP]) == 0 {
				delete(n.S, op.BP)
			}
			if len(n.S) == 0 {
				n.S = nil
			}
		} else {
			if n.S == nil {
				n.S = map[string]map[string]string{}
			}
			if n.S[op.BP] == nil {
				n.S[op.BP] = map[string]string{}
			}
			n.S[op.BP][op.Key] = *v
		}
		return inv, nil
	}
	return Op{}, errors.New("builder: op: unknown verb")
}

// clone deep-copies a node subtree so an op never aliases caller memory.
func (n *Node) clone() *Node {
	if n == nil {
		return nil
	}
	c := &Node{ID: n.ID, T: n.T, P: cloneMap(n.P), B: maps.Clone(n.B)}
	if n.S != nil {
		c.S = make(map[string]map[string]string, len(n.S))
		for bp, m := range n.S {
			cm := make(map[string]string, len(m))
			for k, v := range m {
				cm[k] = v
			}
			c.S[bp] = cm
		}
	}
	if n.C != nil {
		c.C = make([]*Node, len(n.C))
		for i, x := range n.C {
			c.C[i] = x.clone()
		}
	}
	return c
}

func cloneMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	c := make(map[string]any, len(m))
	for k, v := range m {
		c[k] = cloneAny(v)
	}
	return c
}

func cloneAny(v any) any {
	switch x := v.(type) {
	case map[string]any:
		return cloneMap(x)
	case []any:
		c := make([]any, len(x))
		for i, e := range x {
			c[i] = cloneAny(e)
		}
		return c
	}
	return v
}
