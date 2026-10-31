package admin

import (
	"net/url"
	"testing"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/menu"
	"hatamoto.dev/core/internal/siteconfig"
)

func TestLocaleStartWithCopies(t *testing.T) {
	t.Parallel()
	g, site := localeFixture(t)
	site.cfg.Locales = []siteconfig.LocaleEntry{{Tag: "en"}, {Tag: "fr", Segment: "fr"}}
	site.cfg.HomepageEntityID = fid(11)
	cp := newFakeCopier()
	g.a.deps.Content.Copier = cp
	g.a.deps.Content.Groups = newFakeGroups()
	g.a.deps.Content.Entities = newFakeEntities()
	g.a.deps.Content.Entities.(*fakeEntities).ents[fid(11)] = &entity.Entity{ID: fid(11), Locale: "en", Name: "Home"}
	g.a.deps.Content.Entities.(*fakeEntities).ents[fid(12)] = &entity.Entity{ID: fid(12), Locale: "en", Name: "About"}
	g.a.deps.Content.Entities.(*fakeEntities).ents[fid(13)] = &entity.Entity{ID: fid(13), Locale: "en", Name: "Team"}
	ms := newFakeMenus()
	a12, a13 := fid(12), fid(13)
	ms.add("Main", menu.LocationHeader,
		&menu.MenuItem{Label: "About", EntityID: &a12, Children: []*menu.MenuItem{{Label: "Team", EntityID: &a13}, {Label: "Ext"}}},
		&menu.MenuItem{Label: "Home again", EntityID: &a12})
	g.a.deps.Growth.Menus = ms
	cp.errFor[fid(13)] = entity.ErrLocaleTaken
	rec := g.do("POST", "/admin/settings/locales/add", "owner-token", url.Values{"tag": {"it-IT"}, "start": {"copies"}}, nil)
	if rec.Code != 303 {
		t.Fatalf("add: %d", rec.Code)
	}
	if len(cp.calls) != 3 {
		t.Fatalf("calls = %d, want 3 (home, about, team once each)", len(cp.calls))
	}
	has(t, g.do("GET", "/admin/settings/locales/new", "owner-token", nil, nil).Body.String(), `name="start"`, "Start with")
}

func TestLocaleStartHiddenWithoutEntries(t *testing.T) {
	t.Parallel()
	g, _ := localeFixture(t)
	g.a.deps.Content.Copier = newFakeCopier()
	g.a.deps.Content.Entities = newFakeEntities()
	lacks(t, g.do("GET", "/admin/settings/locales/new", "owner-token", nil, nil).Body.String(), `name="start"`)
}
