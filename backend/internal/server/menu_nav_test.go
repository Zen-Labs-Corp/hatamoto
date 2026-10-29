package server

import (
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/menu"
	"hatamoto.dev/core/internal/render"
)

func testMenuDecl() render.MenuDecl {
	return render.MenuDecl{
		Styles: []render.MenuPreset{{ID: "button", Label: "Button", Class: "hm-nav__item--button"}},
		Colors: []render.MenuPreset{{ID: "accent", Label: "Accent", Class: "hm-nav-color--accent"}},
		Badges: []render.MenuPreset{{ID: "new", Label: "New", Class: "hm-nav__badge--new"}},
		Icons:  []render.MenuPreset{{ID: "arrow", Label: "Arrow"}},
	}
}

func TestNavItemsNoLink(t *testing.T) {
	str := func(s string) *string { return &s }
	id := uuid.New()
	l := &navLinks{snap: entity.DefaultURLSnapshot()}
	for name, tc := range map[string]struct {
		it     *menu.MenuItem
		href   string
		noLink bool
	}{
		"none type":            {&menu.MenuItem{LinkType: menu.LinkTypeNone, Label: "Product"}, "#", true},
		"none ignores stray":   {&menu.MenuItem{LinkType: menu.LinkTypeNone, URL: str("/x")}, "#", true},
		"empty entity":         {&menu.MenuItem{LinkType: menu.LinkTypeEntity, EntityID: &id}, "#", true},
		"docs group":           {&menu.MenuItem{LinkType: "custom"}, "#", true},
		"external without url": {&menu.MenuItem{LinkType: "external", URL: str("")}, "#", true},
		"page":                 {&menu.MenuItem{LinkType: menu.LinkTypeEntity, EntityID: &id, EntitySlug: str("about")}, "/about", false},
		"address":              {&menu.MenuItem{LinkType: "external", URL: str("https://example.com/")}, "https://example.com/", false},
	} {
		got := l.items([]*menu.MenuItem{tc.it})[0]
		if got.Href != tc.href || got.NoLink != tc.noLink {
			t.Errorf("%s: href %q noLink %v, want %q %v", name, got.Href, got.NoLink, tc.href, tc.noLink)
		}
	}
}

func TestNavItemsResolvePresets(t *testing.T) {
	str := func(s string) *string { return &s }
	it := &menu.MenuItem{
		LinkType: "external", URL: str("/go"), CSSClasses: str("footer-cta  extra"),
		Attrs: menu.MenuItemAttrs{Style: "button", Color: "accent", Badge: "new", Icon: "arrow",
			ShowOn: "mobile", ID: "cta", Title: "T", AriaLabel: "A", Rel: []string{"nofollow", "me"}},
	}
	l := &navLinks{snap: entity.DefaultURLSnapshot(), decl: testMenuDecl()}
	n := l.items([]*menu.MenuItem{it})[0]
	want := render.NavItem{
		Label: "", Href: "/go", Class: "footer-cta extra hm-nav__item--button hm-nav-color--accent hm-show-mobile",
		ID: "cta", Title: "T", AriaLabel: "A", Rel: "nofollow me", Badge: "New", BadgeClass: "hm-nav__badge--new",
		Icon: "arrow", Show: "mobile",
	}
	if n.Class != want.Class || n.ID != want.ID || n.Title != want.Title || n.AriaLabel != want.AriaLabel ||
		n.Rel != want.Rel || n.Badge != want.Badge || n.BadgeClass != want.BadgeClass || n.Icon != want.Icon ||
		n.Show != want.Show || n.NoLink {
		t.Errorf("got %+v, want %+v", n, want)
	}

	// A theme that declares nothing ignores the ids but keeps the user's classes.
	l.decl = render.MenuDecl{}
	n = l.items([]*menu.MenuItem{it})[0]
	if n.Class != "footer-cta extra hm-show-mobile" || n.Badge != "" || n.BadgeClass != "" || n.Icon != "" {
		t.Errorf("unknown ids not ignored: %+v", n)
	}

	// Unknown ids and an all-empty item add nothing.
	plain := l.items([]*menu.MenuItem{{LinkType: "external", URL: str("/p"), Attrs: menu.MenuItemAttrs{Style: "gone"}}})[0]
	if plain.Href != "/p" || plain.Class != "" || plain.Badge != "" || plain.Icon != "" || plain.NoLink {
		t.Errorf("plain item changed: %+v", plain)
	}
}
