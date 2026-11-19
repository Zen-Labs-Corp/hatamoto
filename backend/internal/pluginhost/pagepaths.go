package pluginhost

import (
	"maps"
	"slices"

	"hatamoto.dev/sdk"
)

// pageIndex is the render.page path snapshot: every loaded plugin that
// registered the filter, by name, with its page_paths (empty = site-wide).
// Several plugins may declare the same path.
type pageIndex struct {
	byPlugin map[string][]string
	siteWide int            // plugins with no page_paths
	paths    map[string]int // declared path -> number of plugins declaring it
}

func newPageIndex(by map[string][]string) *pageIndex {
	idx := &pageIndex{byPlugin: by, paths: map[string]int{}}
	for _, paths := range by {
		if len(paths) == 0 {
			idx.siteWide++
		}
		for _, p := range paths {
			idx.paths[p]++
		}
	}
	return idx
}

// pagePlugins returns a copy of the current per-plugin map. Callers hold pageMu.
func (r *Runtime) pagePlugins() map[string][]string {
	if idx := r.pageIdx.Load(); idx != nil {
		return maps.Clone(idx.byPlugin)
	}
	return map[string][]string{}
}

// claimPagePaths records m after a load (only if it registered render.page);
// a re-load replaces its entry.
func (r *Runtime) claimPagePaths(m sdk.Manifest, hooksGot []string) {
	r.pageMu.Lock()
	defer r.pageMu.Unlock()
	by := r.pagePlugins()
	delete(by, m.Name)
	if slices.Contains(hooksGot, sdk.RenderPage.Name) {
		by[m.Name] = slices.Clone(m.PagePaths)
	}
	r.pageIdx.Store(newPageIndex(by))
}

// releasePagePaths drops an unloaded plugin.
func (r *Runtime) releasePagePaths(name string) {
	r.pageMu.Lock()
	defer r.pageMu.Unlock()
	by := r.pagePlugins()
	delete(by, name)
	r.pageIdx.Store(newPageIndex(by))
}

// pageFiltered reports whether render.page must run for path, given n filters
// registered on the engine. A filter the runtime has no manifest for (n larger
// than what it registered) makes every page filtered, as before page_paths.
// The comparison counts filters, not names: it assumes every engine render.page
// filter except an unknown one has an index entry. That holds because the only
// removers (Load, Unload, Reregister, grant changes) release or re-add the
// entry at once; a new remover must keep it, or a removed filter would cancel
// out an unknown one.
// Lock-free: one atomic load and a map lookup per request.
func (r *Runtime) pageFiltered(path string, n int) bool {
	if n == 0 {
		return false
	}
	idx := r.pageIdx.Load()
	if idx == nil {
		return true
	}
	return n > len(idx.byPlugin) || idx.siteWide > 0 || idx.paths[path] > 0
}
