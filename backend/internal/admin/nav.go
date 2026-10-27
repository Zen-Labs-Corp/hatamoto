package admin

import (
	"sort"
	"strings"
	"sync"
)

// NavItem is one sidebar link. Roles empty means every signed-in user.
type NavItem struct {
	ID, Label, Href, Icon string
	Order                 int
	// Group is the sidebar section title; consecutive items sharing one are
	// listed together, and "" means no title. Items sharing a Group must have
	// adjacent Order, or GroupNav splits them into separate groups.
	Group string
	Roles []string
	// Prefixes are the paths the item covers: it is the current section when
	// the request path equals one or lies below it (at a "/" boundary). Empty
	// means Href alone.
	Prefixes []string
	// Exact limits the item to Href alone: Overview lives at /admin, which is
	// the parent of every other admin path.
	Exact bool
	// Badge is evaluated per render ("" hides it).
	Badge func() string
	// Visible, when set, decides visibility per role instead of Roles.
	Visible func(role string) bool
	// HrefFor, when set, resolves the link per role instead of Href.
	HrefFor func(role string) string
}

// NavEntry is a NavItem resolved for one user and path. Current is the
// aria-current value: "page" on the link of the page, "" otherwise.
type NavEntry struct {
	Label, Href, Icon, Badge string
	Current                  string
	Group                    string
}

// NavGroup is a run of consecutive entries under one title ("" for none).
type NavGroup struct {
	Label string
	Items []NavEntry
}

// GroupNav folds consecutive entries with the same Group into NavGroups. A
// group with no visible entry never appears, since it is derived from entries.
// Entries sharing a Group must be adjacent (items need adjacent Order).
func GroupNav(entries []NavEntry) []NavGroup {
	var out []NavGroup
	for _, e := range entries {
		if n := len(out); n > 0 && out[n-1].Label == e.Group {
			out[n-1].Items = append(out[n-1].Items, e)
			continue
		}
		out = append(out, NavGroup{Label: e.Group, Items: []NavEntry{e}})
	}
	return out
}

// Nav is the sidebar registry. Core registers its six primary items; none has
// children (plugin pages live in the Plugins subnav).
type Nav struct {
	mu    sync.RWMutex
	items []NavItem
	// providers add items computed per render (plugin admin pages come and go
	// as plugins are installed).
	providers []func() []NavItem
}

// AddProvider registers a function whose items join the registered ones on
// every render.
func (n *Nav) AddProvider(f func() []NavItem) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.providers = append(n.providers, f)
}

// all is the registered items plus the providers' current ones.
func (n *Nav) all() []NavItem {
	out := append([]NavItem(nil), n.items...)
	for _, f := range n.providers {
		out = append(out, f()...)
	}
	return out
}

// Register adds an item (a duplicate ID replaces the earlier one).
func (n *Nav) Register(it NavItem) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for i := range n.items {
		if n.items[i].ID == it.ID {
			n.items[i] = it
			return
		}
	}
	n.items = append(n.items, it)
}

// pluginsID is the Plugins entry.
const pluginsID = "plugins"

// update changes the registered item id in place; it reports whether it exists.
func (n *Nav) update(id string, f func(*NavItem)) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	for i := range n.items {
		if n.items[i].ID == id {
			f(&n.items[i])
			return true
		}
	}
	return false
}

// matchLen is the length of the longest of the item's prefixes that covers
// path at a "/" boundary, or -1.
func (it NavItem) matchLen(path string) int {
	if it.Exact {
		if path == it.Href || path == it.Href+"/" {
			return len(path)
		}
		return -1
	}
	best := -1
	ps := it.Prefixes
	if len(ps) == 0 {
		ps = []string{it.Href}
	}
	for _, p := range ps {
		if (path == p || strings.HasPrefix(path, p+"/")) && len(p) > best {
			best = len(p)
		}
	}
	return best
}

func (it NavItem) allows(role string) bool {
	if it.Visible != nil {
		return it.Visible(role)
	}
	return len(it.Roles) == 0 || contains(it.Roles, role)
}

// For resolves the primary entries visible to role and marks the one covering
// path. The most specific prefix wins, so /admin/plugins lights up
// Plugins and not Settings.
func (n *Nav) For(role, path string) []NavEntry {
	n.mu.RLock()
	defer n.mu.RUnlock()
	top := n.all()
	sort.SliceStable(top, func(i, j int) bool { return top[i].Order < top[j].Order })

	type cand struct {
		e     NavEntry
		match int
	}
	var cands []cand
	best := -1
	for _, it := range top {
		if !it.allows(role) {
			continue
		}
		e := NavEntry{Label: it.Label, Href: it.Href, Icon: it.Icon, Group: it.Group}
		if it.HrefFor != nil {
			e.Href = it.HrefFor(role)
		}
		if it.Badge != nil {
			e.Badge = it.Badge()
		}
		m := it.matchLen(path)
		cands = append(cands, cand{e, m})
		best = max(best, m)
	}
	out := make([]NavEntry, 0, len(cands))
	for _, c := range cands {
		if best >= 0 && c.match == best {
			c.e.Current = "page"
		}
		out = append(out, c.e)
	}
	return out
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// defaultNav registers the core sidebar under prefix: Overview, then Site
// (Entities, AI analytics) and Customize (Plugins, Theme, Settings). Theme and
// Settings carry a secondary sidebar of their own (see adminArea).
func defaultNav(prefix string) *Nav {
	n := &Nav{}
	owner := []string{"owner"}
	for i, it := range []NavItem{
		{ID: "overview", Label: "Overview", Href: prefix, Icon: "home", Exact: true},
		{ID: "entities", Label: "Entities", Group: "Site", Href: prefix + "/entities", Icon: "file-text", Prefixes: []string{prefix + "/entities", prefix + "/people"}},
		{ID: "analytics", Label: "AI analytics", Group: "Site", Href: prefix + "/analytics", Icon: "analytics", Prefixes: []string{prefix + "/analytics"}},
		{ID: pluginsID, Label: "Plugins", Group: "Customize", Href: prefix + "/plugins", Icon: "puzzle", Roles: owner, Prefixes: []string{prefix + "/plugins"}},
		{ID: "theme", Label: "Theme", Group: "Customize", Href: prefix + "/theme", Icon: "palette", Visible: func(role string) bool { return len(themeFor(prefix, role)) > 0 }, Prefixes: []string{prefix + "/theme"}},
		{ID: "settings", Label: "Settings", Group: "Customize", Href: prefix + "/settings", Icon: "settings", Visible: func(role string) bool { return len(settingsFor(prefix, role)) > 0 }, Prefixes: []string{
			prefix + "/settings", prefix + "/media", // legacy-path-ok: media detail pages live under <prefix>/media/{id}
		}},
	} {
		it.Order = i
		n.Register(it)
	}
	return n
}
