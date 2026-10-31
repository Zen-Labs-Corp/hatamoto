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
	v, _ := url.ParseQuery("perm=nonet&hide=0&q=x")
	if got := ParseBrowseQuery(v).WithoutValue("perm", "nonet").Encode(); got != "hide=0&q=x" {
		t.Errorf("multi-key without = %q", got)
	}
	all := []RegistryEntry{{Kind: "plugin", Name: "a", Installed: true}, {Kind: "plugin", Name: "b"}}
	if p := RunBrowse(all, BrowseQuery{HideInstalled: true, Q: "b"}); p.InstalledHidden != 0 {
		t.Errorf("filtered-out installed counted: hidden=%d", p.InstalledHidden)
	}
	if p := RunBrowse(all, BrowseQuery{HideInstalled: true}); p.InstalledHidden != 1 || p.InstalledTotal != 1 || p.Total != 1 {
		t.Errorf("hidden=%d total=%d", p.InstalledHidden, p.InstalledTotal)
	}
}

func themeBrowseEntries() []RegistryEntry {
	return []RegistryEntry{
		{Kind: "theme", Name: "hanshi", Title: "Hanshi", Tier: "first-party", SiteType: "blog", Flags: []string{"dark", "nojs"}, Installed: true, Active: true, Featured: true},
		{Kind: "theme", Name: "ledger", Author: "Acme", SiteType: "docs", Flags: []string{"nojs"}},
		{Kind: "theme", Name: "kiln", Description: "Launch pages", SiteType: "landing", Flags: []string{"builder", "dark"}, Installed: true, Update: true},
		{Kind: "theme", Name: "tavola", SiteType: "restaurant", Flags: []string{"dark", "nojs"}},
		{Kind: "theme", Name: "plain"},
		{Kind: "plugin", Name: "seo", Category: "SEO"},
	}
}

func TestParseBrowseQueryThemeParams(t *testing.T) {
	q := ParseBrowseQuery(url.Values{"type": {"blog", "shop", "docs", "blog"}, "feat": {"dark", "wide", "nojs"}})
	if !slices.Equal(q.Types, []string{"blog", "docs"}) || !slices.Equal(q.Feats, []string{"dark", "nojs"}) {
		t.Fatalf("parsed %+v", q)
	}
	if got := q.Values().Encode(); got != "feat=dark&feat=nojs&type=blog&type=docs" {
		t.Fatalf("values %q", got)
	}
	if !q.Filtered() {
		t.Fatal("type/feat must count as filters")
	}
	if got := q.WithoutValue("type", "blog").Encode(); got != "feat=dark&feat=nojs&type=docs" {
		t.Errorf("without type = %q", got)
	}
	if got := q.WithoutValue("feat", "dark").Encode(); got != "feat=nojs&type=blog&type=docs" {
		t.Errorf("without feat = %q", got)
	}
	back := ParseBrowseQuery(q.Values())
	if !slices.Equal(back.Types, q.Types) || !slices.Equal(back.Feats, q.Feats) {
		t.Errorf("round trip %+v", back)
	}
}

func TestParseThemeBrowseQueryIgnoresPermCat(t *testing.T) {
	q := ParseThemeBrowseQuery(url.Values{"cat": {"SEO"}, "perm": {"nonet"}, "type": {"blog"}, "tier": {"community"}})
	if q.Cats != nil || q.Perms != nil || !slices.Equal(q.Types, []string{"blog"}) || !slices.Equal(q.Tiers, []string{"community"}) {
		t.Fatalf("parsed %+v", q)
	}
	if got := q.Values().Encode(); got != "tier=community&type=blog" {
		t.Fatalf("values %q", got)
	}
}

