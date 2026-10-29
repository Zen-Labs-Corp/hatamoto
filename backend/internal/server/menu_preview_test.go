package server

import (
	"bytes"
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/menu"
	"hatamoto.dev/core/internal/render"
)

// previewRepo serves published entities by id; resolveRepo is never reached
// for the default page pattern.
type previewRepo struct {
	resolveRepo
	published map[uuid.UUID]*entity.Entity
}

func (p previewRepo) GetPublishedByID(_ context.Context, id uuid.UUID) (*entity.Entity, error) {
	if e, ok := p.published[id]; ok {
		return e, nil
	}
	return nil, entity.ErrEntityNotFound
}

func previewTree(published, draft uuid.UUID) []*menu.MenuItem {
	url := "/docs"
	return []*menu.MenuItem{
		{Label: "Docs", LinkType: menu.LinkTypeCustom, URL: &url, Position: 0, Children: []*menu.MenuItem{
			{Label: "About", LinkType: menu.LinkTypeEntity, EntityID: &published, Position: 1},
			{Label: "Draft", LinkType: menu.LinkTypeEntity, EntityID: &draft, Position: 2},
		}},
		{Label: "Group", LinkType: menu.LinkTypeNone, Position: 3},
	}
}

func TestMenuPreviewViewSetsOrdinalsAndResolvesPublishedPages(t *testing.T) {
	engine, err := render.New(render.Options{})
	if err != nil {
		t.Fatal(err)
	}
	pub, draft := uuid.New(), uuid.New()
	links := &navLinks{
		snap: entity.DefaultURLSnapshot(),
		repo: previewRepo{published: map[uuid.UUID]*entity.Entity{pub: {ID: pub, Slug: "about", Schema: &entity.EntitySchema{Slug: "page"}}}},
	}
	site := render.SiteView{Name: "Site", Nav: []render.NavItem{{Label: "Stored", Href: "/stored"}}, FooterNav: []render.NavItem{{Label: "StoredFooter", Href: "/sf"}}}
	render1 := func(location string) string {
		v := menuPreviewView(context.Background(), links, site, "https://x.io", location, &menu.Menu{Items: previewTree(pub, draft)})
		var buf bytes.Buffer
		if err := engine.RenderPage(&buf, v); err != nil {
			t.Fatal(err)
		}
		return buf.String()
	}

	header := render1(menu.LocationHeader)
	for _, want := range []string{`id="menu-pv-0"`, `id="menu-pv-1"`, `id="menu-pv-2"`, `id="menu-pv-3"`, `href="/about"`, ">Docs<", "StoredFooter"} {
		if !strings.Contains(header, want) {
			t.Errorf("header preview misses %q", want)
		}
	}
	if strings.Contains(header, "Stored<") {
		t.Error("the stored header menu must be replaced")
	}
	if strings.Contains(header, `>Draft</a>`) {
		t.Error("an unpublished page must render as text, not a link")
	}
	if !strings.Contains(header, "noindex") {
		t.Error("preview must be noindex")
	}
	footer := render1(menu.LocationFooter)
	if !strings.Contains(footer, `id="menu-pv-1"`) || !strings.Contains(footer, "Stored<") || strings.Contains(footer, "StoredFooter") {
		t.Error("footer preview must replace only the footer menu")
	}
	if other := render1(""); strings.Contains(other, "menu-pv-") {
		t.Error("a menu without a header or footer location shows nowhere")
	}
}

func TestPublicNavItemsKeepTheirOwnIDs(t *testing.T) {
	// The preview ids are set by the preview path only: the shared mapping
	// leaves NavItem.ID to the item's own setting.
	l := &navLinks{snap: entity.DefaultURLSnapshot()}
	url := "/a"
	n := l.items([]*menu.MenuItem{{Label: "A", LinkType: menu.LinkTypeCustom, URL: &url, Position: 7}})[0]
	if n.ID != "" {
		t.Errorf("public nav item id = %q", n.ID)
	}
}

func TestRenderMenuPreviewWithoutEngine(t *testing.T) {
	s := &Server{}
	_, _, err := s.renderMenuPreview(httptest.NewRequest("POST", "/admin/theme/menus/preview", nil), "header", &menu.Menu{})
	if err != admin.ErrMenuPreviewOff {
		t.Errorf("err = %v", err)
	}
}
