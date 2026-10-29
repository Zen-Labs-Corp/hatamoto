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
	Q             string
	Tiers         []string
	Cats          []string
	Perms         []string
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
}

// ParseBrowseQuery reads ?q=&tier=&cat=&perm=&hide=&sort=&page=. Unknown
// facet values are dropped, an unknown sort becomes the default and the page
// is at least 1.
func ParseBrowseQuery(v url.Values) BrowseQuery {
	q := BrowseQuery{
		Q:             strings.TrimSpace(v.Get("q")),
		Tiers:         keepKnown(v["tier"], []string{packages.TierFirstParty, packages.TierCommunity}),
		Cats:          keepKnown(v["cat"], packages.Categories),
		Perms:         keepKnown(v["perm"], []string{browsePermNoNet, browsePermNoScripts}),
		HideInstalled: v.Get("hide") == "1",
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
	for _, k := range []string{"tier", "cat", "perm"} {
		if len(v[k]) == 0 {
			delete(v, k)
		}
	}
	if q.HideInstalled {
		v.Set("hide", "1")
	}
	if q.Sort != browseSortName {
		v.Set("sort", q.Sort)
	}
	return v
}

// Filtered reports whether any search or facet narrows the list. Sort and
// page do not count.
func (q BrowseQuery) Filtered() bool {
	return q.Q != "" || len(q.Tiers) > 0 || len(q.Cats) > 0 || len(q.Perms) > 0 || q.HideInstalled
}

// RunBrowse filters, sorts and pages the plugin entries of the registry.
func RunBrowse(all []RegistryEntry, q BrowseQuery) BrowsePage {
	var plugins []RegistryEntry
	for _, e := range all {
		if e.Kind == string(packages.KindPlugin) {
			plugins = append(plugins, e)
		}
	}
	page := BrowsePage{Facets: browseFacets(plugins), Filtered: q.Filtered()}

	var matched []RegistryEntry
	for _, e := range plugins {
		if browseMatches(e, q) {
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

func browseFacets(plugins []RegistryEntry) BrowseFacets {
	var f BrowseFacets
	for _, c := range packages.Categories {
		if slices.ContainsFunc(plugins, func(e RegistryEntry) bool { return e.Category == c }) {
			f.Categories = append(f.Categories, c)
		}
	}
	f.HasCategories = len(f.Categories) > 0
	return f
}

func browseMatches(e RegistryEntry, q BrowseQuery) bool {
	if q.HideInstalled && e.Installed {
		return false
	}
	if q.Q != "" {
		n := strings.ToLower(q.Q)
		hit := false
		for _, s := range []string{e.Name, e.Title, e.Author, e.Description} {
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
