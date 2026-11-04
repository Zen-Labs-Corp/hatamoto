package searchreplace

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/changeset"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/menu"
)

type fakeEnts struct {
	list    []*entity.Entity
	updates []*entity.UpdateEntityInput
	failOn  uuid.UUID
	schema  json.RawMessage
}

func (f *fakeEnts) ListEntitiesWithOffset(_ context.Context, _ *entity.ListEntitiesFilter, p *entity.OffsetPage) (*entity.OffsetPagedResult[*entity.Entity], error) {
	lo := (p.Page - 1) * p.Limit
	hi := min(lo+p.Limit, len(f.list))
	var items []*entity.Entity
	if lo < len(f.list) {
		items = f.list[lo:hi]
	}
	return &entity.OffsetPagedResult[*entity.Entity]{Items: items, Total: len(f.list), Page: p.Page, Limit: p.Limit, TotalPages: (len(f.list) + p.Limit - 1) / p.Limit}, nil
}

func (f *fakeEnts) GetEntity(_ context.Context, id uuid.UUID) (*entity.Entity, error) {
	for _, e := range f.list {
		if e.ID == id {
			return e, nil
		}
	}
	return nil, errors.New("not found")
}

func (f *fakeEnts) GetSchema(context.Context, uuid.UUID) (*entity.EntitySchema, error) {
	return &entity.EntitySchema{Schema: f.schema}, nil
}

func (f *fakeEnts) UpdateEntity(_ context.Context, id uuid.UUID, _ *uuid.UUID, in *entity.UpdateEntityInput) (*entity.Entity, error) {
	e, _ := f.GetEntity(context.Background(), id)
	if id == f.failOn {
		return nil, errors.New("invalid attributes")
	}
	if in.ExpectedVersion != nil && *in.ExpectedVersion != e.Version {
		return nil, &entity.VersionConflictError{Current: e.Version}
	}
	f.updates = append(f.updates, in)
	e.Attributes = *in.Attributes
	e.Version++
	return e, nil
}

type fakeMenus struct {
	items   map[uuid.UUID]*menu.MenuItem
	menu    *menu.Menu
	updated int
}

func (f *fakeMenus) ListMenus(context.Context) ([]*menu.Menu, error) {
	return []*menu.Menu{f.menu}, nil
}
func (f *fakeMenus) GetMenuWithItems(context.Context, uuid.UUID) (*menu.Menu, error) {
	return f.menu, nil
}
func (f *fakeMenus) GetMenuItem(_ context.Context, id uuid.UUID) (*menu.MenuItem, error) {
	return f.items[id], nil
}
func (f *fakeMenus) UpdateMenuItem(_ context.Context, id uuid.UUID, in *menu.UpdateMenuItemInput) (*menu.MenuItem, error) {
	it := f.items[id]
	if in.Label != nil {
		it.Label = *in.Label
	}
	if in.URL != nil {
		it.URL = in.URL
	}
	f.updated++
	return it, nil
}

type fakeOwners map[uuid.UUID]bool

func (f fakeOwners) Get(_ context.Context, id uuid.UUID) (*changeset.Ownership, error) {
	if f[id] {
		return &changeset.Ownership{State: changeset.OwnershipTracked, Plugin: "importer"}, nil
	}
	return &changeset.Ownership{}, nil
}

func ent(name, attrs string) *entity.Entity {
	return &entity.Entity{ID: uuid.New(), Name: name, Locale: "en", Version: 3, Attributes: json.RawMessage(attrs)}
}

func tokens(p *Preview) []string {
	var out []string
	for _, g := range p.Groups {
		for _, m := range g.Matches {
			out = append(out, m.Token)
		}
	}
	return out
}

func TestPreviewAndApplySubset(t *testing.T) {
	a := ent("A", `{"body":"<p>old.example</p>","meta_title":"old.example deals"}`)
	b := ent("B", `{"body":"<p>nothing</p>"}`)
	c := ent("C", `{"title":"old.example"}`)
	ents := &fakeEnts{list: []*entity.Entity{a, b, c}}
	s := &Service{Ents: ents, Owners: fakeOwners{c.ID: true}}
	o := Options{Find: "old.example", Replace: "new.example"}
	all := ScopeContent | ScopeSEO | ScopeOther

	p, err := s.Preview(context.Background(), o, all)
	if err != nil {
		t.Fatal(err)
	}
	if p.Total != 3 || p.TotalItems != 2 || p.Listed != 3 || len(p.Groups) != 2 || p.Capped {
		t.Fatalf("preview %+v", p)
	}
	if !p.Groups[1].PluginOwned || p.Groups[1].Plugin != "importer" || p.Groups[0].PluginOwned {
		t.Errorf("ownership flags %+v", p.Groups)
	}

	// Pick only the body match of A.
	var pick []string
	for _, m := range p.Groups[0].Matches {
		if strings.Contains(m.Label, "Body") {
			pick = append(pick, m.Token)
		}
	}
	rep, err := s.Apply(context.Background(), nil, o, all, pick)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Replaced != 1 || rep.Items != 1 || rep.Skipped != 0 || len(rep.Failed) != 0 {
		t.Fatalf("report %+v", rep)
	}
	if got := string(a.Attributes); !strings.Contains(got, "<p>new.example</p>") || !strings.Contains(got, "old.example deals") {
		t.Errorf("attributes %s", got)
	}
	if len(ents.updates) != 1 || ents.updates[0].Name != nil || ents.updates[0].Slug != nil || *ents.updates[0].ExpectedVersion != 3 {
		t.Errorf("update input %+v", ents.updates[0])
	}
	if got := *ents.updates[0].ChangeSummary; got != `Search and replace: "old.example" -> "new.example"` {
		t.Errorf("summary %q", got)
	}
}

