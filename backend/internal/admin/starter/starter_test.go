package starter

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/builder"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/form"
	"hatamoto.dev/core/internal/menu"
	"hatamoto.dev/core/internal/siteconfig"
)

type mem struct {
	schemas  map[string]uuid.UUID
	entities map[string]*entity.Entity
	menus    map[string]*menu.Menu
	items    map[uuid.UUID][]string
	forms    map[string]*form.Form
	site     siteconfig.UpdateInput
	updates  int
	flushed  int
}

func newMem() *mem {
	return &mem{
		schemas:  map[string]uuid.UUID{"page": uuid.New(), "post": uuid.New()},
		entities: map[string]*entity.Entity{}, menus: map[string]*menu.Menu{}, items: map[uuid.UUID][]string{}, forms: map[string]*form.Form{},
	}
}

func (m *mem) GetSchemaBySlug(_ context.Context, slug string) (*entity.EntitySchema, error) {
	if id, ok := m.schemas[slug]; ok {
		return &entity.EntitySchema{ID: id, Slug: slug}, nil
	}
	return nil, entity.ErrSchemaNotFound
}
func (m *mem) GetEntityBySlug(_ context.Context, slug string) (*entity.Entity, error) {
	if e, ok := m.entities[slug]; ok {
		return e, nil
	}
	return nil, entity.ErrEntityNotFound
}
func (m *mem) CreateEntity(_ context.Context, _ *uuid.UUID, in *entity.CreateEntityInput) (*entity.Entity, error) {
	e := &entity.Entity{ID: uuid.New(), SchemaID: in.SchemaID, Name: in.Name, Slug: in.Slug, Attributes: in.Attributes, Status: entity.StatusDraft}
	m.entities[in.Slug] = e
	return e, nil
}
func (m *mem) UpdateEntity(_ context.Context, id uuid.UUID, _ *uuid.UUID, in *entity.UpdateEntityInput) (*entity.Entity, error) {
	for _, e := range m.entities {
		if e.ID == id {
			if in.Attributes != nil {
				e.Attributes = *in.Attributes
			}
			return e, nil
		}
	}
	return nil, entity.ErrEntityNotFound
}
func (m *mem) UpdateEntityStatus(_ context.Context, id uuid.UUID, _ *uuid.UUID, s entity.EntityStatus) (*entity.Entity, error) {
	for _, e := range m.entities {
		if e.ID == id {
			e.Status = s
			return e, nil
		}
	}
	return nil, entity.ErrEntityNotFound
}
func (m *mem) GetMenuByLocation(_ context.Context, loc string) (*menu.Menu, error) {
	if x, ok := m.menus[loc]; ok {
		return x, nil
	}
	return nil, menu.ErrMenuNotFound
}
func (m *mem) GetMenuWithItems(_ context.Context, id uuid.UUID) (*menu.Menu, error) {
	for _, x := range m.menus {
		if x.ID == id {
			c := *x
			c.Items = nil
			for _, l := range m.items[id] {
				c.Items = append(c.Items, &menu.MenuItem{Label: l})
			}
			return &c, nil
		}
	}
	return nil, menu.ErrMenuNotFound
}
func (m *mem) CreateMenu(_ context.Context, in *menu.CreateMenuInput) (*menu.Menu, error) {
	x := &menu.Menu{ID: uuid.New(), Name: in.Name, Slug: in.Slug, Location: in.Location}
	m.menus[*in.Location] = x
	return x, nil
}
func (m *mem) CreateMenuItem(_ context.Context, id uuid.UUID, in *menu.CreateMenuItemInput) (*menu.MenuItem, error) {
	m.items[id] = append(m.items[id], in.Label)
	return &menu.MenuItem{ID: uuid.New()}, nil
}
func (m *mem) GetFormBySlug(_ context.Context, slug string) (*form.Form, error) {
	if f, ok := m.forms[slug]; ok {
		return f, nil
	}
	return nil, form.ErrFormNotFound
}
func (m *mem) CreateForm(_ context.Context, in *form.CreateFormInput) (*form.Form, error) {
	f := &form.Form{ID: uuid.New(), Slug: in.Slug, Fields: in.Fields, Settings: in.Settings}
	m.forms[in.Slug] = f
	return f, nil
}
func (m *mem) Update(_ context.Context, in siteconfig.UpdateInput) (*siteconfig.Config, error) {
	m.site = in
	m.updates++
	return &siteconfig.Config{}, nil
}

func (m *mem) installer() *Installer {
	return &Installer{Entities: m, Menus: m, Forms: m, Site: m, Invalidate: func(context.Context) { m.flushed++ }}
}

func seedHome(m *mem) uuid.UUID {
	e := &entity.Entity{ID: uuid.New(), SchemaID: m.schemas["page"], Name: "My site", Slug: "home", Status: entity.StatusPublished,
		Attributes: json.RawMessage(`{"page_sections":[{"id":"welcome","type":"hero","sort_order":1,"content":{"headline":"My site"}}],"content":"<p>x</p>"}`)}
	m.entities["home"] = e
	return e.ID
}

