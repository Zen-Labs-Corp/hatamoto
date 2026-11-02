package admin

import (
	"net/url"
	"strings"
)

// PluginNavEntry is one link of the Plugins subnav. ID is the no-JS select's
// option value: empty for the list (never a valid plugin name, so a plugin
// named "all" stays selectable) and the plugin's name otherwise.
type PluginNavEntry struct {
	ID, Href, Label, Marker string
	Muted, Current          bool
}

// isPluginsPath reports whether p is the plugin list or below it.
func isPluginsPath(prefix, p string) bool {
	base := prefix + "/plugins"
	return p == base || strings.HasPrefix(p, base+"/")
}

// pluginsNavFor builds the subnav for role on path: "All plugins" and one
// entry per installed plugin for the owner, only the plugins whose UI admits
// them for an editor. Off and failed plugins are muted with a marker.
func (a *Admin) pluginsNavFor(role, path string) []PluginNavEntry {
	var out []PluginNavEntry
	all := -1
	if role == "owner" {
		all = len(out)
		out = append(out, PluginNavEntry{ID: "", Href: a.url("plugins"), Label: "All plugins", Current: a.isPluginsParentPath(path)})
	}
	pa := a.deps.Growth.Plugins
	if pa == nil {
		return out
	}
	matched := false
	for _, p := range pa.Plugins() {
		if role != "owner" && !uiAllows(p, role) {
			continue
		}
		href := a.url("plugins", url.PathEscape(p.Name))
		st := pluginStatus(p)
		cur := path == href || strings.HasPrefix(path, href+"/")
		matched = matched || cur
		out = append(out, PluginNavEntry{
			ID: p.Name, Href: href + "/settings", Label: p.Name, Marker: st.Marker, Muted: !st.Active,
			Current: cur,
		})
	}
	// A plugin's own page keeps its own entry; "All plugins" is current only
	// for the list and its child pages, never together with a plugin entry.
	if all >= 0 && matched {
		out[all].Current = false
	}
	return out
}

// isPluginsParentPath reports whether path is the plugin list, Browse (and
// its detail pages), Package security or a plugin's About page: the pages
// that highlight "All plugins". The About page yields to a per-plugin entry
// in pluginsNavFor.
func (a *Admin) isPluginsParentPath(path string) bool {
	under := func(rel string) bool {
		base := a.url("plugins", rel)
		return path == base || strings.HasPrefix(path, base+"/")
	}
	return path == a.url("plugins") || under("browse") || under("security") || (isPluginsPath(a.adminPath, path) && strings.HasSuffix(path, "/about"))
}

// PluginsNav is the Plugins subnav the shell renders on /admin/plugins* pages
// (nil elsewhere and for signed-out requests).
func (v *View) PluginsNav() []PluginNavEntry { return v.pluginNav }

// HasSubnav reports whether the shell wraps the page in a secondary sidebar.
func (v *View) HasSubnav() bool {
	return len(v.AreaNav()) > 0 || len(v.pluginNav) > 0 || len(v.entityNav) > 0
}
