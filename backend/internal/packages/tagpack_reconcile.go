package packages

import (
	"context"
	"slices"
	"sort"
	"strings"

	"hatamoto.dev/core/internal/tags"
)

// tagEntry is what reconcile knows of one stored plugin package's tags,
// cached by package hash so unchanged packages are not read again.
type tagEntry struct {
	sha     string
	vendors []tags.PackVendor
}

// tagState is the registration reconcile applied last.
type tagState struct {
	key   string // sorted name@sha of the packs applied
	cache map[string]tagEntry
}

// reconcileTagPacks makes the tag registry match the enabled, installed,
// unblocked plugin packages that ship tags/: a disabled, blocked or
// uninstalled pack leaves the registry (and so the page policy and the
// Tracking page) in the same pass. One swap per call, only when the wanted set
// changed. A pack that no longer validates is skipped and logged. It reports
// whether the registry changed.
func (m *Manager) reconcileTagPacks(ctx context.Context, rows []Row) bool {
	var wanted []Row
	for _, r := range rows {
		if r.Kind != KindPlugin || !r.Enabled || r.State != StateInstalled {
			continue
		}
		if _, blocked := m.blocked["plugin/"+r.Name]; blocked {
			continue
		}
		wanted = append(wanted, r)
	}
	sort.Slice(wanted, func(i, j int) bool { return wanted[i].Name < wanted[j].Name })

	cache := map[string]tagEntry{}
	var (
		vendors []tags.PackVendor
		keys    []string
	)
	for _, r := range wanted {
		e, ok := m.tagPacks.cache[r.Name]
		if !ok || e.sha != r.SHA256 {
			e = m.readTagPack(ctx, r)
		}
		cache[r.Name] = e
		if len(e.vendors) == 0 {
			continue
		}
		vendors = append(vendors, e.vendors...)
		keys = append(keys, r.Name+"@"+r.SHA256)
	}
	m.tagPacks.cache = cache
	key := strings.Join(keys, ",")
	if key == m.tagPacks.key {
		return false
	}
	tags.SetPacks(vendors)
	m.tagPacks.key = key
	return true
}

// readTagPack loads and validates the tags of one stored plugin package. A
// failure is logged and cached as "no tags" until the package changes.
func (m *Manager) readTagPack(ctx context.Context, r Row) tagEntry {
	e := tagEntry{sha: r.SHA256}
	if l, ok := m.loaded[r.Name]; ok && l.pkg == nil && l.sha == r.SHA256 {
		return e // reconcilePlugins already refused this exact package and logged why
	}
	p := m.loaded[r.Name].pkg
	if p == nil || m.loaded[r.Name].sha != r.SHA256 {
		var err error
		if p, _, err = m.loadUsable(ctx, KindPlugin, r.Name); err != nil {
			m.Logger.Error("packages: read tag pack", "plugin", r.Name, "error", err)
			return tagEntry{} // sha unset: tried again at the next reconcile
		}
	}
	vs, err := p.TagVendors()
	if err != nil {
		m.Logger.Error("packages: tag pack skipped", "plugin", r.Name, "error", err)
		return e
	}
	for _, v := range vs {
		e.vendors = append(e.vendors, tags.PackVendor{Vendor: v, PluginVersion: r.Version})
	}
	return e
}

// tagPackAccess is what a pack's declarations put on the page: per vendor key,
// the CSP origins and the consent category.
type tagPackAccess map[string]struct {
	hosts    []string
	category string
}

func tagPackAccessOf(p *Package) tagPackAccess {
	out := tagPackAccess{}
	vs, err := p.TagVendors()
	if err != nil {
		return out
	}
	for _, v := range vs {
		a := out[v.ID]
		a.category = v.Category
		a.hosts = append(slices.Clone(v.Script), v.Connect...)
		slices.Sort(a.hosts)
		a.hosts = slices.Compact(a.hosts)
		out[v.ID] = a
	}
	return out
}

// newTagAccess compares the installed pack with its update: the origins the
// update adds (a new tag file adds all of its own) and whether a tag the
// installed pack already had moved to another consent category.
func newTagAccess(installed, update *Package) (hosts []string, categoryChanged bool) {
	old, next := tagPackAccessOf(installed), tagPackAccessOf(update)
	for id, n := range next {
		o, had := old[id]
		if had && o.category != n.category {
			categoryChanged = true
		}
		for _, h := range n.hosts {
			if !slices.Contains(o.hosts, h) {
				hosts = append(hosts, h)
			}
		}
	}
	slices.Sort(hosts)
	return slices.Compact(hosts), categoryChanged
}

// tagPackCount is how many tags the published packs hold for a plugin.
func tagPackCount(name string) int {
	n := 0
	for _, v := range tags.Packs() {
		if strings.HasPrefix(v.ID, name+".") {
			n++
		}
	}
	return n
}

// addTagDetail records a tag pack's size and hosts in an audit detail.
func addTagDetail(detail map[string]any, p *Package) {
	vs, err := p.TagVendors()
	if err != nil || len(vs) == 0 {
		return
	}
	var hosts []string
	for _, v := range vs {
		for _, h := range v.Script {
			hosts = append(hosts, strings.TrimPrefix(h, "https://"))
		}
	}
	slices.Sort(hosts)
	detail["tags"] = len(vs)
	detail["tag_hosts"] = slices.Compact(hosts)
}
