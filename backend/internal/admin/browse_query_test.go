package admin

import (
	"fmt"
	"net/url"
	"slices"
	"testing"
)

func browseEntries(n int) []RegistryEntry {
	out := make([]RegistryEntry, n)
	for i := range out {
		out[i] = RegistryEntry{Kind: "plugin", Name: fmt.Sprintf("p%03d", i), Description: "does things"}
	}
	return out
}

func browseNames(es []RegistryEntry) []string {
	var out []string
	for _, e := range es {
		out = append(out, e.Name)
	}
	return out
}

func TestParseBrowseQuery(t *testing.T) {
	q := ParseBrowseQuery(url.Values{
		"q": {"  seo "}, "tier": {"first-party", "bogus", "first-party"}, "cat": {"SEO", "nope"},
		"perm": {"nonet", "x"}, "hide": {"1"}, "sort": {"popular"}, "page": {"0"},
	})
	if q.Q != "seo" || !slices.Equal(q.Tiers, []string{"first-party"}) || !slices.Equal(q.Cats, []string{"SEO"}) ||
		!slices.Equal(q.Perms, []string{"nonet"}) || !q.HideInstalled || q.Sort != "name" || q.Page != 1 {
		t.Fatalf("parsed %+v", q)
	}
	if p := ParseBrowseQuery(url.Values{"page": {"abc"}, "sort": {"name-desc"}}); p.Page != 1 || p.Sort != "name-desc" {
		t.Fatalf("parsed %+v", p)
	}
	if p := ParseBrowseQuery(url.Values{"page": {"7"}}); p.Page != 7 {
		t.Fatalf("page %d", p.Page)
	}
	if got := q.Values().Encode(); got != "cat=SEO&perm=nonet&q=seo&tier=first-party" {
		t.Fatalf("values %q", got)
	}
}

