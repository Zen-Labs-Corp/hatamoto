package pluginhost

import (
	"maps"
	"slices"
	"strings"

	"hatamoto.dev/sdk"
)

// pageScope is where a render.page plugin runs: exact paths and path
// prefixes (a prefix "/item/" matches "/item/<x>", never "/item"). Empty means
// everywhere. The hook (one plugin) and the index (union of all) share it.
type pageScope struct {
	paths    map[string]int // path -> number of plugins declaring it
	prefixes []string
}

// newPageScope builds a scope; duplicate prefixes are dropped, duplicate
// paths are counted.
func newPageScope(paths, prefixes []string) pageScope {
	s := pageScope{paths: make(map[string]int, len(paths))}
	for _, p := range paths {
		s.paths[p]++
	}
	s.prefixes = slices.Clone(prefixes)
	slices.Sort(s.prefixes)
	s.prefixes = slices.Compact(s.prefixes)
	return s
}

func (s pageScope) everywhere() bool { return len(s.paths) == 0 && len(s.prefixes) == 0 }

// matches is lock-free and allocation-free.
func (s pageScope) matches(path string) bool {
	if s.paths[path] > 0 {
		return true
	}
	for _, p := range s.prefixes {
		if len(path) > len(p) && strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

// pageEntry is one render.page plugin's declarations.
type pageEntry struct{ paths, prefixes, query []string }

// pageIndex is the render.page snapshot: every loaded plugin that registered
// the filter, by name. A plugin with neither page_paths nor page_path_prefixes
// is site-wide. Several plugins may declare the same path.
type pageIndex struct {
	byPlugin map[string]pageEntry
	siteWide int       // plugins with neither page_paths nor page_path_prefixes
	union    pageScope // every declared path (counted) and prefix

	queryKeys []string // sorted, deduped union of every entry.query
}

func newPageIndex(by map[string]pageEntry) *pageIndex {
	idx := &pageIndex{byPlugin: by}
	var paths, prefixes []string
	seen := map[string]bool{}
	for _, e := range by {
		if len(e.paths) == 0 && len(e.prefixes) == 0 {
			idx.siteWide++
		}
		paths = append(paths, e.paths...)
		prefixes = append(prefixes, e.prefixes...)
		for _, k := range e.query {
			if !seen[k] {
				seen[k] = true
				idx.queryKeys = append(idx.queryKeys, k)
			}
		}
	}
	slices.Sort(idx.queryKeys)
	idx.union = newPageScope(paths, prefixes)
	return idx
}

// pagePlugins returns a copy of the current per-plugin map. Callers hold pageMu.
func (r *Runtime) pagePlugins() map[string]pageEntry {
	if idx := r.pageIdx.Load(); idx != nil {
		return maps.Clone(idx.byPlugin)
	}
	return map[string]pageEntry{}
}

// claimPagePaths records m after a load (only if it registered render.page);
// a re-load replaces its entry.
func (r *Runtime) claimPagePaths(m sdk.Manifest, hooksGot []string) {
	r.pageMu.Lock()
	defer r.pageMu.Unlock()
	by := r.pagePlugins()
	delete(by, m.Name)
	if slices.Contains(hooksGot, sdk.RenderPage.Name) {
		by[m.Name] = pageEntry{
			paths:    slices.Clone(m.PagePaths),
			prefixes: slices.Clone(m.PagePathPrefixes),
			query:    slices.Clone(m.PageQuery),
		}
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
// Lock-free: one atomic load, a map lookup and a short prefix loop per request.
func (r *Runtime) pageFiltered(path string, n int) bool {
	if n == 0 {
		return false
	}
	idx := r.pageIdx.Load()
	if idx == nil {
		return true
	}
	return n > len(idx.byPlugin) || idx.siteWide > 0 || idx.union.matches(path)
}
