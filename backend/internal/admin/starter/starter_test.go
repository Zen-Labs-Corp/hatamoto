package starter

import (
	"context"
	"encoding/json/jsontext"
	"strings"
	"testing"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/builder"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/form"
	"hatamoto.dev/core/internal/jsonx"
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
	// entityUpdates counts UpdateEntity calls.
	entityUpdates int
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
			if in.Name != nil {
				e.Name = *in.Name
			}
			if in.Attributes != nil {
				e.Attributes = *in.Attributes
			}
			m.entityUpdates++
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
		Attributes: jsontext.Value(`{"page_sections":[{"id":"welcome","type":"hero","sort_order":1,"content":{"headline":"My site"}}],"content":"<p>x</p>"}`)}
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
	if got := m.entities["home"].Name; got != "Acme <Co>" {
		t.Errorf("home page name = %q, want the site name", got)
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
	_ = jsonx.Unmarshal(m.entities["hello-world"].Attributes, &post)
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
		Attributes: jsontext.Value(`{"page_sections":[{"id":"mine","type":"hero","sort_order":1}]}`)}
	m.entities["home"] = e
	m.entities["about"] = &entity.Entity{ID: uuid.New(), Slug: "about", Attributes: jsontext.Value(`{"content":"mine"}`)}
	if _, err := m.installer().Install(context.Background(), Options{SiteName: "X"}); err != nil {
		t.Fatal(err)
	}
	if string(m.entities["home"].Attributes) != `{"page_sections":[{"id":"mine","type":"hero","sort_order":1}]}` || string(m.entities["about"].Attributes) != `{"content":"mine"}` {
		t.Error("existing content was overwritten")
	}
	if m.entities["home"].Name != "Mine" {
		t.Errorf("edited home page renamed to %q", m.entities["home"].Name)
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
	doc, _ := jsonx.Marshal(builder.FromSections([]builder.Section{{ID: "welcome", Type: "hero", Content: map[string]any{}}}))
	other, _ := jsonx.Marshal(builder.FromSections([]builder.Section{{ID: "mine", Type: "hero", Content: map[string]any{}}}))
	for attrs, want := range map[string]bool{
		`{"page_sections":[{"id":"welcome"}]}`:            true,
		`{"page_doc":` + string(doc) + `}`:                true,
		`{"page_doc":` + string(other) + `}`:              false,
		`{"starter":true,"page_doc":` + string(doc) + `}`: false,
		`{"content":"x"}`:                                 false,
	} {
		if got := IsSeed(&entity.Entity{Attributes: jsontext.Value(attrs)}); got != want {
			t.Errorf("IsSeed(%s) = %v, want %v", attrs, got, want)
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

// docSeedHome is the home page as the server's first boot makes it
// (app/firstboot.go): one "welcome" hero in a page_doc, named after the site
// name the server started with.
func docSeedHome(t *testing.T, m *mem, name string) *entity.Entity {
	t.Helper()
	doc, err := jsonx.Marshal(builder.FromSections([]builder.Section{{
		ID: "welcome", Type: "hero", SortOrder: 1,
		Content: map[string]any{"headline": name, "subheadline": "Your site is running. Sign in to the admin to edit this page."},
	}}))
	if err != nil {
		t.Fatal(err)
	}
	attrs, err := jsonx.Marshal(map[string]any{"page_doc": jsontext.Value(doc), "content": "<p>Your site is running.</p>"})
	if err != nil {
		t.Fatal(err)
	}
	e := &entity.Entity{ID: uuid.New(), SchemaID: m.schemas["page"], Name: name, Slug: "home", Status: entity.StatusPublished, Attributes: jsontext.Value(attrs)}
	m.entities["home"] = e
	return e
}

func welcome(t *testing.T, e *entity.Entity) map[string]any {
	t.Helper()
	var a struct {
		Doc jsontext.Value `json:"page_doc"`
	}
	if err := jsonx.Unmarshal(e.Attributes, &a); err != nil {
		t.Fatal(err)
	}
	d, err := builder.Parse(a.Doc)
	if err != nil {
		t.Fatal(err)
	}
	cs := builder.Components(d)
	if len(cs) != 1 {
		t.Fatalf("%d components", len(cs))
	}
	content, _ := cs[0].P["content"].(map[string]any)
	return content
}

func TestNameSeedHomeRenamesTheFirstBootPage(t *testing.T) {
	m := newMem()
	home := docSeedHome(t, m, "My Hatamoto site")
	in := m.installer()
	uid := uuid.New()
	changed, err := in.NameSeedHome(context.Background(), Options{SiteName: "Acme Plumbing", UserID: &uid})
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	if home.Name != "Acme Plumbing" {
		t.Errorf("name = %q", home.Name)
	}
	c := welcome(t, home)
	if c["headline"] != "Acme Plumbing" || c["subheadline"] != "Your site is running. Sign in to the admin to edit this page." {
		t.Errorf("welcome content = %v", c)
	}
	if !contains(string(home.Attributes), `"content":"<p>Your site is running.</p>"`) {
		t.Errorf("other attributes lost: %s", home.Attributes)
	}
	if !IsSeed(home) {
		t.Error("the renamed page must still count as the seed, so the starter content can replace it later")
	}
	if m.flushed != 1 {
		t.Errorf("page cache flushed %d times, want 1", m.flushed)
	}

	// Again with the same name: nothing to do.
	if changed, err := in.NameSeedHome(context.Background(), Options{SiteName: "Acme Plumbing"}); err != nil || changed {
		t.Errorf("second run: changed=%v err=%v", changed, err)
	}
	if m.entityUpdates != 1 {
		t.Errorf("%d entity updates, want 1", m.entityUpdates)
	}
}

func TestNameSeedHomeLeavesOtherPagesAlone(t *testing.T) {
	ctx := context.Background()

	// No home page at all.
	m := newMem()
	if changed, err := m.installer().NameSeedHome(ctx, Options{SiteName: "Acme"}); err != nil || changed {
		t.Errorf("no home: changed=%v err=%v", changed, err)
	}

	// A home page someone built.
	m = newMem()
	m.entities["home"] = &entity.Entity{ID: uuid.New(), Slug: "home", Name: "Mine",
		Attributes: jsontext.Value(`{"page_sections":[{"id":"mine","type":"hero","sort_order":1}]}`)}
	if changed, err := m.installer().NameSeedHome(ctx, Options{SiteName: "Acme"}); err != nil || changed || m.entities["home"].Name != "Mine" {
		t.Errorf("edited home: changed=%v err=%v name=%q", changed, err, m.entities["home"].Name)
	}

	// The starter's home page.
	m = newMem()
	if _, err := m.installer().Install(ctx, Options{SiteName: "Acme"}); err != nil {
		t.Fatal(err)
	}
	before := string(m.entities["home"].Attributes)
	if changed, err := m.installer().NameSeedHome(ctx, Options{SiteName: "Other"}); err != nil || changed || m.entities["home"].Name != "Acme" || string(m.entities["home"].Attributes) != before {
		t.Errorf("starter home: changed=%v err=%v name=%q", changed, err, m.entities["home"].Name)
	}

	// No site name.
	m = newMem()
	docSeedHome(t, m, "My Hatamoto site")
	if changed, err := m.installer().NameSeedHome(ctx, Options{}); err != nil || changed || m.entities["home"].Name != "My Hatamoto site" {
		t.Errorf("empty name: changed=%v err=%v", changed, err)
	}
}

// A seed whose headline was changed keeps it; only the page name follows.
func TestNameSeedHomeKeepsAChangedHeadline(t *testing.T) {
	m := newMem()
	home := docSeedHome(t, m, "My Hatamoto site")
	attrs := strings.Replace(string(home.Attributes), `"headline":"My Hatamoto site"`, `"headline":"Hello"`, 1)
	if attrs == string(home.Attributes) {
		t.Fatalf("test setup: headline not found in %s", home.Attributes)
	}
	home.Attributes = jsontext.Value(attrs)
	if changed, err := m.installer().NameSeedHome(context.Background(), Options{SiteName: "Acme"}); err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	if home.Name != "Acme" || string(home.Attributes) != attrs {
		t.Errorf("name=%q attributes=%s", home.Name, home.Attributes)
	}
}