func TestRunBrowseFilters(t *testing.T) {
	all := []RegistryEntry{
		{Kind: "plugin", Name: "alpha", Title: "Alpha SEO", Author: "Acme", Tier: "first-party", Category: "SEO", Permissions: []string{"settings"}},
		{Kind: "plugin", Name: "bravo", Title: "Bravo", Description: "Sends mail", Tier: "community", Category: "Forms", Permissions: []string{"http.fetch"}, Installed: true},
		{Kind: "plugin", Name: "charlie", Permissions: []string{"site.scripts"}},
		{Kind: "plugin", Name: "delta", Permissions: []string{"builder.island", "http.fetch.any"}},
		{Kind: "theme", Name: "echo"},
	}
	cases := []struct {
		name string
		q    BrowseQuery
		want []string
	}{
		{"none, themes dropped", BrowseQuery{}, []string{"alpha", "bravo", "charlie", "delta"}},
		{"search title", BrowseQuery{Q: "alpha"}, []string{"alpha"}},
		{"search author case-insensitive", BrowseQuery{Q: "ACME"}, []string{"alpha"}},
		{"search description", BrowseQuery{Q: "mail"}, []string{"bravo"}},
		{"first-party", BrowseQuery{Tiers: []string{"first-party"}}, []string{"alpha"}},
		{"community includes unstated", BrowseQuery{Tiers: []string{"community"}}, []string{"bravo", "charlie", "delta"}},
		{"both tiers", BrowseQuery{Tiers: []string{"first-party", "community"}}, []string{"alpha", "bravo", "charlie", "delta"}},
		{"category", BrowseQuery{Cats: []string{"Forms"}}, []string{"bravo"}},
		{"no network", BrowseQuery{Perms: []string{"nonet"}}, []string{"alpha", "charlie"}},
		{"no scripts", BrowseQuery{Perms: []string{"noscripts"}}, []string{"alpha", "bravo"}},
		{"both perms", BrowseQuery{Perms: []string{"nonet", "noscripts"}}, []string{"alpha"}},
		{"hide installed", BrowseQuery{HideInstalled: true}, []string{"alpha", "charlie", "delta"}},
		{"combined", BrowseQuery{Q: "a", Tiers: []string{"community"}, Perms: []string{"noscripts"}, HideInstalled: true}, nil},
		{"sort desc", BrowseQuery{Sort: "name-desc", Tiers: []string{"community"}}, []string{"delta", "charlie", "bravo"}},
		{"empty result", BrowseQuery{Q: "zzz"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := RunBrowse(all, c.q)
			if got := browseNames(p.Items); !slices.Equal(got, c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
			if p.Total != len(c.want) {
				t.Fatalf("total %d", p.Total)
			}
		})
	}
}

func TestRunBrowseSortUsesTitleThenName(t *testing.T) {
	all := []RegistryEntry{
		{Kind: "plugin", Name: "zz", Title: "Apple"},
		{Kind: "plugin", Name: "aa", Title: "banana"},
		{Kind: "plugin", Name: "b2", Title: "Cherry"},
		{Kind: "plugin", Name: "b1", Title: "Cherry"},
	}
	if got := browseNames(RunBrowse(all, BrowseQuery{}).Items); !slices.Equal(got, []string{"zz", "aa", "b1", "b2"}) {
		t.Fatalf("asc %v", got)
	}
	if got := browseNames(RunBrowse(all, BrowseQuery{Sort: "name-desc"}).Items); !slices.Equal(got, []string{"b1", "b2", "aa", "zz"}) {
		// equal titles keep name order in both directions
		t.Fatalf("desc %v", got)
	}
}

func TestRunBrowsePaging(t *testing.T) {
	for _, c := range []struct{ n, page, items, last, cur int }{
		{0, 1, 0, 1, 1}, {12, 1, 12, 1, 1}, {13, 1, 12, 2, 1}, {13, 2, 1, 2, 2},
		{25, 3, 1, 3, 3}, {25, 9, 1, 3, 3}, {25, 2, 12, 3, 2},
	} {
		p := RunBrowse(browseEntries(c.n), BrowseQuery{Page: c.page})
		if len(p.Items) != c.items || p.Pager.Last() != c.last || p.Pager.Page != c.cur || p.Total != c.n {
			t.Errorf("n=%d page=%d: items %d last %d page %d total %d", c.n, c.page, len(p.Items), p.Pager.Last(), p.Pager.Page, p.Total)
		}
	}
	p := RunBrowse(browseEntries(13), BrowseQuery{Page: 2})
	if p.Pager.From() != 13 || p.Pager.To() != 13 || browseNames(p.Items)[0] != "p012" {
		t.Fatalf("page 2: %+v", p.Pager)
	}
}

func TestRunBrowseFeatured(t *testing.T) {
	all := browseEntries(20)
	for _, i := range []int{1, 3, 5, 7} {
		all[i].Featured = true
	}
	if p := RunBrowse(all, BrowseQuery{HideInstalled: true}); !slices.Equal(browseNames(p.Featured), []string{"p001", "p003", "p005"}) {
		t.Fatalf("featured %v", browseNames(p.Featured))
	}
	for name, q := range map[string]BrowseQuery{
		"search": {Q: "p"}, "page 2": {Page: 2}, "tier": {Tiers: []string{"community"}},
	} {
		if p := RunBrowse(all, q); len(p.Featured) != 0 {
			t.Errorf("%s: featured shown", name)
		}
	}
	if p := RunBrowse(browseEntries(5), BrowseQuery{}); len(p.Featured) != 0 {
		t.Fatal("featured without any flagged entry")
	}
	if p := RunBrowse(all, BrowseQuery{Sort: "name-desc"}); len(p.Featured) == 0 {
		t.Fatal("sort alone must keep the featured row")
	}
}

func TestRunBrowseCategoryFacet(t *testing.T) {
	if f := RunBrowse(browseEntries(3), BrowseQuery{}).Facets; f.HasCategories {
		t.Fatalf("facet shown without categories: %+v", f)
	}
	all := []RegistryEntry{{Kind: "plugin", Name: "a", Category: "Forms"}, {Kind: "plugin", Name: "b", Category: "SEO"}, {Kind: "plugin", Name: "c"}}
	f := RunBrowse(all, BrowseQuery{Q: "c"}).Facets
	if !f.HasCategories || !slices.Equal(f.Categories, []string{"SEO", "Forms"}) {
		t.Fatalf("facets %+v", f)
	}
}

func TestBrowseQueryHideDefault(t *testing.T) {
	for _, tc := range []struct {
		query string
		hide  bool
		enc   string
	}{
		{"", true, ""},
		{"hide=1", true, ""},
		{"hide=0", false, "hide=0"},
		{"hide=1&hide=0", true, ""},
	} {
		v, _ := url.ParseQuery(tc.query)
		q := ParseBrowseQuery(v)
		if q.HideInstalled != tc.hide || q.Values().Encode() != tc.enc || q.Filtered() {
			t.Errorf("%q: hide=%v values=%q filtered=%v", tc.query, q.HideInstalled, q.Values().Encode(), q.Filtered())
		}
	}
	q := BrowseQuery{HideInstalled: true, Sort: "name", Perms: []string{"nonet", "noscripts"}}
	if got := q.WithoutValue("perm", "nonet").Encode(); got != "perm=noscripts" {
		t.Errorf("without = %q", got)
	}
	all := []RegistryEntry{{Kind: "plugin", Name: "a", Installed: true}, {Kind: "plugin", Name: "b"}}
	if p := RunBrowse(all, BrowseQuery{HideInstalled: true, Q: "b"}); p.InstalledHidden != 0 {
		t.Errorf("filtered-out installed counted: hidden=%d", p.InstalledHidden)
	}
	if p := RunBrowse(all, BrowseQuery{HideInstalled: true}); p.InstalledHidden != 1 || p.InstalledTotal != 1 || p.Total != 1 {
		t.Errorf("hidden=%d total=%d", p.InstalledHidden, p.InstalledTotal)
	}
}
