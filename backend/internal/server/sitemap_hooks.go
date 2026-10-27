// Package server — plugin extension point for the public sitemap.
//
// Core derives its
// own groups from the schema URL settings (sitemap_builder.go). Plugins that
// re-add a content type with its own archive listing (e.g. case-study, playbook) register
// an archive group via RegisterArchiveGroup, and plugins that own a
// standalone funnel page register it via
// RegisterStaticSitemapRoute — both from their Wire(), which runs at boot
// before any request is served.
package server

import "sync"

// ArchiveGroupConfig describes a plugin-owned sitemap group.
type ArchiveGroupConfig struct {
	PathPrefix       string
	SchemaSlugs      []string
	ArchiveIndexPath string
}

// SitemapEntry is a plugin-owned static URL for the misc sitemap.
type SitemapEntry struct {
	Path       string
	Priority   float64
	ChangeFreq string
}

// sitemapHooksMu guards the plugin-registered groups and static routes below.
// Registration happens at boot (Wire()), before any request is served, so
// writers never race in practice — but every reader still takes
// sitemapHooksMu.RLock() (via sitemapHooksSnapshot) for safety under -race
// and against a late Wire() call.
var sitemapHooksMu sync.RWMutex

// namedArchiveGroup is a plugin archive group with its registration key (the
// child sitemap name).
type namedArchiveGroup struct {
	Key string
	ArchiveGroupConfig
}

var (
	pluginArchiveGroups []namedArchiveGroup // registration order
	pluginStaticRoutes  []SitemapEntry
)

// RegisterArchiveGroup installs a plugin-owned sitemap archive group. The
// group is merged with the groups derived from schema settings when the
// sitemap is built; re-registering a key replaces its configuration.
func RegisterArchiveGroup(groupKey string, cfg ArchiveGroupConfig) {
	sitemapHooksMu.Lock()
	defer sitemapHooksMu.Unlock()
	for i := range pluginArchiveGroups {
		if pluginArchiveGroups[i].Key == groupKey {
			pluginArchiveGroups[i].ArchiveGroupConfig = cfg
			return
		}
	}
	pluginArchiveGroups = append(pluginArchiveGroups, namedArchiveGroup{Key: groupKey, ArchiveGroupConfig: cfg})
}

// RegisterStaticSitemapRoute installs a plugin-owned
// static URL into the "misc" sub-sitemap.
func RegisterStaticSitemapRoute(e SitemapEntry) {
	sitemapHooksMu.Lock()
	defer sitemapHooksMu.Unlock()
	pluginStaticRoutes = append(pluginStaticRoutes, e)
}

// sitemapPluginHooks is an immutable copy of the plugin registrations.
type sitemapPluginHooks struct {
	Groups []namedArchiveGroup
	Static []SitemapEntry
}

func sitemapHooksSnapshot() sitemapPluginHooks {
	sitemapHooksMu.RLock()
	defer sitemapHooksMu.RUnlock()
	return sitemapPluginHooks{
		Groups: append([]namedArchiveGroup(nil), pluginArchiveGroups...),
		Static: append([]SitemapEntry(nil), pluginStaticRoutes...),
	}
}
