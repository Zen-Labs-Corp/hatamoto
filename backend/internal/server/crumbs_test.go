package server

import (
	"reflect"
	"testing"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/seo"
)

func TestShapeCrumbs(t *testing.T) {
	t.Parallel()
	home := BreadcrumbItem{Href: "/"}
	entry := BreadcrumbItem{Label: "Post", Href: "/blog/post"}
	arch := &BreadcrumbItem{Label: "Blog", Href: "/blog"}
	term := &BreadcrumbItem{Label: "News", Href: "/category/news"}
	for _, tc := range []struct {
		name string
		in   crumbInput
		want []BreadcrumbItem
	}{
		{"zero settings", crumbInput{Home: home, Entry: entry, Archive: arch},
			[]BreadcrumbItem{{Label: "Home", Href: "/", defaultHome: true}, {Label: "Blog", Href: "/blog"}, {Label: "Post", Href: "/blog/post"}}},
		{"no archive", crumbInput{Home: home, Entry: entry},
			[]BreadcrumbItem{{Label: "Home", Href: "/", defaultHome: true}, {Label: "Post", Href: "/blog/post"}}},
		{"home label", crumbInput{Home: home, Entry: entry, Cfg: seo.Breadcrumbs{HomeLabel: "Start"}},
			[]BreadcrumbItem{{Label: "Start", Href: "/"}, {Label: "Post", Href: "/blog/post"}}},
		{"term between archive and entry", crumbInput{Home: home, Entry: entry, Archive: arch, Term: term},
			[]BreadcrumbItem{{Label: "Home", Href: "/", defaultHome: true}, {Label: "Blog", Href: "/blog"}, {Label: "News", Href: "/category/news"}, {Label: "Post", Href: "/blog/post"}}},
		{"entry title", crumbInput{Home: home, Entry: entry, Title: "  Short  "},
			[]BreadcrumbItem{{Label: "Home", Href: "/", defaultHome: true}, {Label: "Short", Href: "/blog/post"}}},
		{"blank title keeps name", crumbInput{Home: home, Entry: entry, Title: "  "},
			[]BreadcrumbItem{{Label: "Home", Href: "/", defaultHome: true}, {Label: "Post", Href: "/blog/post"}}},
	} {
		if got := shapeCrumbs(tc.in); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestPrimaryTerm(t *testing.T) {
	t.Parallel()
	terms := []crumbTerm{
		{ID: "t1", Kind: "tag", Name: "Go", Href: "/tag/go"},
		{ID: "c2", Kind: "category", Name: "Zeta", Href: "/category/zeta"},
		{ID: "c1", Kind: "category", Name: "Alpha", Href: "/category/alpha"},
	}
	for _, tc := range []struct {
		name, pick, want string
	}{
		{"picked wins", "t1", "Go"},
		{"detached pick falls back to first category", "gone", "Alpha"},
		{"no pick", "", "Alpha"},
	} {
		got := primaryTerm(tc.pick, terms)
		if got == nil || got.Label != tc.want {
			t.Errorf("%s: got %v, want %s", tc.name, got, tc.want)
		}
	}
	if got := primaryTerm("", terms[:1]); got != nil {
		t.Errorf("tags only: got %v, want none", got)
	}
	if got := primaryTerm("x", nil); got != nil {
		t.Errorf("no terms: got %v, want none", got)
	}
}

func TestLocaleRootCrumb(t *testing.T) {
	t.Parallel()
	multi, err := entity.NewLocaleSet("en", []entity.LocaleEntry{{Tag: "en"}, {Tag: "fr", Segment: "fr", Name: "Francais"}, {Tag: "es", Segment: "es"}}, false, "")
	if err != nil {
		t.Fatal(err)
	}
	one, err := entity.NewLocaleSet("en", []entity.LocaleEntry{{Tag: "en"}}, false, "")
	if err != nil {
		t.Fatal(err)
	}
	entry := BreadcrumbItem{Label: "Post", Href: "/fr/blog/post"}
	for _, tc := range []struct {
		name  string
		set   *entity.LocaleSet
		tag   string
		saved string
		want  []BreadcrumbItem
	}{
		{"prefixed locale", multi, "fr", "", []BreadcrumbItem{{Label: "Francais", Href: "/fr"}, {Label: "Post", Href: "/fr/blog/post"}}},
		{"prefixed locale, name fallback", multi, "es", "", []BreadcrumbItem{{Label: "Spanish", Href: "/fr"}, {Label: "Post", Href: "/fr/blog/post"}}},
		{"saved label loses on a prefixed locale", multi, "fr", "Start", []BreadcrumbItem{{Label: "Francais", Href: "/fr"}, {Label: "Post", Href: "/fr/blog/post"}}},
		{"unprefixed default locale", multi, "en", "", []BreadcrumbItem{{Label: "Home", Href: "/fr", defaultHome: true}, {Label: "Post", Href: "/fr/blog/post"}}},
		{"saved label on the default locale", multi, "en", "Start", []BreadcrumbItem{{Label: "Start", Href: "/fr"}, {Label: "Post", Href: "/fr/blog/post"}}},
		{"one locale", one, "en", "", []BreadcrumbItem{{Label: "Home", Href: "/fr", defaultHome: true}, {Label: "Post", Href: "/fr/blog/post"}}},
		{"shared page", multi, entity.LocaleShared, "", []BreadcrumbItem{{Label: "Home", Href: "/fr", defaultHome: true}, {Label: "Post", Href: "/fr/blog/post"}}},
		{"nil set", nil, "fr", "", []BreadcrumbItem{{Label: "Home", Href: "/fr", defaultHome: true}, {Label: "Post", Href: "/fr/blog/post"}}},
	} {
		got := shapeCrumbs(crumbInput{
			Home:      BreadcrumbItem{Href: "/fr"},
			HomeLabel: localeRootLabel(tc.set, tc.tag),
			Entry:     entry,
			Cfg:       seo.Breadcrumbs{HomeLabel: tc.saved},
		})
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}
