package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/sdk"
)

// locSchemas serves a fixed set of schemas by slug.
type locSchemas struct {
	entity.SchemaRepository
	schemas []*entity.EntitySchema
}

func (f *locSchemas) GetBySlug(_ context.Context, slug string) (*entity.EntitySchema, error) {
	for _, s := range f.schemas {
		if s.Slug == slug {
			return s, nil
		}
	}
	return nil, entity.ErrSchemaNotFound
}

func (f *locSchemas) GetByID(_ context.Context, id uuid.UUID) (*entity.EntitySchema, error) {
	for _, s := range f.schemas {
		if s.ID == id {
			return s, nil
		}
	}
	return nil, entity.ErrSchemaNotFound
}

func (f *locSchemas) List(context.Context) ([]*entity.EntitySchema, error) { return f.schemas, nil }

// locEntities keeps entities in memory with their locale.
type locEntities struct {
	entity.EntityRepository
	mu      sync.Mutex
	list    []*entity.Entity
	deleted []uuid.UUID
}

func (f *locEntities) add(e *entity.Entity) *entity.Entity {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.list = append(f.list, e)
	return e
}

func (f *locEntities) Create(_ context.Context, _ *uuid.UUID, in *entity.CreateEntityInput) (*entity.Entity, error) {
	return f.add(&entity.Entity{ID: uuid.New(), SchemaID: in.SchemaID, Name: in.Name, Slug: in.Slug,
		Locale: in.Locale, Status: entity.StatusDraft, Attributes: in.Attributes, Version: 1}), nil
}

func (f *locEntities) GetByID(_ context.Context, id uuid.UUID) (*entity.Entity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, e := range f.list {
		if e.ID == id {
			cp := *e
			return &cp, nil
		}
	}
	return nil, entity.ErrEntityNotFound
}

func (f *locEntities) Update(ctx context.Context, id uuid.UUID, _ *uuid.UUID, in *entity.UpdateEntityInput) (*entity.Entity, error) {
	e, err := f.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if in.Name != nil {
		e.Name = *in.Name
	}
	return e, nil
}

func (f *locEntities) UpdateStatus(ctx context.Context, id uuid.UUID, _ *uuid.UUID, st entity.EntityStatus) (*entity.Entity, error) {
	e, err := f.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	e.Status = st
	return e, nil
}

func (f *locEntities) Delete(ctx context.Context, id uuid.UUID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, id)
	for i, e := range f.list {
		if e.ID == id {
			f.list = append(f.list[:i], f.list[i+1:]...)
			break
		}
	}
	return nil
}

func (f *locEntities) List(context.Context, *entity.ListEntitiesFilter, *entity.KeysetPage) (*entity.PagedResult[*entity.Entity], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return &entity.PagedResult[*entity.Entity]{Items: append([]*entity.Entity(nil), f.list...)}, nil
}

// fakeGroups is an in-memory TranslationGroups that counts its calls.
type fakeGroups struct {
	mu        sync.Mutex
	linkErr   error
	onLink    func() // runs inside Link, before it fails
	linked    [][]uuid.UUID
	groups    map[uuid.UUID]entity.GroupMember
	members   map[uuid.UUID][]entity.Member
	groupOf   int
	groupsFor int
	// groupErr fails GroupOf and GroupsFor.
	groupErr error
}

func (f *fakeGroups) Link(_ context.Context, ids ...uuid.UUID) (uuid.UUID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.onLink != nil {
		f.onLink()
	}
	if f.linkErr != nil {
		return uuid.Nil, f.linkErr
	}
	f.linked = append(f.linked, ids)
	return uuid.New(), nil
}

func (f *fakeGroups) GroupOf(_ context.Context, id uuid.UUID) (*uuid.UUID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.groupOf++
	if f.groupErr != nil {
		return nil, f.groupErr
	}
	if m, ok := f.groups[id]; ok {
		return &m.Group, nil
	}
	return nil, nil
}

func (f *fakeGroups) GroupsFor(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]entity.GroupMember, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.groupsFor++
	if f.groupErr != nil {
		return nil, f.groupErr
	}
	out := map[uuid.UUID]entity.GroupMember{}
	for _, id := range ids {
		if m, ok := f.groups[id]; ok {
			out[id] = m
		}
	}
	return out, nil
}

func (f *fakeGroups) Members(_ context.Context, id uuid.UUID) ([]entity.Member, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.members[id], nil
}