func TestRunThemeBrowseFilters(t *testing.T) {
	all := themeBrowseEntries()
	cases := []struct {
		name string
		q    BrowseQuery
		want []string
	}{
		{"none, plugins dropped", BrowseQuery{}, []string{"hanshi", "kiln", "ledger", "plain", "tavola"}},
		{"type OR", BrowseQuery{Types: []string{"docs", "restaurant"}}, []string{"ledger", "tavola"}},
		{"feat AND", BrowseQuery{Feats: []string{"dark", "nojs"}}, []string{"hanshi", "tavola"}},
		{"feat single", BrowseQuery{Feats: []string{"builder"}}, []string{"kiln"}},
		{"type and feat", BrowseQuery{Types: []string{"blog", "docs"}, Feats: []string{"nojs"}}, []string{"hanshi", "ledger"}},
		{"type and feat no match", BrowseQuery{Types: []string{"landing"}, Feats: []string{"nojs"}}, nil},
		{"search site type", BrowseQuery{Q: "restaur"}, []string{"tavola"}},
		{"search author", BrowseQuery{Q: "acme"}, []string{"ledger"}},
		{"search description", BrowseQuery{Q: "launch"}, []string{"kiln"}},
		{"search name", BrowseQuery{Q: "HAN"}, []string{"hanshi"}},
		{"tier", BrowseQuery{Tiers: []string{"first-party"}}, []string{"hanshi"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := RunThemeBrowse(all, c.q)
			if got := browseNames(p.Items); !slices.Equal(got, c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
			if p.Total != len(c.want) {
				t.Fatalf("total %d", p.Total)
			}
		})
	}
	if got := browseNames(RunBrowse(all, BrowseQuery{}).Items); !slices.Equal(got, []string{"seo"}) {
		t.Errorf("plugin run saw %v", got)
	}
}

func TestRunThemeBrowseHide(t *testing.T) {
	all := []RegistryEntry{
		{Kind: "theme", Name: "a", Installed: true},
		{Kind: "theme", Name: "b"},
		{Kind: "theme", Name: "c", Installed: true, Update: true},
	}
	for _, tc := range []struct {
		query string
		items []string
		total int
		it    int
		hid   int
	}{
		{"", []string{"b"}, 1, 2, 2},
		{"hide=1", []string{"b"}, 1, 2, 2},
		{"hide=0", []string{"a", "b", "c"}, 3, 2, 0},
	} {
		v, _ := url.ParseQuery(tc.query)
		q := ParseThemeBrowseQuery(v)
		if q.Filtered() {
			t.Errorf("%q: hide default counted as filter", tc.query)
		}
		p := RunThemeBrowse(all, q)
		if !slices.Equal(browseNames(p.Items), tc.items) || p.Total != tc.total || p.InstalledTotal != tc.it || p.InstalledHidden != tc.hid {
			t.Errorf("%q: items %v total %d installed %d hidden %d", tc.query, browseNames(p.Items), p.Total, p.InstalledTotal, p.InstalledHidden)
		}
	}
}

func TestRunThemeBrowseLiveRule(t *testing.T) {
	all := []RegistryEntry{
		{Kind: "theme", Name: "live", Installed: true, Active: true},
		{Kind: "theme", Name: "liveup", Installed: true, Active: true, Update: true},
		{Kind: "theme", Name: "other", Installed: true},
		{Kind: "theme", Name: "fresh"},
	}
	p := RunThemeBrowse(all, BrowseQuery{HideInstalled: true})
	if got := browseNames(p.Items); !slices.Equal(got, []string{"fresh", "live", "liveup"}) {
		t.Fatalf("items %v", got)
	}
	if p.InstalledTotal != 1 || p.InstalledHidden != 1 {
		t.Fatalf("installed %d hidden %d (live must not count)", p.InstalledTotal, p.InstalledHidden)
	}
	// Active has no meaning for plugins.
	pl := RunBrowse([]RegistryEntry{{Kind: "plugin", Name: "x", Installed: true, Active: true}}, BrowseQuery{HideInstalled: true})
	if len(pl.Items) != 0 || pl.InstalledHidden != 1 {
		t.Fatalf("plugin: %+v", pl)
	}
}

func TestRunThemeBrowseFacetCounts(t *testing.T) {
	all := themeBrowseEntries()
	want := []BrowseCount{{"blog", 1}, {"docs", 1}, {"landing", 1}, {"restaurant", 1}}
	all = append(all, RegistryEntry{Kind: "theme", Name: "second", SiteType: "docs"})
	want[1].Count = 2
	for _, q := range []BrowseQuery{{}, {Types: []string{"blog"}}, {Feats: []string{"builder"}, Q: "kiln", HideInstalled: true}} {
		f := RunThemeBrowse(all, q).Facets
		if !f.HasSiteTypes || !f.HasFlags || !slices.Equal(f.SiteTypes, want) || f.HasCategories {
			t.Errorf("%+v: facets %+v", q, f)
		}
	}
	bare := []RegistryEntry{{Kind: "theme", Name: "a"}, {Kind: "theme", Name: "b"}}
	if f := RunThemeBrowse(bare, BrowseQuery{}).Facets; f.HasSiteTypes || f.HasFlags || len(f.SiteTypes) != 0 {
		t.Errorf("facets shown without values: %+v", f)
	}
}

func TestRunThemeBrowseFeatured(t *testing.T) {
	var all []RegistryEntry
	for i := range 14 {
		all = append(all, RegistryEntry{Kind: "theme", Name: fmt.Sprintf("t%02d", i), Featured: i >= 1 && i <= 4})
	}
	if p := RunThemeBrowse(all, BrowseQuery{}); !slices.Equal(browseNames(p.Featured), []string{"t01", "t02", "t03"}) {
		t.Fatalf("featured %v", browseNames(p.Featured))
	}
	if p := RunThemeBrowse(all, BrowseQuery{Types: []string{"blog"}}); len(p.Featured) != 0 {
		t.Fatal("featured shown while filtered")
	}
	if p := RunThemeBrowse(all, BrowseQuery{Page: 2}); len(p.Featured) != 0 {
		t.Fatal("featured shown on page 2")
	}
}

func TestRunBrowseSearchIgnoresSiteType(t *testing.T) {
	all := []RegistryEntry{
		{Kind: "plugin", Name: "alpha", SiteType: "blog"},
		{Kind: "theme", Name: "echo", SiteType: "blog"},
	}
	if got := RunBrowse(all, BrowseQuery{Q: "blog"}); got.Total != 0 {
		t.Errorf("plugin search matched site_type: %d", got.Total)
	}
	if got := RunThemeBrowse(all, BrowseQuery{Q: "blog"}); got.Total != 1 {
		t.Errorf("theme search lost site_type: %d", got.Total)
	}
}
