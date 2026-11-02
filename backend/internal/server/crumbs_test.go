package server

import (
	"reflect"
	"testing"

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
			[]BreadcrumbItem{{"Home", "/"}, {"Blog", "/blog"}, {"Post", "/blog/post"}}},
		{"no archive", crumbInput{Home: home, Entry: entry},
			[]BreadcrumbItem{{"Home", "/"}, {"Post", "/blog/post"}}},
		{"home label", crumbInput{Home: home, Entry: entry, Cfg: seo.Breadcrumbs{HomeLabel: "Start"}},
			[]BreadcrumbItem{{"Start", "/"}, {"Post", "/blog/post"}}},
		{"term between archive and entry", crumbInput{Home: home, Entry: entry, Archive: arch, Term: term},
			[]BreadcrumbItem{{"Home", "/"}, {"Blog", "/blog"}, {"News", "/category/news"}, {"Post", "/blog/post"}}},
		{"entry title", crumbInput{Home: home, Entry: entry, Title: "  Short  "},
			[]BreadcrumbItem{{"Home", "/"}, {"Short", "/blog/post"}}},
		{"blank title keeps name", crumbInput{Home: home, Entry: entry, Title: "  "},
			[]BreadcrumbItem{{"Home", "/"}, {"Post", "/blog/post"}}},
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
