package admin

import (
	"sort"
	"strings"
	"sync"

	"hatamoto.dev/core/authz"
)

// NavItem is one sidebar link. Cap empty means every signed-in user.
type NavItem struct {
	ID, Label, Href, Icon string
	Order                 int
	// Group is the sidebar section title; consecutive items sharing one are
	// listed together, and "" means no title. Items sharing a Group must have
	// adjacent Order, or GroupNav splits them into separate groups.
	Group string
	// Cap is the action the viewer needs to see the item; "" means every
	// signed-in user.
	Cap authz.Action
	// Prefixes are the paths the item covers: it is the current section when
	// the request path equals one or lies below it (at a "/" boundary). Empty
	// means Href alone.
	Prefixes []string
	// Exact limits the item to Href alone: Overview lives at /admin, which is
	// the parent of every other admin path.
	Exact bool
	// Badge is evaluated per render for the viewer ("" hides it).
	Badge func(v viewer) string
	// BadgeLabel words a non-empty badge for the viewer ("3 drafts to review"): it is the
	// marker's title and its screen-reader text.
	BadgeLabel func(v viewer, badge string) string
	// Visible, when set, decides visibility per viewer instead of Cap.
	Visible func(v viewer) bool
	// HrefFor, when set, resolves the link per viewer instead of Href.
	HrefFor func(v viewer) string
}

// NavEntry is a NavItem resolved for one user and path. Current is the
// aria-current value: "page" on the link of the page, "" otherwise.
type NavEntry struct {
	Label, Href, Icon, Badge string
	BadgeLabel               string
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

func (it NavItem) allows(v viewer) bool {
	if it.Visible != nil {
		return it.Visible(v)
	}
	return it.Cap == "" || v.Can(it.Cap)
}

// For resolves the primary entries visible to the viewer and marks the one covering
// path. The most specific prefix wins, so /admin/plugins lights up
// Plugins and not Settings.
func (n *Nav) For(v viewer, path string) []NavEntry {
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
		if !it.allows(v) {
			continue
		}
		e := NavEntry{Label: it.Label, Href: it.Href, Icon: it.Icon, Group: it.Group}
		if it.HrefFor != nil {
			e.Href = it.HrefFor(v)
		}
		if it.Badge != nil {
			e.Badge = it.Badge(v)
		}
		if e.Badge != "" && it.BadgeLabel != nil {
			e.BadgeLabel = it.BadgeLabel(v, e.Badge)
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
// (Entities, Media, Forms, Comments, Analytics) and Customize (Plugins, Theme, Settings). Theme and
// Settings carry a secondary sidebar of their own (see adminArea).
func defaultNav(prefix string) *Nav {
	n := &Nav{}
	for i, it := range []NavItem{
		{ID: "overview", Label: "Overview", Href: prefix, Icon: "home", Exact: true},
		{ID: "entities", Label: "Entities", Group: "Site", Href: prefix + "/entities", Icon: "file-text", Prefixes: []string{prefix + "/entities", prefix + "/people"}},
		// Hidden until wireReviewNav finds review in use (Visible reads cached values only).
		{ID: "review", Label: "Review", Group: "Site", Href: prefix + "/review", Icon: "check", Prefixes: []string{prefix + "/review"}, Visible: func(viewer) bool { return false }},
		{ID: "media", Label: "Media", Group: "Site", Href: prefix + "/media", Icon: "image", Prefixes: []string{prefix + "/media"}},
		{ID: "forms", Label: "Forms", Group: "Site", Href: prefix + "/forms", Icon: "inbox", Prefixes: []string{prefix + "/forms"}},
		// Hidden until wireNavCounts finds a comment service in use (Visible reads a cached count only).
		{ID: "comments", Label: "Comments", Group: "Site", Href: prefix + "/comments", Icon: "quote", Prefixes: []string{prefix + "/comments"}, Visible: func(viewer) bool { return false }},
		{ID: "analytics", Label: "Analytics", Group: "Site", Href: prefix + "/analytics", Icon: "analytics", Prefixes: []string{prefix + "/analytics"}},
		{ID: pluginsID, Label: "Plugins", Group: "Customize", Href: prefix + "/plugins", Icon: "puzzle", Cap: authz.ActionPluginManage, Prefixes: []string{prefix + "/plugins"}},
		{ID: "theme", Label: "Theme", Group: "Customize", Href: prefix + "/theme", Icon: "palette", Visible: func(v viewer) bool { return len(themeFor(prefix, v)) > 0 }, Prefixes: []string{prefix + "/theme"}},
		{ID: "settings", Label: "Settings", Group: "Customize", Href: prefix + "/settings", Icon: "settings", Visible: func(v viewer) bool { return len(settingsFor(prefix, v)) > 0 }, Prefixes: []string{prefix + "/settings"}},
	} {
		it.Order = i
		n.Register(it)
	}
	return n
}
