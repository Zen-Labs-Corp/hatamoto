package seo

import "testing"

func TestExpand(t *testing.T) {
	v := Vars{Title: "Hello", SiteName: "Site", Tagline: "Tag", Page: 2, PageTotal: 5, Year: 2030,
		Term: "Go", Custom: map[string]string{"brand": "Acme", "none": ""}}
	tests := []struct{ tpl, want string }{
		{"%title% %sep% %sitename%", "Hello | Site"},
		{"no variables", "no variables"},
		{"%title% - Page %page% of %pagetotal%", "Hello - Page 2 of 5"},
		{"(c) %currentyear% %cv_brand%", "(c) 2030 Acme"},
		{"%unknown% stays", "%unknown% stays"},
		{"%cv_missing% stays", "%cv_missing% stays"},
		{"100% sure %title%", "100% sure Hello"},
		{"%title% %excerpt% %sep% %sitename%", "Hello | Site"},
		{"%cv_none% %title%", "Hello"},
		{"%title%%", "Hello%"},
		{"a  b %title%", "a  b Hello"},
	}
	for _, tt := range tests {
		if got := Expand(tt.tpl, v, "|"); got != tt.want {
			t.Errorf("Expand(%q) = %q, want %q", tt.tpl, got, tt.want)
		}
	}
}

func TestExpandCurrentYearDefault(t *testing.T) {
	if got := Expand("%currentyear%", Vars{}, "|"); len(got) != 4 {
		t.Fatalf("got %q", got)
	}
}

func TestTitleWithSite(t *testing.T) {
	tests := []struct{ title, site, want string }{
		{"Exact", "Site", "Exact | Site"},
		{"X | Site", "Site", "X | Site"},
		{"X - Site", "Site", "X - Site"},
		{"X – Site", "Site", "X – Site"},
		{"X — Site", "Site", "X — Site"},
		{"X · Site", "Site", "X · Site"},
		{"X : Site", "Site", "X : Site"},
		{"Site", "Site", "Site"},
		{"Exact", "", "Exact"},
		{"Exact", "  ", "Exact"},
	}
	for _, tt := range tests {
		if got := TitleWithSite(tt.title, tt.site, "|"); got != tt.want {
			t.Errorf("TitleWithSite(%q, %q) = %q, want %q", tt.title, tt.site, got, tt.want)
		}
	}
}

// The default template must give the same title as TitleWithSite for every
// title shape, so a site that never opens Settings > SEO sees no change.
func TestResolveDefaultsMatchLegacyTitle(t *testing.T) {
	s := Defaults()
	for _, title := range []string{"Exact", "N", "X | Site", "X - Site", "X – Site", "X — Site", "X · Site", "X : Site", "Site"} {
		for _, site := range []string{"Site", "", " "} {
			in := Input{Kind: KindSingle, SchemaSlug: "page", Vars: Vars{Title: title, SiteName: site}}
			if !IsLegacyTitle(&s, in) {
				t.Fatal("defaults must be legacy")
			}
			want := TitleWithSite(title, site, "|")
			if got := Resolve(&s, in).Title; got != want {
				t.Errorf("title %q site %q: got %q, want %q", title, site, got, want)
			}
		}
	}
}

func TestResolveTemplates(t *testing.T) {
	s := Defaults()
	s.Types["doc"] = TypeTemplates{Title: "%title% %sep% Docs", Description: "%title% on %sitename%"}
	s.Taxonomy["tag"] = "Tag: %term% %sep% %sitename%"
	s.Archives.Search = "Results for %searchphrase%"
	s.Archives.Date = "Archive: %archive%"
	s.Archives.NotFound = "Lost %sep% %sitename%"
	s.Separator = "-"
	v := Vars{Title: "Install", Archive: "May", SiteName: "Site", Term: "go", SearchPhrase: "q"}

	tests := []struct {
		name string
		in   Input
		want Result
	}{
		{"type title and description", Input{Kind: KindSingle, SchemaSlug: "doc", Vars: v}, Result{"Install - Docs", "Install on Site"}},
		{"other type uses default", Input{Kind: KindSingle, SchemaSlug: "page", Vars: v}, Result{"Install - Site", ""}},
		{"taxonomy", Input{Kind: KindTaxonomy, TermKind: "tag", Vars: v}, Result{"Tag: go - Site", ""}},
		{"search", Input{Kind: KindSearch, Vars: v}, Result{"Results for q", ""}},
		{"date archive", Input{Kind: KindDate, Vars: v}, Result{"Archive: May", ""}},
		{"not found", Input{Kind: KindNotFound, Vars: v}, Result{"Lost - Site", ""}},
		{"hand title keeps itself and gets the suffix", Input{Kind: KindSingle, SchemaSlug: "doc", Vars: v, HandTitle: "Mine", TitleSet: true}, Result{"Mine - Site", "Install on Site"}},
		{"hand title already suffixed", Input{Kind: KindSingle, SchemaSlug: "doc", Vars: v, HandTitle: "Mine | Site", TitleSet: true}, Result{"Mine | Site", "Install on Site"}},
		{"hand description wins", Input{Kind: KindSingle, SchemaSlug: "doc", Vars: v, HandDescription: "Mine", DescriptionSet: true}, Result{"Install - Docs", "Mine"}},
	}
	for _, tt := range tests {
		if got := Resolve(&s, tt.in); got != tt.want {
			t.Errorf("%s: got %+v, want %+v", tt.name, got, tt.want)
		}
	}
}

func TestResolveSiteSuffixDedupe(t *testing.T) {
	s := Defaults()
	s.Separator = "»"
	in := Input{Kind: KindHome, Vars: Vars{Title: "Welcome | Site", SiteName: "Site"}}
	if got := Resolve(&s, in).Title; got != "Welcome | Site" {
		t.Fatalf("got %q", got)
	}
	in.Vars.Title = "Welcome"
	if got := Resolve(&s, in).Title; got != "Welcome » Site" {
		t.Fatalf("got %q", got)
	}
}

func TestResolveUsesCustomVariables(t *testing.T) {
	s := Defaults()
	s.Variables["brand"] = "Acme"
	s.Home = "%cv_brand% %sep% %title%"
	got := Resolve(&s, Input{Kind: KindHome, Vars: Vars{Title: "Home"}}).Title
	if got != "Acme | Home" {
		t.Fatalf("got %q", got)
	}
}

func TestIsLegacyTitleFalseWhenCustomised(t *testing.T) {
	s := Defaults()
	in := Input{Kind: KindSingle, SchemaSlug: "doc"}
	s.Types["doc"] = TypeTemplates{Title: "%title%"}
	if IsLegacyTitle(&s, in) {
		t.Fatal("custom template is not legacy")
	}
	s = Defaults()
	s.Separator = "-"
	if IsLegacyTitle(&s, in) {
		t.Fatal("custom separator is not legacy")
	}
}

func TestIsLegacyTitleDoesNotAllocate(t *testing.T) {
	s := Defaults()
	in := Input{Kind: KindSingle, SchemaSlug: "page"}
	if n := testing.AllocsPerRun(100, func() { IsLegacyTitle(&s, in) }); n != 0 {
		t.Fatalf("allocs = %v", n)
	}
}