func TestApplySkipsStaleAndCountsFailures(t *testing.T) {
	a := ent("A", `{"title":"old"}`)
	b := ent("B", `{"title":"old"}`)
	ents := &fakeEnts{list: []*entity.Entity{a, b}, failOn: b.ID}
	s := &Service{Ents: ents}
	o := Options{Find: "old", Replace: "new"}
	p, _ := s.Preview(context.Background(), o, ScopeOther)
	a.Version++ // edited after the preview
	rep, err := s.Apply(context.Background(), nil, o, ScopeOther, tokens(p))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Skipped != 1 || len(rep.Failed) != 1 || rep.Failed[0].Name != "B" || rep.Items != 0 {
		t.Fatalf("report %+v", rep)
	}
	if string(a.Attributes) != `{"title":"old"}` {
		t.Errorf("stale entity written: %s", a.Attributes)
	}
}

func TestPreviewCaps(t *testing.T) {
	var list []*entity.Entity
	for range 130 {
		list = append(list, ent("E", `{"title":"x x x x x x x x"}`))
	}
	s := &Service{Ents: &fakeEnts{list: list}}
	p, err := s.Preview(context.Background(), Options{Find: "x"}, ScopeOther)
	if err != nil {
		t.Fatal(err)
	}
	if p.Total != 130*8 || p.Listed != ListCap || p.Capped {
		t.Fatalf("total=%d listed=%d capped=%v", p.Total, p.Listed, p.Capped)
	}
	list = nil
	for range 1300 {
		list = append(list, ent("E", `{"title":"x x x x x x x x"}`))
	}
	s.Ents = &fakeEnts{list: list}
	p, _ = s.Preview(context.Background(), Options{Find: "x"}, ScopeOther)
	if p.Total != CountCap || !p.Capped || p.Listed != ListCap {
		t.Fatalf("total=%d listed=%d capped=%v", p.Total, p.Listed, p.Capped)
	}
}

func TestMenus(t *testing.T) {
	u := "https://old.example/shop"
	mid := uuid.New()
	it := &menu.MenuItem{ID: uuid.New(), MenuID: mid, Label: "Old shop", URL: &u}
	m := &menu.Menu{ID: mid, Name: "Main", Items: []*menu.MenuItem{{ID: uuid.New(), Label: "Home", Children: []*menu.MenuItem{it}}}}
	fm := &fakeMenus{menu: m, items: map[uuid.UUID]*menu.MenuItem{it.ID: it}}
	s := &Service{Ents: &fakeEnts{}, Menus: fm}
	o := Options{Find: "old", Replace: "new"}
	p, err := s.Preview(context.Background(), o, ScopeMenus)
	if err != nil || p.Total != 2 || p.TotalItems != 1 || !p.Groups[0].IsMenu {
		t.Fatalf("preview %+v %v", p, err)
	}
	// Changing the label after the preview skips only that field.
	toks := tokens(p)
	it.Label = "Changed"
	rep, err := s.Apply(context.Background(), nil, o, ScopeMenus, toks)
	if err != nil || rep.Replaced != 1 || rep.MenuItems != 1 || rep.Skipped != 1 {
		t.Fatalf("report %+v %v", rep, err)
	}
	if *it.URL != "https://new.example/shop" || it.Label != "Changed" {
		t.Errorf("item %+v url %s", it, *it.URL)
	}
	// No menu scope: not searched.
	if p, _ := s.Preview(context.Background(), o, ScopeContent|ScopeSEO|ScopeOther); p.Total != 0 {
		t.Errorf("menus searched without scope")
	}
}

func TestParseSelection(t *testing.T) {
	if _, err := ParseSelection([]string{"junk"}); err == nil {
		t.Error("junk accepted")
	}
	if _, err := ParseSelection(make([]string, MaxSelection+1)); err == nil {
		t.Error("over the cap accepted")
	}
	id := uuid.New()
	sel, err := ParseSelection([]string{entityToken(id, 2, "/page_doc/nodes/0/p/a b", 4)})
	if err != nil || !sel.Entities[id].pick("/page_doc/nodes/0/p/a b", 4) || sel.Entities[id].Version != 2 {
		t.Fatalf("%+v %v", sel, err)
	}
}

func TestPreviewAndApplyUseTheSchemaKinds(t *testing.T) {
	a := ent("A", `{"intro":"<p>Joe&#39;s &amp; Co</p>"}`)
	ents := &fakeEnts{list: []*entity.Entity{a}, schema: json.RawMessage(richSchema)}
	s := &Service{Ents: ents}
	o := Options{Find: "Joe's & Co", Replace: "Max"}
	p, err := s.Preview(context.Background(), o, ScopeOther)
	if err != nil || p.Total != 1 {
		t.Fatalf("preview %+v err=%v", p, err)
	}
	rep, err := s.Apply(context.Background(), nil, o, ScopeOther, tokens(p))
	if err != nil || rep.Replaced != 1 || string(a.Attributes) != `{"intro":"<p>Max</p>"}` {
		t.Fatalf("report %+v err=%v attrs %s", rep, err, a.Attributes)
	}
}
