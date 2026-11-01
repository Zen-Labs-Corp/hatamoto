package seo

import "testing"

func socialSettings() *Settings {
	s := Defaults()
	s.Social.Types = map[string]SocialType{
		"doc":  {Title: "%title% %sep% Docs", Image: "/doc.png"},
		"post": {Title: "%title%"},
	}
	s.Social.Taxonomy = map[string]string{"tag": "Tagged %term%"}
	return &s
}

func TestSocialTitle(t *testing.T) {
	t.Parallel()
	s := socialSettings()
	vars := Vars{Title: "Doc", Term: "Go", SiteName: "Site"}
	cases := []struct {
		name string
		in   Input
		want string
	}{
		{"single template with sep", Input{Kind: KindSingle, SchemaSlug: "doc", Vars: vars}, "Doc | Docs"},
		{"page template", Input{Kind: KindPage, SchemaSlug: "doc", Vars: vars}, "Doc | Docs"},
		{"identity template", Input{Kind: KindSingle, SchemaSlug: "post", Vars: vars}, ""},
		{"no template", Input{Kind: KindSingle, SchemaSlug: "other", Vars: vars}, ""},
		{"taxonomy", Input{Kind: KindTaxonomy, TermKind: "tag", Vars: vars}, "Tagged Go"},
		{"taxonomy unset", Input{Kind: KindTaxonomy, TermKind: "cat", Vars: vars}, ""},
		{"home", Input{Kind: KindHome, Vars: vars}, ""},
		{"archive", Input{Kind: KindArchive, Vars: vars}, ""},
	}
	for _, tc := range cases {
		if got := SocialTitle(s, tc.in); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestSocialTitleUsesSeparator(t *testing.T) {
	t.Parallel()
	s := socialSettings()
	s.Separator = "-"
	got := SocialTitle(s, Input{Kind: KindSingle, SchemaSlug: "doc", Vars: Vars{Title: "Doc"}})
	if got != "Doc - Docs" {
		t.Fatalf("got %q", got)
	}
}

func TestSocialTitleShortCircuitDoesNotAllocate(t *testing.T) {
	s := socialSettings()
	for _, in := range []Input{
		{Kind: KindSingle, SchemaSlug: "post"},
		{Kind: KindSingle, SchemaSlug: "none"},
		{Kind: KindTaxonomy, TermKind: "none"},
	} {
		if n := testing.AllocsPerRun(100, func() { _ = SocialTitle(s, in) }); n != 0 {
			t.Fatalf("%+v: %v allocs", in, n)
		}
	}
}

func TestSocialImage(t *testing.T) {
	t.Parallel()
	s := socialSettings()
	if got := SocialImage(s, Input{Kind: KindSingle, SchemaSlug: "doc"}); got != "/doc.png" {
		t.Fatalf("got %q", got)
	}
	if got := SocialImage(s, Input{Kind: KindSingle, SchemaSlug: "post"}); got != "" {
		t.Fatalf("got %q", got)
	}
	if got := SocialImage(s, Input{Kind: KindTaxonomy, TermKind: "tag"}); got != "" {
		t.Fatalf("got %q", got)
	}
}
