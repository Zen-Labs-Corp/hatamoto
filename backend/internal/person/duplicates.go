package person

import (
	"sort"
	"strings"
)

// Duplicates groups the people who look like the same human, for the Merge
// screen. Two people are alike when their names are equal ignoring case and
// spacing, or when their slugs share a first name and one slug starts with
// the other (alex-c and alex-carter). A group holds at least two people, in
// name order; groups are in name order of their first member. Nothing is
// merged here: a person decides.
func Duplicates(people []*Person) [][]*Person {
	parent := make([]int, len(people))
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(i int) int {
		if parent[i] != i {
			parent[i] = find(parent[i])
		}
		return parent[i]
	}
	for i := range people {
		for j := i + 1; j < len(people); j++ {
			if alike(people[i], people[j]) {
				parent[find(i)] = find(j)
			}
		}
	}
	groups := map[int][]*Person{}
	for i, p := range people {
		root := find(i)
		groups[root] = append(groups[root], p)
	}
	var out [][]*Person
	for _, g := range groups {
		if len(g) < 2 {
			continue
		}
		sort.SliceStable(g, func(a, b int) bool { return sortName(g[a]) < sortName(g[b]) })
		out = append(out, g)
	}
	sort.Slice(out, func(a, b int) bool { return sortName(out[a][0]) < sortName(out[b][0]) })
	return out
}

// sortName orders by name, then slug, then ID, so equal names (and groups
// opened by them) never depend on map iteration or random IDs.
func sortName(p *Person) string {
	return normName(p.Name) + "\x00" + strings.ToLower(p.Slug) + "\x00" + p.ID.String()
}

func normName(s string) string { return strings.Join(strings.Fields(strings.ToLower(s)), " ") }

func alike(a, b *Person) bool {
	if n := normName(a.Name); n != "" && n == normName(b.Name) {
		return true
	}
	x, y := strings.ToLower(a.Slug), strings.ToLower(b.Slug)
	if len(x) > len(y) {
		x, y = y, x
	}
	first, _, _ := strings.Cut(x, "-")
	other, _, _ := strings.Cut(y, "-")
	return x != "" && first == other && strings.HasPrefix(y, x)
}