type transRig struct {
	api    *API
	ents   *locEntities
	groups *fakeGroups
	page   *entity.EntitySchema
	person *entity.EntitySchema
	// sdk is the calling plugin's manifest SDK range.
	sdk string
	// ctx is the call context; nil means context.Background().
	ctx context.Context
}

// newTransRig builds an API over in-memory services. multi gives the site two
// locales (en-XX default, fr-XX under /xx); withGroups installs the fake store.
func newTransRig(t *testing.T, multi, withGroups bool) *transRig {
	t.Helper()
	free := json.RawMessage(`{"type":"object"}`)
	page := &entity.EntitySchema{ID: uuid.New(), Slug: "page", Name: "Page", Schema: free}
	person := &entity.EntitySchema{ID: uuid.New(), Slug: "person", Name: "Person", Schema: free}
	ents := &locEntities{}
	svc := entity.NewService(&locSchemas{schemas: []*entity.EntitySchema{page, person}}, ents, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if multi {
		svc.URLRegistry().SetLocales(func(context.Context) (*entity.LocaleSet, error) {
			return entity.NewLocaleSet("en-XX", []entity.LocaleEntry{{Tag: "en-XX"}, {Tag: "fr-XX", Segment: "xx"}}, false, "")
		})
	}
	r := &transRig{sdk: "^0.15", ents: ents, page: page, person: person, groups: &fakeGroups{
		groups: map[uuid.UUID]entity.GroupMember{}, members: map[uuid.UUID][]entity.Member{},
	}}
	s := Services{Entities: svc}
	if withGroups {
		s.Groups = r.groups
	}
	r.api = NewAPI(s, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return r
}

func (r *transRig) call(t *testing.T, fn func(context.Context, *API, *callEnv, json.RawMessage) (any, error), args any) (map[string]any, error) {
	t.Helper()
	raw, _ := json.Marshal(args)
	ctx := r.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	res, err := fn(ctx, r.api, &callEnv{manifest: sdk.Manifest{SDK: r.sdk}}, raw)
	if err != nil {
		return nil, err
	}
	b, _ := json.Marshal(res)
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out, nil
}

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	var he *sdk.HostError
	if err == nil {
		t.Fatalf("no error, want %s", code)
	}
	if !errors.As(err, &he) || he.Code != code {
		t.Fatalf("error %v, want code %s", err, code)
	}
}

func (r *transRig) seed(schema *entity.EntitySchema, locale string) *entity.Entity {
	return r.ents.add(&entity.Entity{ID: uuid.New(), SchemaID: schema.ID, Name: "n-" + locale, Slug: "s-" + locale,
		Locale: locale, Status: entity.StatusDraft, Attributes: json.RawMessage(`{}`), Version: 1})
}

func TestEntitiesCreateTranslationOf(t *testing.T) {
	r := newTransRig(t, true, true)
	target := r.seed(r.page, "en-XX")
	out, err := r.call(t, fnEntitiesCreate, map[string]any{
		"schema_slug": "page", "name": "Copy", "slug": "copy", "locale": "fr-XX", "translation_of": target.ID.String(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.groups.linked) != 1 || r.groups.linked[0][0] != target.ID || r.groups.linked[0][1].String() != out["id"] {
		t.Fatalf("linked = %v, want [target, created]", r.groups.linked)
	}
	if out["locale"] != "fr-XX" {
		t.Fatalf("locale = %v", out["locale"])
	}
}

func TestEntitiesCreateTranslationOfRejects(t *testing.T) {
	r := newTransRig(t, true, true)
	target := r.seed(r.page, "en-XX")
	other := r.seed(r.person, "en-XX")
	before := len(r.ents.list)
	for name, c := range map[string]struct {
		args map[string]any
		code string
	}{
		"same locale":    {map[string]any{"schema_slug": "page", "name": "x", "locale": "en-XX", "translation_of": target.ID.String()}, sdk.ErrInvalidArgument},
		"other schema":   {map[string]any{"schema_slug": "page", "name": "x", "locale": "fr-XX", "translation_of": other.ID.String()}, sdk.ErrInvalidArgument},
		"unknown target": {map[string]any{"schema_slug": "page", "name": "x", "locale": "fr-XX", "translation_of": uuid.NewString()}, sdk.ErrNotFound},
		"bad target id":  {map[string]any{"schema_slug": "page", "name": "x", "locale": "fr-XX", "translation_of": "nope"}, sdk.ErrInvalidArgument},
		"no locale":      {map[string]any{"schema_slug": "page", "name": "x", "translation_of": target.ID.String()}, sdk.ErrInvalidArgument},
		"person target":  {map[string]any{"schema_slug": "person", "name": "x", "locale": "fr-XX", "translation_of": other.ID.String()}, sdk.ErrInvalidArgument},
	} {
		_, err := r.call(t, fnEntitiesCreate, c.args)
		if err == nil {
			t.Errorf("%s: no error", name)
			continue
		}
		if he := r.api.toHostError("p", err); he.Code != c.code {
			t.Errorf("%s: code %s, want %s", name, he.Code, c.code)
		}
	}
	if len(r.ents.list) != before || len(r.groups.linked) != 0 {
		t.Fatalf("a rejected create left rows or links: %d rows, %v", len(r.ents.list), r.groups.linked)
	}
}

func TestEntitiesCreateTranslationOfLinkFailureDeletes(t *testing.T) {
	r := newTransRig(t, true, true)
	target := r.seed(r.page, "en-XX")
	r.groups.linkErr = entity.ErrLocaleTaken
	_, err := r.call(t, fnEntitiesCreate, map[string]any{
		"schema_slug": "page", "name": "Copy", "locale": "fr-XX", "translation_of": target.ID.String(),
	})
	wantCode(t, err, sdk.ErrInvalidArgument)
	if len(r.ents.deleted) != 1 || len(r.ents.list) != 1 {
		t.Fatalf("deleted = %v, rows = %d: the created entity must be removed", r.ents.deleted, len(r.ents.list))
	}
}

// A locale the target's group already has is refused before anything is
// created, so no entity (and no created/deleted event pair) comes of it.
func TestEntitiesCreateTranslationOfLocaleTakenRefusedEarly(t *testing.T) {
	r := newTransRig(t, true, true)
	target := r.seed(r.page, "en-XX")
	taken := r.seed(r.page, "fr-XX")
	r.groups.members[target.ID] = []entity.Member{{ID: target.ID, Locale: "en-XX"}, {ID: taken.ID, Locale: "fr-XX"}}
	before := len(r.ents.list)
	_, err := r.call(t, fnEntitiesCreate, map[string]any{
		"schema_slug": "page", "name": "Copy", "locale": "fr-XX", "translation_of": target.ID.String(),
	})
	wantCode(t, err, sdk.ErrInvalidArgument)
	if len(r.ents.list) != before || len(r.ents.deleted) != 0 || len(r.groups.linked) != 0 {
		t.Fatalf("rows %d (was %d), deleted %v, linked %v: nothing may be created or deleted", len(r.ents.list), before, r.ents.deleted, r.groups.linked)
	}
}

// A link that fails because the call's context ended must still delete the new
// entity: the cleanup does not run on the dead context.
func TestEntitiesCreateTranslationOfLinkTimeoutDeletes(t *testing.T) {
	r := newTransRig(t, true, true)
	target := r.seed(r.page, "en-XX")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.ctx = ctx
	r.groups.onLink = cancel
	r.groups.linkErr = context.Canceled
	_, err := r.call(t, fnEntitiesCreate, map[string]any{
		"schema_slug": "page", "name": "Copy", "locale": "fr-XX", "translation_of": target.ID.String(),
	})
	if err == nil {
		t.Fatal("no error")
	}
	if len(r.ents.deleted) != 1 || len(r.ents.list) != 1 {
		t.Fatalf("deleted = %v, rows = %d: the created entity must be removed", r.ents.deleted, len(r.ents.list))
	}
}

func TestEntitiesCreateTranslationOfNeedsGroups(t *testing.T) {
	r := newTransRig(t, false, false)
	target := r.seed(r.page, "")
	_, err := r.call(t, fnEntitiesCreate, map[string]any{
		"schema_slug": "page", "name": "Copy", "locale": "fr-XX", "translation_of": target.ID.String(),
	})
	wantCode(t, err, sdk.ErrUnavailable)
}

func TestEntitiesCreateLocaleRequiredOnMulti(t *testing.T) {
	r := newTransRig(t, true, true)
	_, err := r.call(t, fnEntitiesCreate, map[string]any{"schema_slug": "page", "name": "x"})
	wantCode(t, err, sdk.ErrInvalidArgument)
	if _, err := r.call(t, fnEntitiesCreate, map[string]any{"schema_slug": "person", "name": "x"}); err != nil {
		t.Fatalf("a schema without translations needs no locale: %v", err)
	}
	if _, err := r.call(t, fnEntitiesCreate, map[string]any{"schema_slug": "page", "name": "x", "locale": "fr-XX"}); err != nil {
		t.Fatalf("create with locale: %v", err)
	}
}

func TestEntitiesCreateLocaleOptionalForOlderPlugins(t *testing.T) {
	for _, rng := range []string{"^0.14", "^0.13", "^0.5", ""} {
		r := newTransRig(t, true, true)
		r.sdk = rng
		if _, err := r.call(t, fnEntitiesCreate, map[string]any{"schema_slug": "page", "name": "x"}); err != nil {
			t.Fatalf("range %q: create without locale: %v", rng, err)
		}
	}
	r := newTransRig(t, true, true)
	r.sdk = ">=0.15.0 <1.0.0"
	_, err := r.call(t, fnEntitiesCreate, map[string]any{"schema_slug": "page", "name": "x"})
	wantCode(t, err, sdk.ErrInvalidArgument)
}

func TestEntitiesCreateSingleLocaleUnchanged(t *testing.T) {
	r := newTransRig(t, false, false)
	out, err := r.call(t, fnEntitiesCreate, map[string]any{"schema_slug": "page", "name": "x"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := out["translation_group"]; ok {
		t.Fatalf("translation_group present without groups: %v", out)
	}
}

func TestEntitiesGroupInDTO(t *testing.T) {
	r := newTransRig(t, true, true)
	a, b, c := r.seed(r.page, "en-XX"), r.seed(r.page, "fr-XX"), r.seed(r.page, "en-XX")
	g := uuid.New()
	r.groups.groups[a.ID] = entity.GroupMember{ID: a.ID, Group: g, Locale: "en-XX"}
	r.groups.groups[b.ID] = entity.GroupMember{ID: b.ID, Group: g, Locale: "fr-XX"}

	get, err := r.call(t, fnEntitiesGet, map[string]any{"id": a.ID.String()})
	if err != nil || get["translation_group"] != g.String() {
		t.Fatalf("get = %v, %v", get, err)
	}
	if get, _ = r.call(t, fnEntitiesGet, map[string]any{"id": c.ID.String()}); get["translation_group"] != nil {
		t.Fatalf("ungrouped entity carries a group: %v", get)
	}

	r.groups.groupsFor = 0
	list, err := r.call(t, fnEntitiesList, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if r.groups.groupsFor != 1 {
		t.Fatalf("GroupsFor called %d times for a list of 3, want 1", r.groups.groupsFor)
	}
	grouped := 0
	for _, it := range list["items"].([]any) {
		if it.(map[string]any)["translation_group"] == g.String() {
			grouped++
		}
	}
	if grouped != 2 {
		t.Fatalf("%d grouped items, want 2", grouped)
	}
}

type fakePaths struct {
	// gotPrefix is the localePrefix argument of the last Resolve call.
	gotPrefix bool
}

func (*fakePaths) Path(_ context.Context, e *entity.Entity) string {
	return "/" + e.Locale + "/" + e.Slug
}
func (f *fakePaths) Resolve(_ context.Context, _ string, localePrefix bool) (*entity.Entity, error) {
	f.gotPrefix = localePrefix
	return nil, entity.ErrEntityNotFound
}
func (*fakePaths) BaseURL(context.Context) string { return "" }

func (r *transRig) translations(t *testing.T, id uuid.UUID) ([]map[string]any, error) {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"id": id.String()})
	res, err := fnEntitiesTranslations(context.Background(), r.api, nil, raw)
	if err != nil {
		return nil, err
	}
	b, _ := json.Marshal(res)
	var out []map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out, nil
}

func TestEntitiesTranslations(t *testing.T) {
	r := newTransRig(t, true, true)
	en, fr := r.seed(r.page, "en-XX"), r.seed(r.page, "fr-XX")
	lone := r.seed(r.page, "en-XX")
	g := uuid.New()
	// Members arrive unordered; the result is ordered by locale.
	members := []entity.Member{
		{ID: fr.ID, Group: g, Locale: "fr-XX", Status: "published"},
		{ID: en.ID, Group: g, Locale: "en-XX", Status: "draft"},
	}
	r.groups.members[en.ID], r.groups.members[fr.ID] = members, members

	if got, err := r.translations(t, en.ID); err != nil || len(got) != 2 || got[0]["locale"] != "en-XX" || got[1]["locale"] != "fr-XX" {
		t.Fatalf("translations = %v, %v", got, err)
	} else if _, has := got[0]["path"]; has {
		t.Fatalf("path present without Paths: %v", got[0])
	}

	r.api.svc.Paths = &fakePaths{}
	got, err := r.translations(t, fr.ID)
	if err != nil || got[1]["path"] != "/fr-XX/"+fr.Slug || got[0]["status"] != "draft" {
		t.Fatalf("translations with paths = %v, %v", got, err)
	}

	for name, m := range map[string][]entity.Member{"no group": nil, "one member": {{ID: lone.ID, Group: g}}} {
		r.groups.members[lone.ID] = m
		if got, err := r.translations(t, lone.ID); err != nil || got == nil || len(got) != 0 {
			t.Fatalf("%s: translations = %#v, %v, want []", name, got, err)
		}
	}
}

func TestEntitiesTranslationsGuards(t *testing.T) {
	if h := hostFns[sdk.FnEntitiesTranslations]; h.perm != sdk.PermEntitiesRead {
		t.Fatalf("permission = %q, want %s", h.perm, sdk.PermEntitiesRead)
	}
	r := newTransRig(t, false, false)
	_, err := r.translations(t, uuid.New())
	wantCode(t, err, sdk.ErrUnavailable)
	r = newTransRig(t, false, true)
	raw, _ := json.Marshal(map[string]any{"id": "nope"})
	_, err = fnEntitiesTranslations(context.Background(), r.api, nil, raw)
	wantCode(t, err, sdk.ErrInvalidArgument)
}

func TestEntitiesGetPathPrefixFollowsSDKRange(t *testing.T) {
	for rng, want := range map[string]bool{"^0.14": false, "^0.15": true, ">=0.15.0 <1.0.0": true} {
		r := newTransRig(t, true, true)
		paths := &fakePaths{gotPrefix: !want}
		r.api.svc.Paths = paths
		r.sdk = rng
		_, err := r.call(t, fnEntitiesGet, map[string]any{"path": "/xx/about"})
		if !errors.Is(err, entity.ErrEntityNotFound) {
			t.Fatalf("range %q: err = %v, want not found", rng, err)
		}
		if paths.gotPrefix != want {
			t.Errorf("range %q: locale prefix = %v, want %v", rng, paths.gotPrefix, want)
		}
	}
}

func TestEntitiesGroupLookupFailureIsInternal(t *testing.T) {
	calls := map[string]func(r *transRig, id uuid.UUID) error{
		"get": func(r *transRig, id uuid.UUID) error {
			_, err := r.call(t, fnEntitiesGet, map[string]any{"id": id.String()})
			return err
		},
		"list": func(r *transRig, _ uuid.UUID) error {
			_, err := r.call(t, fnEntitiesList, map[string]any{"schema_slug": "page"})
			return err
		},
		"create": func(r *transRig, _ uuid.UUID) error {
			_, err := r.call(t, fnEntitiesCreate, map[string]any{"schema_slug": "page", "name": "x", "locale": "fr-XX"})
			return err
		},
		"update": func(r *transRig, id uuid.UUID) error {
			_, err := r.call(t, fnEntitiesUpdate, map[string]any{"id": id.String(), "name": "y"})
			return err
		},
		"status": func(r *transRig, id uuid.UUID) error {
			_, err := r.call(t, fnEntitiesStatus, map[string]any{"id": id.String(), "status": "published"})
			return err
		},
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			r := newTransRig(t, true, true)
			ent := r.seed(r.page, "en-XX")
			ent.Schema = r.page // the service reads it on update and status
			r.groups.groupErr = fmt.Errorf("group of entity: %w", &pgconn.PgError{Message: "boom"})
			err := call(r, ent.ID)
			if err == nil {
				t.Fatal("no error")
			}
			he := r.api.toHostError("p", err)
			if he.Code != sdk.ErrInternal || strings.Contains(he.Message, "boom") {
				t.Fatalf("error = %s %q, want internal without the cause", he.Code, he.Message)
			}
		})
	}
}
