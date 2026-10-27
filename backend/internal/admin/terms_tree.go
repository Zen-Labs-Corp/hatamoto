package admin

import (
	"context"
	"sort"
	"strings"

	"github.com/google/uuid"
)

// termNode is one term in tree order: its depth below the root of its kind.
type termNode struct {
	ID          uuid.UUID
	ParentID    *uuid.UUID
	Kind, Name  string
	Slug        string
	Description string
	Depth       int
	Count       int
	HasChildren bool
}

// maxTermPages bounds how many pages of 200 terms the admin loads for a tree.
const maxTermPages = 25

// termNodes loads every term and orders them kind by kind, each term followed
// by its children (alphabetical).
func (a *Admin) termNodes(ctx context.Context) ([]termNode, error) {
	return a.termNodesOf(ctx, "")
}

func (a *Admin) termNodesOf(ctx context.Context, kind string) ([]termNode, error) {
	ts := a.deps.Content.Terms
	if ts == nil {
		return nil, nil
	}
	var all []termNode
	for page := 1; page <= maxTermPages; page++ {
		res, err := ts.ListTerms(ctx, kind, "", page, 200)
		if err != nil {
			return nil, err
		}
		for _, t := range res.Items {
			all = append(all, termNode{ID: t.ID, ParentID: t.ParentID, Kind: t.Kind, Name: t.Name, Slug: t.Slug, Description: t.Description, Count: t.EntityCount})
		}
		if len(res.Items) == 0 || page*200 >= res.Total {
			break
		}
	}
	return orderTerms(all), nil
}

// orderTerms sorts nodes into tree order and fills Depth and HasChildren. A
// term whose parent is missing is treated as a root, so bad data never hides it.
func orderTerms(all []termNode) []termNode {
	byID := map[uuid.UUID]int{}
	for i, n := range all {
		byID[n.ID] = i
	}
	kids := map[uuid.UUID][]int{}
	var roots []int
	for i, n := range all {
		if n.ParentID != nil {
			if _, ok := byID[*n.ParentID]; ok {
				kids[*n.ParentID] = append(kids[*n.ParentID], i)
				continue
			}
		}
		roots = append(roots, i)
	}
	less := func(s []int) {
		sort.SliceStable(s, func(x, y int) bool {
			a, b := all[s[x]], all[s[y]]
			if a.Kind != b.Kind {
				return a.Kind < b.Kind
			}
			return strings.ToLower(a.Name) < strings.ToLower(b.Name)
		})
	}
	less(roots)
	out := make([]termNode, 0, len(all))
	seen := map[uuid.UUID]bool{}
	var walk func(i, depth int)
	walk = func(i, depth int) {
		n := all[i]
		if seen[n.ID] {
			return
		}
		seen[n.ID] = true
		n.Depth = depth
		n.HasChildren = len(kids[n.ID]) > 0
		out = append(out, n)
		c := kids[n.ID]
		less(c)
		for _, ci := range c {
			walk(ci, depth+1)
		}
	}
	for _, r := range roots {
		walk(r, 0)
	}
	return out
}
