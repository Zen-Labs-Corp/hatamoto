package admin

import (
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/packages"
)

// browsePageSize is how many cards one Browse page shows.
const browsePageSize = 12

// browseFeaturedMax caps the Featured row.
const browseFeaturedMax = 3

// Browse permission facets: a package passes "nonet" when it can make no
// outgoing requests and "noscripts" when it ships no front-end JavaScript.
const (
	browsePermNoNet     = "nonet"
	browsePermNoScripts = "noscripts"
)

// Browse sort keys; name is the default.
const (
	browseSortName     = "name"
	browseSortNameDesc = "name-desc"
)

// BrowseQuery is the parsed query string of the Browse page.
type BrowseQuery struct {
	Q     string
	Tiers []string
	Cats  []string
	Perms []string
	// Types (site type, OR) and Feats (theme flags, AND) apply to themes only.
	Types, Feats  []string
	HideInstalled bool
	Sort          string
	Page          int
}

// BrowseFacets describes which facet groups apply to the listed entries.
type BrowseFacets struct {
	// Categories are the fixed categories at least one entry uses, in list
	// order; HasCategories is false (hide the facet) when there are none.
	Categories    []string
	HasCategories bool
	// SiteTypes are the site types at least one theme uses with how many
	// themes use each, in packages.SiteTypes order; HasSiteTypes and HasFlags
	// say whether the Site type and Features groups apply. Counts are over the
	// theme list before any search or facet, less the installed themes that
	// hide-installed drops.
	SiteTypes    []BrowseCount
	HasSiteTypes bool
	HasFlags     bool
}

// BrowseCount is a facet value with the number of entries that carry it.
type BrowseCount struct {
	Value string
	Count int
}

// BrowsePage is one page of Browse results.
type BrowsePage struct {
	Items    []RegistryEntry
	Featured []RegistryEntry
	Facets   BrowseFacets
	// Pager carries Page, PerPage and Total; the caller sets BaseURL and Query.
	Pager ui.Pagination
	// Filtered is true when any search or facet is active.
	Filtered bool
	// Total is the number of entries matching the query.
	Total int
	// InstalledTotal counts installed plugins that pass every filter but hide
	// (shown or not); InstalledHidden is how many of them hide drops.
	InstalledTotal, InstalledHidden int
}

// ParseBrowseQuery reads ?q=&tier=&cat=&perm=&type=&feat=&hide=&sort=&page=.
// Installed packages are hidden unless hide=0. Unknown
// facet values are dropped, an unknown sort becomes the default and the page
// is at least 1.
func ParseBrowseQuery(v url.Values) BrowseQuery {
	q := BrowseQuery{
		Q:             strings.TrimSpace(v.Get("q")),
		Tiers:         keepKnown(v["tier"], []string{packages.TierFirstParty, packages.TierCommunity}),
		Cats:          keepKnown(v["cat"], packages.Categories),
		Perms:         keepKnown(v["perm"], []string{browsePermNoNet, browsePermNoScripts}),
		Types:         keepKnown(v["type"], packages.SiteTypes),
		Feats:         keepKnown(v["feat"], packages.ThemeFlags),
		HideInstalled: v.Get("hide") != "0",
		Sort:          browseSortName,
		Page:          1,
	}
	if s := v.Get("sort"); s == browseSortNameDesc {
		q.Sort = s
	}
	if n, err := strconv.Atoi(v.Get("page")); err == nil && n > 1 {
		q.Page = n
	}
	return q
}

// ParseThemeBrowseQuery is ParseBrowseQuery without the plugin-only facets.
func ParseThemeBrowseQuery(v url.Values) BrowseQuery {
	q := ParseBrowseQuery(v)
	q.Cats, q.Perms = nil, nil
	return q
}

