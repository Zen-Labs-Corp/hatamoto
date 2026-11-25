package kit

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"os"
	"slices"
	"testing"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/comment"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/menu"
	"hatamoto.dev/core/internal/packages"
)

// zipOf packs files into a zip.
func zipOf(t testing.TB, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func pluginZip(t testing.TB, name, version, extra string) []byte {
	perms := `"db","settings","site.scripts"`
	if extra != "" { // an entity_schema needs the matching write permission
		perms += `,"entities.write.own"`
	}
	manifest := fmt.Sprintf(`{"name":%q,"version":%q,"sdk":"^0.27","permissions":[%s]%s}`, name, version, perms, extra)
	return zipOf(t, map[string]string{"plugin.json": manifest, "plugin.wasm": "\x00asm\x01\x00\x00\x00"})
}

func themeZip(t testing.TB, name, version, menus string) []byte {
	manifest := fmt.Sprintf(`{"name":%q,"version":%q%s}`, name, version, menus)
	return zipOf(t, map[string]string{"theme.json": manifest})
}

// kitPackage parses testdata/community/kit.json (after edit, when given) as a
// kit package.
func kitPackage(t testing.TB, edit func(*packages.Kit)) *packages.Package {
	t.Helper()
	b, err := os.ReadFile("testdata/community/kit.json")
	if err != nil {
		t.Fatal(err)
	}
	if edit != nil {
		var k packages.Kit
		if err := jsonx.Unmarshal(b, &k); err != nil {
			t.Fatal(err)
		}
		edit(&k)
		if b, err = jsonx.Marshal(k); err != nil {
			t.Fatal(err)
		}
	}
	p, err := packages.ParseNew(zipOf(t, map[string]string{"kit.json": string(b)}), packages.DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

type schemaRec struct {
	s      *entity.EntitySchema
	origin string
}

// fake implements every Deps service in memory and records each write.
type fake struct {
	t testing.TB

	registry    []packages.Entry
	blobs       map[string][]byte // kind/name
	installed   map[string]string // kind/name -> version
	grants      map[string][]string
	activeTheme string
	themeLocs   []string
	activated   []string
	installErr  error

	schemas map[string]*schemaRec
	pages   map[string]*entity.Entity // schema/slug
	menus   map[string]*menu.Menu
	home    *entity.Entity
	address string
	comOn   bool
	// noSeam leaves Deps.SchemaComments nil; builtin lists slugs the seam refuses.
	noSeam bool
	// failPolicy names a slug whose policy switch-on write fails.
	failPolicy string
	builtin    map[string]bool
	applied    map[string]Applied

	// pluginCreates makes installing a plugin that declares entity_schema
	// create that content type (what its package does at install).
	pluginCreates bool
	// failPage makes CreatePublished of that slug fail once.
	failPage string

	writes []string
}

func newFake(t testing.TB) *fake {
	f := &fake{
		t: t, blobs: map[string][]byte{}, installed: map[string]string{}, grants: map[string][]string{},
		schemas: map[string]*schemaRec{"page": {s: &entity.EntitySchema{ID: uuid.New(), Slug: "page"}}},
		pages:   map[string]*entity.Entity{}, menus: map[string]*menu.Menu{}, applied: map[string]Applied{},
		address: "https://example.com", pluginCreates: true,
	}
	f.home = &entity.Entity{ID: uuid.New(), Slug: "home", Attributes: jsontext.Value(`{"page_sections":[{"id":"welcome"}]}`)}
	return f
}

// withRegistry lists and serves the fixture's plugin and theme.
func (f *fake) withRegistry(t testing.TB) *fake {
	f.publish(packages.KindPlugin, "community-plug", "1.2.0", pluginZip(t, "community-plug", "1.2.0", ""))
	f.publish(packages.KindTheme, "community-look", "1.0.0", themeZip(t, "community-look", "1.0.0", ""))
	return f
}

func (f *fake) publish(kind packages.Kind, name, version string, raw []byte) {
	f.registry = slices.DeleteFunc(f.registry, func(e packages.Entry) bool { return e.Kind == kind && e.Name == name })
	f.registry = append(f.registry, packages.Entry{Kind: kind, Name: name, Version: version})
	f.blobs[string(kind)+"/"+name] = raw
}

func (f *fake) write(format string, a ...any) { f.writes = append(f.writes, fmt.Sprintf(format, a...)) }

func (f *fake) deps() Deps {
	d := Deps{Packages: f, Schemas: f, Pages: f, Menus: f, Site: f, Comments: f, Applied: f}
	if !f.noSeam {
		d.SchemaComments = f
	}
	return d
}

// Packages

func (f *fake) Registry(context.Context) (*packages.Index, error) {
	return &packages.Index{Packages: f.registry}, nil
}
func (f *fake) Fetch(_ context.Context, kind packages.Kind, name string) ([]byte, error) {
	raw, ok := f.blobs[string(kind)+"/"+name]
	if !ok {
		return nil, packages.ErrNotListed
	}
	return raw, nil
}
func (f *fake) Installed(_ context.Context, kind packages.Kind, name string) (string, bool, error) {
	v, ok := f.installed[string(kind)+"/"+name]
	return v, ok, nil
}
func (f *fake) Install(_ context.Context, raw []byte, grant []string) error {
	if f.installErr != nil {
		return f.installErr
	}
	p, err := packages.ParseNew(raw, packages.DefaultLimits)
	if err != nil {
		return err
	}
	f.write("install %s %s grant=%v", p.Kind, p.Name, grant)
	f.installed[string(p.Kind)+"/"+p.Name] = p.Version
	f.grants[p.Name] = grant
	if p.Kind == packages.KindPlugin && f.pluginCreates {
		if man, err := p.PluginManifest(); err == nil && man.EntitySchema != "" {
			f.schemas[man.EntitySchema] = &schemaRec{s: &entity.EntitySchema{ID: uuid.New(), Slug: man.EntitySchema, Settings: jsontext.Value(`{}`)}, origin: p.Name}
		}
	}
	return nil
}
func (f *fake) ActiveTheme(context.Context) (string, error)            { return f.activeTheme, nil }
func (f *fake) ActiveThemeLocations(context.Context) ([]string, error) { return f.themeLocs, nil }
func (f *fake) Activate(_ context.Context, name string) error {
	f.write("activate %s", name)
	f.activeTheme = name
	f.activated = append(f.activated, name)
	return nil
}

// Schemas

func (f *fake) GetSchemaBySlug(_ context.Context, slug string) (*entity.EntitySchema, error) {
	if r, ok := f.schemas[slug]; ok {
		return r.s, nil
	}
	return nil, entity.ErrSchemaNotFound
}
func (f *fake) CreateSchema(_ context.Context, in *entity.CreateSchemaInput) (*entity.EntitySchema, error) {
	f.write("create schema %s", in.Slug)
	s := &entity.EntitySchema{ID: uuid.New(), Name: in.Name, Slug: in.Slug, Schema: in.Schema, Settings: in.Settings}
	f.schemas[in.Slug] = &schemaRec{s: s, origin: in.OriginPlugin}
	return s, nil
}
func (f *fake) UpdateSchema(_ context.Context, id uuid.UUID, in *entity.UpdateSchemaInput) (*entity.EntitySchema, error) {
	for slug, r := range f.schemas {
		if r.s.ID == id {
			f.write("update schema %s", slug)
			if in.Settings != nil {
				r.s.Settings = *in.Settings
			}
			return r.s, nil
		}
	}
	return nil, entity.ErrSchemaNotFound
}
func (f *fake) SchemaOrigin(_ context.Context, slug string) (string, error) {
	if r, ok := f.schemas[slug]; ok {
		return r.origin, nil
	}
	return "", entity.ErrSchemaNotFound
}

// SchemaComments: the real seam validates, merges and writes through the stored settings.

func (f *fake) SchemaComments(_ context.Context, slug string) (SchemaCommentState, error) {
	r, ok := f.schemas[slug]
	if !ok {
		return SchemaCommentState{}, entity.ErrSchemaNotFound
	}
	var st struct {
		Comments struct {
			Order  string         `json:"order"`
			Policy jsontext.Value `json:"policy"`
		} `json:"comments"`
	}
	if len(r.s.Settings) > 0 {
		if err := jsonx.Unmarshal(r.s.Settings, &st, jsonx.StoredLoose); err != nil {
			return SchemaCommentState{}, err
		}
	}
	return SchemaCommentState{
		ID: r.s.ID.String(), Builtin: f.builtin[slug], Order: st.Comments.Order,
		Policy: comment.DecodePolicy(st.Comments.Policy), HasPolicy: len(st.Comments.Policy) > 0,
	}, nil
}

func (f *fake) SetSchemaComments(_ context.Context, slug, order string, p comment.Policy) error {
	r, ok := f.schemas[slug]
	if !ok {
		return entity.ErrSchemaNotFound
	}
	if f.builtin[slug] {
		return errors.New("built-in content type")
	}
	if err := p.Validate(); err != nil {
		return err
	}
	if !comment.ValidOrder(order) {
		return errors.New("bad order")
	}
	if p.Enabled && slug == f.failPolicy {
		return errors.New("policy write failed")
	}
	f.write("comments %s", slug)
	merged, err := comment.MergeSchemaComments(r.s.Settings, &order, &p)
	if err != nil {
		return err
	}
	r.s.Settings = merged
	return nil
}

// Pages

func (f *fake) GetPage(_ context.Context, schema, slug string) (*entity.Entity, error) {
	return f.pages[schema+"/"+slug], nil
}
func (f *fake) CreatePublished(_ context.Context, in *entity.CreateEntityInput, _ *uuid.UUID) (*entity.Entity, error) {
	if in.Slug == f.failPage {
		f.failPage = ""
		return nil, errors.New("page store down")
	}
	f.write("create page %s", in.Slug)
	e := &entity.Entity{ID: uuid.New(), SchemaID: in.SchemaID, Name: in.Name, Slug: in.Slug, Attributes: in.Attributes, Status: entity.StatusPublished}
	for slug, r := range f.schemas {
		if r.s.ID == in.SchemaID {
			f.pages[slug+"/"+in.Slug] = e
		}
	}
	return e, nil
}

// Menus

func (f *fake) GetMenuByLocation(_ context.Context, loc string) (*menu.Menu, error) {
	if m, ok := f.menus[loc]; ok {
		return m, nil
	}
	return nil, menu.ErrMenuNotFound
}
func (f *fake) EnsureMenuAtLocation(_ context.Context, in menu.EnsureInput) (*menu.Menu, bool, error) {
	if m, ok := f.menus[in.Location]; ok {
		return m, false, nil
	}
	f.write("create menu %s", in.Location)
	m := &menu.Menu{ID: uuid.New(), Name: in.Name}
	f.menus[in.Location] = m
	return m, true, nil
}
func (f *fake) GetMenuWithItems(_ context.Context, id uuid.UUID) (*menu.Menu, error) {
	for _, m := range f.menus {
		if m.ID == id {
			return m, nil
		}
	}
	return nil, menu.ErrMenuNotFound
}
func (f *fake) ReplaceMenu(_ context.Context, id uuid.UUID, fields *menu.UpdateMenuInput, items []menu.ReplaceItem) (*menu.Menu, error) {
	for _, m := range f.menus {
		if m.ID == id {
			f.write("replace menu %s", m.Slug)
			if fields != nil && fields.Name != nil {
				m.Name = *fields.Name
			}
			m.Items = nil
			for _, it := range items {
				m.Items = append(m.Items, &menu.MenuItem{Label: it.Label, LinkType: it.LinkType})
			}
			return m, nil
		}
	}
	return nil, menu.ErrMenuNotFound
}

// Site

func (f *fake) Homepage(context.Context) (*entity.Entity, error) { return f.home, nil }
func (f *fake) SetHomepage(_ context.Context, id uuid.UUID) error {
	f.write("set homepage")
	for _, e := range f.pages {
		if e.ID == id {
			f.home = e
		}
	}
	return nil
}
func (f *fake) Address(context.Context) string { return f.address }

// Comments

func (f *fake) Enabled(context.Context) (bool, error) { return f.comOn, nil }

// AppliedLog

func (f *fake) Get(_ context.Context, name string) (*Applied, error) {
	if a, ok := f.applied[name]; ok {
		return &a, nil
	}
	return nil, ErrNotApplied
}
func (f *fake) Record(_ context.Context, a Applied) error {
	f.write("record %s", a.Name)
	f.applied[a.Name] = a
	return nil
}
