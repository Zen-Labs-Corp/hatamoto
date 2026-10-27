package render

import "strings"

// NavPosition locates a path in a menu tree.
type NavPosition struct {
	Trail      []NavItem // ancestors, root first, excluding the current item
	Current    *NavItem
	Prev, Next *NavItem // depth-first neighbours that have an Href
}

// navPos finds path in items (compared after normPath) and returns its
// ancestors and its depth-first neighbours. Parents come before their
// children; items without an Href are skipped as neighbours but may appear in
// Trail. No match, or nil input, gives the zero value.
func navPos(items []NavItem, path string) NavPosition {
	want := normPath(path)
	if want == "" {
		return NavPosition{}
	}
	var flat []flatNav
	flattenNav(items, nil, &flat)
	for i, f := range flat {
		if f.href == "" || f.href != want {
			continue
		}
		cur := f.item
		pos := NavPosition{Trail: f.trail, Current: &cur}
		for j := i - 1; j >= 0; j-- {
			if flat[j].href != "" {
				p := flat[j].item
				pos.Prev = &p
				break
			}
		}
		for j := i + 1; j < len(flat); j++ {
			if flat[j].href != "" {
				n := flat[j].item
				pos.Next = &n
				break
			}
		}
		return pos
	}
	return NavPosition{}
}

type flatNav struct {
	item  NavItem
	href  string // normalised; empty when the item has no link
	trail []NavItem
}

func flattenNav(items []NavItem, trail []NavItem, out *[]flatNav) {
	for _, it := range items {
		*out = append(*out, flatNav{item: it, href: normPath(it.Href), trail: append([]NavItem(nil), trail...)})
		if len(it.Children) > 0 {
			flattenNav(it.Children, append(trail, it), out)
		}
	}
}

// normPath drops a scheme and host, the query and the fragment, and any
// trailing slash (the root stays "/"), so menu hrefs and request paths compare.
func normPath(p string) string {
	p = strings.TrimSpace(p)
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		p = p[:i]
	}
	if i := strings.Index(p, "://"); i >= 0 {
		rest := p[i+3:]
		if j := strings.IndexByte(rest, '/'); j >= 0 {
			p = rest[j:]
		} else {
			p = "/"
		}
	}
	if p == "" {
		return ""
	}
	if t := strings.TrimRight(p, "/"); t != "" {
		return t
	}
	return "/"
}
