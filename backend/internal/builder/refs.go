package builder

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"regexp"
	"strings"
)

// Limits on reference expansion (see Engine.expandRefs in render).
const (
	RefMaxDepth = 3    // blocks inside blocks
	RefMaxNodes = 1000 // expanded nodes per rendered page
)

var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// walkNodes visits ns and their descendants in document order.
func walkNodes(ns []*Node, fn func(*Node)) {
	for _, n := range ns {
		if n == nil {
			continue
		}
		fn(n)
		walkNodes(n.C, fn)
	}
}

// RefIDs returns the block ids referenced by d, unique, in document order.
func RefIDs(d *Doc) []string {
	if d == nil {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	walkNodes(d.Nodes, func(n *Node) {
		if n.T != "ref" {
			return
		}
		if id, _ := n.P["block"].(string); id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	})
	return out
}

// SlotCount returns the number of slot nodes in d.
func SlotCount(d *Doc) int {
	if d == nil {
		return 0
	}
	c := 0
	walkNodes(d.Nodes, func(n *Node) {
		if n.T == "slot" {
			c++
		}
	})
	return c
}

// SlotReplaces reports whether d holds a slot node with replace set: the part
// then stands in for the theme's own content markup (archive parts only).
func SlotReplaces(d *Doc) bool {
	if d == nil {
		return false
	}
	found := false
	walkNodes(d.Nodes, func(n *Node) {
		if n.T == "slot" && n.P["replace"] == true {
			found = true
		}
	})
	return found
}

// PrefixIDs returns a deep copy of ns with every id set to prefix+id, so a
// block's nodes never collide with the ids of the page that embeds it. An id
// that would pass 64 characters becomes prefix + 12 hex digits of the hash of
// the original id. The result is deterministic and every id stays valid.
func PrefixIDs(ns []*Node, prefix string) []*Node {
	return mapIDs(ns, func(id string) string {
		if n := prefix + id; len(n) <= 64 {
			return n
		}
		h := sha256.Sum256([]byte(id))
		return prefix + hex.EncodeToString(h[:6])
	})
}

// FreshIDs returns a deep copy of ns with new ids from gen, unique within the
// copy (a repeated id from gen is asked for again).
func FreshIDs(ns []*Node, gen func() string) []*Node {
	used := map[string]bool{}
	return mapIDs(ns, func(string) string {
		for {
			if id := gen(); !used[id] {
				used[id] = true
				return id
			}
		}
	})
}

func mapIDs(ns []*Node, f func(string) string) []*Node {
	if ns == nil {
		return nil
	}
	out := make([]*Node, 0, len(ns))
	for _, n := range ns {
		if n == nil {
			continue
		}
		c := *n
		c.ID = f(n.ID)
		c.P = cloneMap(n.P)
		c.S = cloneStyles(n.S)
		c.B = maps.Clone(n.B)
		c.C = mapIDs(n.C, f)
		out = append(out, &c)
	}
	return out
}

func cloneStyles(s map[string]map[string]string) map[string]map[string]string {
	if s == nil {
		return nil
	}
	out := make(map[string]map[string]string, len(s))
	for bp, m := range s {
		c := make(map[string]string, len(m))
		for k, v := range m {
			c[k] = v
		}
		out[bp] = c
	}
	return out
}

// CheckRefs refuses a doc whose references would loop or nest deeper than
// RefMaxDepth once the doc itself is embedded (render allows RefMaxDepth blocks
// on its stack, and the doc counts as one). selfID is the entity the doc belongs to (a reference to it is a
// cycle). lookup returns the doc of a block id; a missing block is not an
// error here (it renders as a placeholder). The error names the path.
func CheckRefs(selfID string, d *Doc, lookup func(id string) (*Doc, bool)) error {
	best := map[string]int{} // shallowest depth each block was already checked at
	var visit func(d *Doc, path []string) error
	visit = func(d *Doc, path []string) error {
		for _, id := range RefIDs(d) {
			next := append(append([]string(nil), path...), id)
			for _, p := range path {
				if p == id {
					return fmt.Errorf("reference cycle: %s", strings.Join(next, " -> "))
				}
			}
			// The doc checked is a block (or part) that a page embeds, so it already
			// is one level of the render stack: the deepest block render accepts
			// is RefMaxDepth-1 below it.
			depth := len(next) - 1 // path starts with selfID
			if depth >= RefMaxDepth {
				return fmt.Errorf("references nested deeper than %d: %s", RefMaxDepth, strings.Join(next, " -> "))
			}
			if prev, ok := best[id]; ok && prev >= depth {
				continue
			}
			best[id] = depth
			if sub, ok := lookup(id); ok {
				if err := visit(sub, next); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return visit(d, []string{selfID})
}
