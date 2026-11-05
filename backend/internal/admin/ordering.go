package admin

import (
	"github.com/google/uuid"
)

// orderNode is one row of a sibling-ordered tree: its parent, in display order.
type orderNode struct {
	ID     uuid.UUID
	Parent *uuid.UUID
}

// orderPlan is where a move puts a row: under Parent (Clear: at the top
// level) at the 1-based Position among its new siblings; 0 means last.
type orderPlan struct {
	Parent   *uuid.UUID
	Clear    bool
	Position int
}

func sameID(a, b *uuid.UUID) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

// siblingsOf lists the nodes under parent, keeping the display order.
func siblingsOf(all []orderNode, parent *uuid.UUID) []orderNode {
	var out []orderNode
	for _, n := range all {
		if sameID(n.Parent, parent) {
			out = append(out, n)
		}
	}
	return out
}

// planMove turns a button (up, down, indent, outdent) into a move of id within
// all (the rows in display order). ok is false when the button does nothing:
// the first row cannot go up, a top-level row cannot be outdented.
//
// Indent makes the row the last child of the sibling above it; outdent puts it
// under its grandparent, right after the parent it leaves.
func planMove(op string, all []orderNode, id uuid.UUID) (orderPlan, bool) {
	var self *orderNode
	for i := range all {
		if all[i].ID == id {
			self = &all[i]
		}
	}
	if self == nil {
		return orderPlan{}, false
	}
	mine := siblingsOf(all, self.Parent)
	idx := 0
	for i, n := range mine {
		if n.ID == id {
			idx = i
		}
	}
	switch op {
	case "up":
		if idx > 0 {
			return orderPlan{Position: idx}, true
		}
	case "down":
		if idx < len(mine)-1 {
			return orderPlan{Position: idx + 2}, true
		}
	case "indent":
		if idx > 0 {
			prev := mine[idx-1].ID
			return orderPlan{Parent: &prev}, true
		}
	case "outdent":
		if self.Parent == nil {
			return orderPlan{}, false
		}
		var par *orderNode
		for i := range all {
			if all[i].ID == *self.Parent {
				par = &all[i]
			}
		}
		if par == nil {
			return orderPlan{Clear: true}, true
		}
		pidx := 0
		for i, n := range siblingsOf(all, par.Parent) {
			if n.ID == par.ID {
				pidx = i
			}
		}
		return orderPlan{Parent: par.Parent, Clear: par.Parent == nil, Position: pidx + 2}, true
	}
	return orderPlan{}, false
}

// moveOps are the buttons of the Order page, in the order they are drawn.
var moveOps = []struct{ Op, Label string }{
	{"up", "Move up"}, {"down", "Move down"}, {"indent", "Indent"}, {"outdent", "Outdent"},
}