// keepKnown returns the distinct values of in that are in known, in input order.
func keepKnown(in, known []string) []string {
	var out []string
	for _, s := range in {
		if slices.Contains(known, s) && !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

// Values is the query as URL values, for building pager and form links. The
// page number is left out.
func (q BrowseQuery) Values() url.Values {
	v := url.Values{}
	if q.Q != "" {
		v.Set("q", q.Q)
	}
	v["tier"] = slices.Clone(q.Tiers)
	v["cat"] = slices.Clone(q.Cats)
	v["perm"] = slices.Clone(q.Perms)
	v["type"] = slices.Clone(q.Types)
	v["feat"] = slices.Clone(q.Feats)
	for _, k := range []string{"tier", "cat", "perm", "type", "feat"} {
		if len(v[k]) == 0 {
			delete(v, k)
		}
	}
	if !q.HideInstalled {
		v.Set("hide", "0")
	}
	if q.Sort != browseSortName {
		v.Set("sort", q.Sort)
	}
	return v
}

// WithoutValue is Values with one value of key dropped (the whole key for a
// single-valued one such as q), for the href of a removable filter chip.
func (q BrowseQuery) WithoutValue(key, val string) url.Values {
	v := q.Values()
	if key == "q" {
		v.Del("q")
		return v
	}
	v[key] = slices.DeleteFunc(v[key], func(s string) bool { return s == val })
	if len(v[key]) == 0 {
		delete(v, key)
	}
	return v
}

// Filtered reports whether any search or facet narrows the list. Sort, page
// and the hide-installed default do not count.
func (q BrowseQuery) Filtered() bool {
	return q.Q != "" || len(q.Tiers) > 0 || len(q.Cats) > 0 || len(q.Perms) > 0 || len(q.Types) > 0 || len(q.Feats) > 0
}

// RunBrowse filters, sorts and pages the plugin entries of the registry.
func RunBrowse(all []RegistryEntry, q BrowseQuery) BrowsePage {
	return runBrowse(all, q, packages.KindPlugin)
}

// RunThemeBrowse filters, sorts and pages the theme entries of the registry.
func RunThemeBrowse(all []RegistryEntry, q BrowseQuery) BrowsePage {
	return runBrowse(all, q, packages.KindTheme)
}

func runBrowse(all []RegistryEntry, q BrowseQuery, kind packages.Kind) BrowsePage {
	var list []RegistryEntry
	for _, e := range all {
		if e.Kind == string(kind) {
			list = append(list, e)
		}
	}
	page := BrowsePage{Facets: browseFacets(list, kind, q.HideInstalled), Filtered: q.Filtered()}

	var matched []RegistryEntry
	for _, e := range list {
		if !browseMatches(e, q, kind) {
			continue
		}
		// The live theme is never counted as installed nor hidden.
		installed := e.Installed && !(kind == packages.KindTheme && e.Active)
		if installed {
			page.InstalledTotal++
			if q.HideInstalled {
				page.InstalledHidden++
			}
		}
		if !(q.HideInstalled && installed) {
			matched = append(matched, e)
		}
	}
	sort.SliceStable(matched, func(i, j int) bool {
		a, b := browseKey(matched[i]), browseKey(matched[j])
		if a != b {
			if q.Sort == browseSortNameDesc {
				return a > b
			}
			return a < b
		}
		return matched[i].Name < matched[j].Name
	})

	total := len(matched)
	pager := ui.Pagination{Page: q.Page, PerPage: browsePageSize, Total: total}
	if last := pager.Last(); pager.Page > last {
		pager.Page = last
	}
	if pager.Page < 1 {
		pager.Page = 1
	}
	from, to := (pager.Page-1)*browsePageSize, min(pager.Page*browsePageSize, total)
	page.Items = matched[from:to]
	page.Pager, page.Total = pager, total

	if !page.Filtered && pager.Page == 1 {
		for _, e := range matched {
			if e.Featured && len(page.Featured) < browseFeaturedMax {
				page.Featured = append(page.Featured, e)
			}
		}
	}
	return page
}

// browseKey is the sort key: the display title, else the name.
func browseKey(e RegistryEntry) string {
	if e.Title != "" {
		return strings.ToLower(e.Title)
	}
	return strings.ToLower(e.Name)
}

func browseFacets(list []RegistryEntry, kind packages.Kind, hideInstalled bool) BrowseFacets {
	var f BrowseFacets
	if kind == packages.KindTheme {
		// Site type counts skip the installed themes the list hides, so a
		// count is what selecting the type shows.
		if hideInstalled {
			list = slices.DeleteFunc(slices.Clone(list), func(e RegistryEntry) bool { return e.Installed && !e.Active })
		}
		for _, st := range packages.SiteTypes {
			if n := countFunc(list, func(e RegistryEntry) bool { return e.SiteType == st }); n > 0 {
				f.SiteTypes = append(f.SiteTypes, BrowseCount{Value: st, Count: n})
			}
		}
		f.HasSiteTypes = len(f.SiteTypes) > 0
		f.HasFlags = slices.ContainsFunc(list, func(e RegistryEntry) bool { return len(e.Flags) > 0 })
		return f
	}
	for _, c := range packages.Categories {
		if slices.ContainsFunc(list, func(e RegistryEntry) bool { return e.Category == c }) {
			f.Categories = append(f.Categories, c)
		}
	}
	f.HasCategories = len(f.Categories) > 0
	return f
}

func countFunc(list []RegistryEntry, fn func(RegistryEntry) bool) int {
	n := 0
	for _, e := range list {
		if fn(e) {
			n++
		}
	}
	return n
}

func browseMatches(e RegistryEntry, q BrowseQuery, kind packages.Kind) bool {
	if q.Q != "" {
		n := strings.ToLower(q.Q)
		hit := false
		fields := []string{e.Name, e.Title, e.Author, e.Description}
		if kind == packages.KindTheme {
			fields = append(fields, e.SiteType)
		}
		for _, s := range fields {
			if strings.Contains(strings.ToLower(s), n) {
				hit = true
				break
			}
		}
		if !hit {
			return false
		}
	}
	if len(q.Tiers) > 0 {
		// An entry that states no tier is community.
		tier := packages.TierCommunity
		if e.Tier == packages.TierFirstParty {
			tier = packages.TierFirstParty
		}
		if !slices.Contains(q.Tiers, tier) {
			return false
		}
	}
	if kind == packages.KindTheme {
		if len(q.Types) > 0 && !slices.Contains(q.Types, e.SiteType) {
			return false
		}
		for _, f := range q.Feats {
			if !slices.Contains(e.Flags, f) {
				return false
			}
		}
		return true
	}
	if len(q.Cats) > 0 && !slices.Contains(q.Cats, e.Category) {
		return false
	}
	for _, p := range q.Perms {
		switch p {
		case browsePermNoNet:
			if hasAnyPerm(e, "http.fetch", "http.fetch.any") {
				return false
			}
		case browsePermNoScripts:
			if hasAnyPerm(e, "site.scripts", "builder.island") {
				return false
			}
		}
	}
	return true
}

func hasAnyPerm(e RegistryEntry, names ...string) bool {
	return slices.ContainsFunc(e.Permissions, func(p string) bool { return slices.Contains(names, p) })
}
