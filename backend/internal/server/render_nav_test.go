package server

import (
	"testing"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/menu"
)

func TestNavItemsArchiveBySchemaSlug(t *testing.T) {
	str := func(s string) *string { return &s }
	l := &navLinks{snap: entity.DefaultURLSnapshot()}
	for name, tc := range map[string]struct {
		it   *menu.MenuItem
		href string
	}{
		"schema only":           {&menu.MenuItem{LinkType: menu.LinkTypeArchive, SchemaSlug: str("post")}, "/blog"},
		"explicit url wins":     {&menu.MenuItem{LinkType: menu.LinkTypeArchive, SchemaSlug: str("post"), URL: str("/category/news")}, "/category/news"},
		"unknown schema":        {&menu.MenuItem{LinkType: menu.LinkTypeArchive, SchemaSlug: str("nope")}, "#"},
		"archive without hints": {&menu.MenuItem{LinkType: menu.LinkTypeArchive}, "#"},
	} {
		if got := l.items([]*menu.MenuItem{tc.it})[0].Href; got != tc.href {
			t.Errorf("%s: href %q, want %q", name, got, tc.href)
		}
	}
}