func TestInstallCreatesEverythingOnceAndReusesTheSeedHome(t *testing.T) {
	m := newMem()
	homeID := seedHome(m)
	in := m.installer()
	res, err := in.Install(context.Background(), Options{SiteName: "Acme <Co>", NotifyEmail: "o@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	// home is reused (counted as updated content), the rest is new.
	if res.Pages != 3 || res.Posts != 2 || res.Menus != 2 || res.MenuItems != 6 || res.Forms != 1 {
		t.Fatalf("%+v", res)
	}
	if len(m.entities) != 5 {
		t.Fatalf("%d entities", len(m.entities))
	}
	if m.entities["home"].ID != homeID {
		t.Error("the seeded home page was duplicated instead of reused")
	}
	for slug, e := range m.entities {
		if e.Status != entity.StatusPublished {
			t.Errorf("%s not published", slug)
		}
	}
	if got := string(m.entities["home"].Attributes); !contains(got, `"starter":true`) || !contains(got, "Acme") || contains(got, `"welcome"`) {
		t.Errorf("home attributes: %s", got)
	}
	var post struct {
		Content string `json:"content"`
	}
	_ = json.Unmarshal(m.entities["hello-world"].Attributes, &post)
	if !contains(post.Content, "Acme &lt;Co&gt;") || contains(post.Content, "<Co>") {
		t.Errorf("site name not escaped in post html: %s", post.Content)
	}
	if m.forms["contact"].Settings.NotifyEmail == nil || *m.forms["contact"].Settings.NotifyEmail != "o@example.com" || len(m.forms["contact"].Fields) != 3 {
		t.Errorf("contact form: %+v", m.forms["contact"])
	}
	if m.site.HomepageEntityID == nil || *m.site.HomepageEntityID != homeID || m.flushed != 1 {
		t.Errorf("homepage / cache: %+v flushed=%d", m.site, m.flushed)
	}
	if got := m.items[m.menus["header"].ID]; len(got) != 4 || got[2] != "Blog" {
		t.Errorf("header menu: %v", got)
	}

	// A second run changes nothing.
	snapshot := map[string]string{}
	for k, e := range m.entities {
		snapshot[k] = string(e.Attributes)
	}
	res2, err := in.Install(context.Background(), Options{SiteName: "Renamed"})
	if err != nil {
		t.Fatal(err)
	}
	if res2.Pages != 1 || res2.Posts != 0 || res2.Menus != 0 || res2.Forms != 0 {
		// home is a starter page now (flag set), so it is left alone too
		if res2.Total() != 0 {
			t.Fatalf("second run created %+v", res2)
		}
	}
	for k, e := range m.entities {
		if snapshot[k] != string(e.Attributes) {
			t.Errorf("%s was rewritten by the second run", k)
		}
	}
	if len(m.entities) != 5 || len(m.menus) != 2 || len(m.forms) != 1 {
		t.Error("second run duplicated content")
	}
}

func TestInstallNeverTouchesEditedHomeOrExistingPages(t *testing.T) {
	m := newMem()
	e := &entity.Entity{ID: uuid.New(), SchemaID: m.schemas["page"], Slug: "home", Name: "Mine",
		Attributes: json.RawMessage(`{"page_sections":[{"id":"mine","type":"hero","sort_order":1}]}`)}
	m.entities["home"] = e
	m.entities["about"] = &entity.Entity{ID: uuid.New(), Slug: "about", Attributes: json.RawMessage(`{"content":"mine"}`)}
	if _, err := m.installer().Install(context.Background(), Options{SiteName: "X"}); err != nil {
		t.Fatal(err)
	}
	if string(m.entities["home"].Attributes) != `{"page_sections":[{"id":"mine","type":"hero","sort_order":1}]}` || string(m.entities["about"].Attributes) != `{"content":"mine"}` {
		t.Error("existing content was overwritten")
	}
}

func TestPagesUseOnlyKnownSectionTypes(t *testing.T) {
	known := map[string]bool{"hero": true, "features": true, "cta": true, "content": true, "contact_form": true, "contact_info": true}
	for _, p := range pages(Options{SiteName: "S"}) {
		if _, ok := p.attrs["page_sections"]; ok {
			t.Errorf("%s writes page_sections", p.slug)
		}
		doc, ok := p.attrs["page_doc"].(*builder.Doc)
		if !ok {
			continue
		}
		if err := doc.Validate(); err != nil {
			t.Errorf("%s page_doc invalid: %v", p.slug, err)
		}
		for _, c := range builder.Components(doc) {
			if typ, _ := c.P["type"].(string); !known[typ] {
				t.Errorf("%s uses unknown section type %q", p.slug, typ)
			}
		}
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// failOnce makes the n-th call of one operation fail, to cut an install short.
type flaky struct {
	*mem
	failStatus, failItemAt int // 1-based call number to fail (0 = never)
	statusCalls, itemCalls int
}

func (f *flaky) UpdateEntityStatus(ctx context.Context, id uuid.UUID, u *uuid.UUID, s entity.EntityStatus) (*entity.Entity, error) {
	f.statusCalls++
	if f.statusCalls == f.failStatus {
		return nil, context.DeadlineExceeded
	}
	return f.mem.UpdateEntityStatus(ctx, id, u, s)
}

func (f *flaky) CreateMenuItem(ctx context.Context, id uuid.UUID, in *menu.CreateMenuItemInput) (*menu.MenuItem, error) {
	f.itemCalls++
	if f.itemCalls == f.failItemAt {
		return nil, context.DeadlineExceeded
	}
	return f.mem.CreateMenuItem(ctx, id, in)
}

func TestRerunRepairsADraftAndAHalfBuiltMenu(t *testing.T) {
	m := newMem()
	f := &flaky{mem: m, failStatus: 2} // the second page stays a draft
	in := &Installer{Entities: f, Menus: m, Forms: m, Site: m}
	if _, err := in.Install(context.Background(), Options{}); err == nil {
		t.Fatal("the first run should have failed")
	}
	drafts := 0
	for _, e := range m.entities {
		if e.Status == entity.StatusDraft {
			drafts++
		}
	}
	if drafts != 1 {
		t.Fatalf("expected one stranded draft, got %d", drafts)
	}

	// Now a menu with two of its four items.
	m2 := newMem()
	f2 := &flaky{mem: m2, failItemAt: 3}
	in2 := &Installer{Entities: f2, Menus: f2, Forms: m2, Site: m2}
	if _, err := in2.Install(context.Background(), Options{}); err == nil {
		t.Fatal("the first run should have failed")
	}
	hdr := m2.menus[menu.LocationHeader]
	if hdr == nil || len(m2.items[hdr.ID]) != 2 {
		t.Fatalf("setup of the half-built menu failed: %v", m2.items)
	}

	for _, tc := range []struct {
		name string
		in   *Installer
		m    *mem
	}{{"draft", &Installer{Entities: m, Menus: m, Forms: m, Site: m}, m}, {"menu", &Installer{Entities: m2, Menus: m2, Forms: m2, Site: m2}, m2}} {
		if _, err := tc.in.Install(context.Background(), Options{}); err != nil {
			t.Fatalf("%s re-run: %v", tc.name, err)
		}
		for slug, e := range tc.m.entities {
			if e.Status != entity.StatusPublished {
				t.Errorf("%s: %s is still %s after the re-run", tc.name, slug, e.Status)
			}
		}
	}
	if got := m2.items[hdr.ID]; len(got) != 4 {
		t.Errorf("header menu items after repair: %v", got)
	}
	// A third run changes nothing.
	res, err := (&Installer{Entities: m2, Menus: m2, Forms: m2, Site: m2}).Install(context.Background(), Options{})
	if err != nil || res.Total() != 0 {
		t.Errorf("not idempotent: %+v %v", res, err)
	}
}

func TestRerunLeavesAnEditedMenuAlone(t *testing.T) {
	m := newMem()
	if _, err := m.installer().Install(context.Background(), Options{}); err != nil {
		t.Fatal(err)
	}
	hdr := m.menus[menu.LocationHeader]
	m.items[hdr.ID] = []string{"Home", "My custom link"}
	if _, err := m.installer().Install(context.Background(), Options{}); err != nil {
		t.Fatal(err)
	}
	if got := m.items[hdr.ID]; len(got) != 2 {
		t.Errorf("an edited menu was altered: %v", got)
	}
}

func TestIsSeedBothShapes(t *testing.T) {
	doc, _ := json.Marshal(builder.FromSections([]builder.Section{{ID: "welcome", Type: "hero", Content: map[string]any{}}}))
	other, _ := json.Marshal(builder.FromSections([]builder.Section{{ID: "mine", Type: "hero", Content: map[string]any{}}}))
	for attrs, want := range map[string]bool{
		`{"page_sections":[{"id":"welcome"}]}`:            true,
		`{"page_doc":` + string(doc) + `}`:                true,
		`{"page_doc":` + string(other) + `}`:              false,
		`{"starter":true,"page_doc":` + string(doc) + `}`: false,
		`{"content":"x"}`:                                 false,
	} {
		if got := isSeed(&entity.Entity{Attributes: json.RawMessage(attrs)}); got != want {
			t.Errorf("isSeed(%s) = %v, want %v", attrs, got, want)
		}
	}
}

// The starter doc is the lossless conversion of the former page_sections.
func TestStarterDocRoundTripsToSections(t *testing.T) {
	for _, p := range pages(Options{SiteName: "S"}) {
		doc, ok := p.attrs["page_doc"].(*builder.Doc)
		if !ok {
			continue
		}
		secs, ok := builder.LegacySections(doc)
		if !ok || len(secs) == 0 {
			t.Errorf("%s doc is not v1-expressible", p.slug)
		}
	}
}
