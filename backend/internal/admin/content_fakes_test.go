package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/author"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/media"
	"hatamoto.dev/core/internal/person"
	"hatamoto.dev/core/internal/taxonomy"
)

// fid makes a readable, stable id: fid(7) = 00000000-0000-0000-0000-000000000007.
func fid(n int) uuid.UUID { return uuid.MustParse(fmt.Sprintf("00000000-0000-0000-0000-%012d", n)) }

var t0 = time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)

// fakeEntities is entity.Service in memory: enough behaviour for the screens
// (validation by the real validator, versions on attribute change, parents).
type fakeEntities struct {
	mu           sync.Mutex
	schemas      []*entity.EntitySchema
	ents         map[uuid.UUID]*entity.Entity
	versions     map[uuid.UUID][]*entity.EntityVersion
	lastList     *entity.ListEntitiesFilter
	lists        int   // ListEntitiesWithOffset calls
	maxLimit     int   // the largest page limit any list call asked for
	counts       int   // CountEntitiesBySchemaStatus calls
	countErr     error // returned by CountEntitiesBySchemaStatus when set
	parentCounts int   // CountEntitiesByParent calls
	nextID       int
	validator    *entity.Validator
}

func newFakeEntities() *fakeEntities {
	return &fakeEntities{ents: map[uuid.UUID]*entity.Entity{}, versions: map[uuid.UUID][]*entity.EntityVersion{}, nextID: 100, validator: entity.NewValidator()}
}

func (f *fakeEntities) addSchema(n int, name, slug, schema, settings string, builtin bool) *entity.EntitySchema {
	s := &entity.EntitySchema{ID: fid(n), Name: name, Slug: slug, Schema: json.RawMessage(schema), Settings: json.RawMessage(settings), IsBuiltin: builtin, CreatedAt: t0, UpdatedAt: t0}
	f.schemas = append(f.schemas, s)
	return s
}

func (f *fakeEntities) addEntity(n int, schema *entity.EntitySchema, name, slug string, status entity.EntityStatus, attrs string, at time.Time) *entity.Entity {
	e := &entity.Entity{ID: fid(n), SchemaID: schema.ID, Name: name, Slug: slug, Status: status, Attributes: json.RawMessage(attrs), Version: 1, CreatedAt: at, UpdatedAt: at}
	f.ents[e.ID] = e
	return e
}

func (f *fakeEntities) ListSchemas(context.Context) ([]*entity.EntitySchema, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.schemas), nil
}
func (f *fakeEntities) GetSchema(_ context.Context, id uuid.UUID) (*entity.EntitySchema, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.schemas {
		if s.ID == id {
			c := *s
			return &c, nil
		}
	}
	return nil, entity.ErrSchemaNotFound
}
func (f *fakeEntities) CreateSchema(_ context.Context, in *entity.CreateSchemaInput) (*entity.EntitySchema, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.validator.ValidateSchema(in.Schema); err != nil {
		return nil, err
	}
	if err := entity.ValidateSchemaSettingsForWrite(in.Settings); err != nil {
		return nil, err
	}
	slug := in.Slug
	if slug == "" {
		slug = strings.ToLower(strings.ReplaceAll(in.Name, " ", "-"))
	}
	if err := entity.ValidateEditorSetting(slug, in.Schema, in.Settings); err != nil {
		return nil, err
	}
	for _, s := range f.schemas {
		if s.Slug == slug {
			return nil, entity.ErrSlugAlreadyExists
		}
	}
	f.nextID++
	s := &entity.EntitySchema{ID: fid(f.nextID), Name: in.Name, Slug: slug, Description: in.Description, Schema: in.Schema, Settings: in.Settings, CreatedAt: t0, UpdatedAt: t0}
	f.schemas = append(f.schemas, s)
	return s, nil
}
func (f *fakeEntities) UpdateSchema(_ context.Context, id uuid.UUID, in *entity.UpdateSchemaInput) (*entity.EntitySchema, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.schemas {
		if s.ID != id {
			continue
		}
		if s.IsBuiltin {
			return nil, entity.ErrCannotModifyBuiltin
		}
		if in.Schema != nil {
			if err := f.validator.ValidateSchema(*in.Schema); err != nil {
				return nil, err
			}
			s.Schema = *in.Schema
		}
		if in.Settings != nil {
			if err := entity.ValidateSchemaSettingsForWrite(*in.Settings); err != nil {
				return nil, err
			}
			s.Settings = *in.Settings
		}
		if err := entity.ValidateEditorSetting(s.Slug, s.Schema, s.Settings); err != nil {
			return nil, err
		}
		if in.Name != nil {
			s.Name = *in.Name
		}
		if in.Description != nil {
			d := *in.Description
			s.Description = &d
		}
		c := *s
		return &c, nil
	}
	return nil, entity.ErrSchemaNotFound
}
func (f *fakeEntities) DeleteSchema(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, s := range f.schemas {
		if s.ID != id {
			continue
		}
		if s.IsBuiltin {
			return entity.ErrCannotDeleteBuiltin
		}
		for _, e := range f.ents {
			if e.SchemaID == id {
				return entity.ErrSchemaInUse
			}
		}
		f.schemas = slices.Delete(f.schemas, i, i+1)
		return nil
	}
	return entity.ErrSchemaNotFound
}

func (f *fakeEntities) schemaOf(id uuid.UUID) *entity.EntitySchema {
	for _, s := range f.schemas {
		if s.ID == id {
			return s
		}
	}
	return nil
}
func (f *fakeEntities) GetEntity(_ context.Context, id uuid.UUID) (*entity.Entity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.ents[id]
	if !ok {
		return nil, entity.ErrEntityNotFound
	}
	c := *e
	c.Schema = f.schemaOf(e.SchemaID)
	return &c, nil
}
func (f *fakeEntities) CreateEntity(_ context.Context, by *uuid.UUID, in *entity.CreateEntityInput) (*entity.Entity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.schemaOf(in.SchemaID)
	if s == nil {
		return nil, entity.ErrSchemaNotFound
	}
	if err := f.validator.ValidateAttributes(s.Schema, in.Attributes); err != nil {
		return nil, err
	}
	slug := in.Slug
	if slug == "" {
		slug = strings.ToLower(strings.ReplaceAll(in.Name, " ", "-"))
	}
	for _, e := range f.ents {
		if e.Slug == slug {
			return nil, entity.ErrSlugAlreadyExists
		}
	}
	f.nextID++
	e := &entity.Entity{ID: fid(f.nextID), SchemaID: s.ID, Name: in.Name, Slug: slug, Status: entity.StatusDraft, Attributes: in.Attributes, Version: 1,
		ParentID: in.ParentID, AuthorID: in.AuthorID, BylinePersonID: in.BylinePersonID, BylineOrg: in.BylineOrg, CreatedBy: by, CreatedAt: t0, UpdatedAt: t0}
	f.ents[e.ID] = e
	c := *e
	return &c, nil
}
func (f *fakeEntities) UpdateEntity(_ context.Context, id uuid.UUID, _ *uuid.UUID, in *entity.UpdateEntityInput) (*entity.Entity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.ents[id]
	if !ok {
		return nil, entity.ErrEntityNotFound
	}
	if in.Attributes != nil {
		if err := f.validator.ValidateAttributes(f.schemaOf(e.SchemaID).Schema, *in.Attributes); err != nil {
			return nil, err
		}
	}
	if in.Slug != nil && *in.Slug != e.Slug {
		for _, o := range f.ents {
			if o.Slug == *in.Slug {
				return nil, entity.ErrSlugAlreadyExists
			}
		}
		e.Slug = *in.Slug
	}
	if in.Name != nil {
		e.Name = *in.Name
	}
	if in.Locale != nil {
		e.Locale = *in.Locale
	}
	if in.Attributes != nil {
		var a, b any
		_ = json.Unmarshal(e.Attributes, &a)
		_ = json.Unmarshal(*in.Attributes, &b)
		if fmt.Sprint(a) != fmt.Sprint(b) {
			f.versions[id] = append(f.versions[id], &entity.EntityVersion{ID: uuid.New(), EntityID: id, Version: e.Version, Attributes: e.Attributes, ChangeSummary: in.ChangeSummary, CreatedAt: t0.Add(time.Duration(e.Version) * time.Hour)})
			e.Attributes = *in.Attributes
			e.Version++
		}
	}
	if in.ClearParent {
		e.ParentID = nil
	}
	if in.ParentID != nil {
		if *in.ParentID == e.ID {
			return nil, entity.ErrParentCycle
		}
		p := *in.ParentID
		e.ParentID = &p
	}
	if in.ClearAuthor {
		e.AuthorID, e.BylinePersonID, e.BylineOrg = nil, nil, false
	}
	if in.AuthorID != nil {
		a := *in.AuthorID
		e.AuthorID = &a
	}
	if in.BylinePersonID != nil {
		id := *in.BylinePersonID
		e.BylinePersonID, e.BylineOrg = &id, false
	}
	if in.BylineOrg != nil && *in.BylineOrg {
		e.BylinePersonID, e.BylineOrg = nil, true
	}
	e.UpdatedAt = t0.Add(48 * time.Hour)
	c := *e
	return &c, nil
}
func (f *fakeEntities) UpdateEntityStatus(_ context.Context, id uuid.UUID, _ *uuid.UUID, st entity.EntityStatus) (*entity.Entity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.ents[id]
	if !ok {
		return nil, entity.ErrEntityNotFound
	}
	e.Status = st
	if st == entity.StatusPublished && e.PublishedAt == nil {
		e.PublishedAt = ptr(t0)
	}
	c := *e
	return &c, nil
}
func (f *fakeEntities) ScheduleEntity(_ context.Context, id uuid.UUID, _ *uuid.UUID, at time.Time) (*entity.Entity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.ents[id]
	if !ok {
		return nil, entity.ErrEntityNotFound
	}
	if !at.After(time.Now()) {
		return nil, entity.ErrScheduleInPast
	}
	e.Status, e.PublishedAt = entity.StatusScheduled, &at
	c := *e
	return &c, nil
}
func (f *fakeEntities) DeleteEntity(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.ents[id]; !ok {
		return entity.ErrEntityNotFound
	}
	for _, e := range f.ents {
		if e.ParentID != nil && *e.ParentID == id {
			return entity.ErrHasChildren
		}
	}
	delete(f.ents, id)
	return nil
}

// isUnder reports whether e is a descendant of root (callers hold f.mu).
func (f *fakeEntities) isUnder(e *entity.Entity, root uuid.UUID) bool {
	for depth := 0; e != nil && e.ParentID != nil && depth < 32; depth++ {
		if *e.ParentID == root {
			return true
		}
		e = f.ents[*e.ParentID]
	}
	return false
}
func (f *fakeEntities) RefUsers(_ context.Context, blockID uuid.UUID, limit int) ([]entity.RefUser, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []entity.RefUser
	for _, e := range f.ents {
		if e.ID == blockID || !strings.Contains(string(e.Attributes), `"block":"`+blockID.String()+`"`) {
			continue
		}
		u := entity.RefUser{ID: e.ID, Name: e.Name, Slug: e.Slug, Status: e.Status, UpdatedAt: e.UpdatedAt}
		if s := f.schemaOf(e.SchemaID); s != nil {
			u.SchemaSlug = s.Slug
		}
		out = append(out, u)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	if limit > 0 && len(out) > limit {
		return out[:limit], true, nil
	}
	return out, false, nil
}
func (f *fakeEntities) CountEntityChildren(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	want := map[uuid.UUID]bool{}
	for _, id := range ids {
		want[id] = true
	}
	out := map[uuid.UUID]int{}
	for _, e := range f.ents {
		if e.ParentID != nil && want[*e.ParentID] && e.Status != "redirected" {
			out[*e.ParentID]++
		}
	}
	return out, nil
}

func (f *fakeEntities) CountEntitiesByParent(_ context.Context, limit int) ([]entity.ParentCount, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.parentCounts++
	n := map[uuid.UUID]int{}
	for _, e := range f.ents {
		if e.ParentID != nil && e.Status != "redirected" {
			n[*e.ParentID]++
		}
	}
	var out []entity.ParentCount
	for id, c := range n {
		if p := f.ents[id]; p != nil {
			cp := *p
			out = append(out, entity.ParentCount{Parent: &cp, Count: c})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Parent.ID.String() < out[j].Parent.ID.String()
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
func (f *fakeEntities) CountEntitiesBySchemaStatus(_ context.Context) ([]entity.SchemaStatusCount, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.counts++
	if f.countErr != nil {
		return nil, f.countErr
	}
	type key struct {
		s  uuid.UUID
		st entity.EntityStatus
	}
	n := map[key]int{}
	for _, e := range f.ents {
		n[key{e.SchemaID, e.Status}]++
	}
	var out []entity.SchemaStatusCount
	for k, c := range n {
		out = append(out, entity.SchemaStatusCount{SchemaID: k.s, Status: k.st, Count: c})
	}
	return out, nil
}
func (f *fakeEntities) ListEntitiesWithOffset(_ context.Context, fl *entity.ListEntitiesFilter, p *entity.OffsetPage) (*entity.OffsetPagedResult[*entity.Entity], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastList = fl
	f.lists++
	if p != nil && p.Limit > f.maxLimit {
		f.maxLimit = p.Limit
	}
	var items []*entity.Entity
	for _, e := range f.ents {
		if fl.SchemaID != nil && e.SchemaID != *fl.SchemaID {
			continue
		}
		if slices.Contains(fl.ExcludeSchemaIDs, e.SchemaID) {
			continue
		}
		if fl.Status != nil && e.Status != *fl.Status {
			continue
		}
		if fl.Under != nil && !f.isUnder(e, *fl.Under) {
			continue
		}
		if fl.AuthorID != nil && (e.AuthorID == nil || *e.AuthorID != *fl.AuthorID) {
			continue
		}
		if fl.Search != "" && !strings.Contains(strings.ToLower(e.Name+" "+e.Slug), strings.ToLower(fl.Search)) {
			continue
		}
		if fl.UpdatedFrom != nil && e.UpdatedAt.Before(*fl.UpdatedFrom) {
			continue
		}
		if fl.UpdatedBefore != nil && !e.UpdatedAt.Before(*fl.UpdatedBefore) {
			continue
		}
		c := *e
		items = append(items, &c)
	}
	sort.Slice(items, func(i, j int) bool {
		a, b := items[i], items[j]
		less := a.UpdatedAt.After(b.UpdatedAt)
		switch fl.Sort {
		case "name":
			less = strings.ToLower(a.Name) < strings.ToLower(b.Name)
		case "status":
			less = a.Status < b.Status
		}
		if fl.Asc && fl.Sort != "name" && fl.Sort != "status" {
			less = !less
		}
		if fl.Sort == "name" && !fl.Asc {
			less = !less
		}
		return less
	})
	total := len(items)
	limit := max(p.Limit, 1)
	start := min((max(p.Page, 1)-1)*limit, total)
	end := min(start+limit, total)
	return &entity.OffsetPagedResult[*entity.Entity]{Items: items[start:end], Total: total, Page: max(p.Page, 1), Limit: limit, TotalPages: max((total+limit-1)/limit, 1)}, nil
}
func (f *fakeEntities) ListVersions(_ context.Context, id uuid.UUID, limit int) ([]*entity.EntityVersion, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.ents[id]; !ok {
		return nil, entity.ErrEntityNotFound
	}
	vs := slices.Clone(f.versions[id])
	slices.Reverse(vs)
	return vs, nil
}
func (f *fakeEntities) GetVersion(_ context.Context, id uuid.UUID, n int) (*entity.EntityVersion, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, v := range f.versions[id] {
		if v.Version == n {
			return v, nil
		}
	}
	return nil, entity.ErrVersionNotFound
}
func (f *fakeEntities) RestoreVersion(ctx context.Context, id uuid.UUID, u *uuid.UUID, n int) (*entity.Entity, error) {
	v, err := f.GetVersion(ctx, id, n)
	if err != nil {
		return nil, err
	}
	sum := fmt.Sprintf("Restored from version %d", n)
	return f.UpdateEntity(ctx, id, u, &entity.UpdateEntityInput{Attributes: &v.Attributes, ChangeSummary: &sum})
}

// fakeTerms is taxonomy.Service in memory.
type fakeTerms struct {
	mu     sync.Mutex
	terms  []*taxonomy.Term
	attach map[uuid.UUID][]uuid.UUID // entity -> terms
	nextID int
}

func newFakeTerms() *fakeTerms { return &fakeTerms{attach: map[uuid.UUID][]uuid.UUID{}, nextID: 500} }

func (f *fakeTerms) add(n int, kind, name, slug string, parent *uuid.UUID) *taxonomy.Term {
	t := &taxonomy.Term{ID: fid(n), Kind: kind, Name: name, Slug: slug, ParentID: parent, CreatedAt: t0, UpdatedAt: t0}
	f.terms = append(f.terms, t)
	return t
}
func (f *fakeTerms) count(id uuid.UUID) int {
	n := 0
	for _, ts := range f.attach {
		if slices.Contains(ts, id) {
			n++
		}
	}
	return n
}
func (f *fakeTerms) ListTerms(_ context.Context, kind, search string, page, per int) (*taxonomy.TermListResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var items []*taxonomy.TermWithCount
	for _, t := range f.terms {
		if kind != "" && t.Kind != kind {
			continue
		}
		if search != "" && !strings.Contains(strings.ToLower(t.Name), strings.ToLower(search)) {
			continue
		}
		items = append(items, &taxonomy.TermWithCount{Term: *t, EntityCount: f.count(t.ID)})
	}
	return &taxonomy.TermListResult{Items: items, Total: len(items), Page: 1, PerPage: per}, nil
}
func (f *fakeTerms) GetTerm(_ context.Context, id uuid.UUID) (*taxonomy.Term, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, t := range f.terms {
		if t.ID == id {
			c := *t
			return &c, nil
		}
	}
	return nil, taxonomy.ErrTermNotFound
}
func (f *fakeTerms) CreateTerm(_ context.Context, in taxonomy.CreateTermInput) (*taxonomy.Term, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !taxonomy.ValidKind(in.Kind) {
		return nil, taxonomy.ErrInvalidKind
	}
	slug := in.Slug
	if slug == "" {
		slug = taxonomy.Slugify(in.Name)
	}
	for _, t := range f.terms {
		if t.Kind == in.Kind && t.Slug == slug {
			return nil, taxonomy.ErrTermSlugConflict
		}
	}
	f.nextID++
	t := &taxonomy.Term{ID: fid(f.nextID), Kind: in.Kind, Name: in.Name, Slug: slug, Description: in.Description, ParentID: in.ParentID}
	f.terms = append(f.terms, t)
	c := *t
	return &c, nil
}
func (f *fakeTerms) UpdateTerm(_ context.Context, in taxonomy.UpdateTermInput) (*taxonomy.Term, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, t := range f.terms {
		if t.ID != in.ID {
			continue
		}
		for _, o := range f.terms {
			if o.ID != t.ID && o.Kind == t.Kind && o.Slug == in.Slug {
				return nil, taxonomy.ErrTermSlugConflict
			}
		}
		t.Name, t.Slug = in.Name, in.Slug
		if in.Description != nil {
			t.Description = *in.Description
		}
		if in.SEO != nil {
			if c := in.SEO.CanonicalURL; c != "" && !strings.HasPrefix(c, "http") && !strings.HasPrefix(c, "/") {
				return nil, &taxonomy.SEOFieldError{Field: taxonomy.SEOFieldCanonical}
			}
			if utf8.RuneCountInString(in.SEO.MetaTitle) > 255 {
				return nil, &taxonomy.SEOFieldError{Field: taxonomy.SEOFieldTitle}
			}
			s := *in.SEO
			t.SEO = &s
		}
		if in.ClearParent {
			t.ParentID = nil
		}
		if in.ParentID != nil {
			if *in.ParentID == t.ID {
				return nil, taxonomy.ErrTermParentInvalid
			}
			p := *in.ParentID
			t.ParentID = &p
		}
		c := *t
		return &c, nil
	}
	return nil, taxonomy.ErrTermNotFound
}
func (f *fakeTerms) DeleteTerm(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.terms = slices.DeleteFunc(f.terms, func(t *taxonomy.Term) bool { return t.ID == id })
	for _, t := range f.terms {
		if t.ParentID != nil && *t.ParentID == id {
			t.ParentID = nil
		}
	}
	for e, ts := range f.attach {
		f.attach[e] = slices.DeleteFunc(ts, func(x uuid.UUID) bool { return x == id })
	}
	return nil
}
func (f *fakeTerms) MergeTerms(_ context.Context, from, into uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, t := range f.terms {
		if t.ParentID != nil && *t.ParentID == from {
			return taxonomy.ErrMergeHasChilds
		}
	}
	for e, ts := range f.attach {
		if slices.Contains(ts, from) {
			ts = slices.DeleteFunc(ts, func(x uuid.UUID) bool { return x == from })
			if !slices.Contains(ts, into) {
				ts = append(ts, into)
			}
			f.attach[e] = ts
		}
	}
	f.terms = slices.DeleteFunc(f.terms, func(t *taxonomy.Term) bool { return t.ID == from })
	return nil
}
func (f *fakeTerms) ListTermsForEntity(_ context.Context, id uuid.UUID) ([]*taxonomy.Term, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*taxonomy.Term
	for _, t := range f.terms {
		if slices.Contains(f.attach[id], t.ID) {
			c := *t
			out = append(out, &c)
		}
	}
	return out, nil
}
func (f *fakeTerms) ReplaceEntityTerms(_ context.Context, id uuid.UUID, ids []uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.attach[id] = slices.Clone(ids)
	return nil
}

// fakeMedia is media.Service in memory.
type fakeMedia struct {
	mu     sync.Mutex
	files  []*media.Media
	usage  map[uuid.UUID][]media.Usage
	nextID int
}

func newFakeMedia() *fakeMedia { return &fakeMedia{usage: map[uuid.UUID][]media.Usage{}, nextID: 700} }

func (f *fakeMedia) add(n int, original, mime string, alt string) *media.Media {
	u := "/media/x/file-" + fmt.Sprint(n) + ".png"
	m := &media.Media{ID: fid(n), Filename: fmt.Sprintf("file-%d.png", n), OriginalFilename: original, MimeType: mime, FileSize: 2048, PublicURL: &u, Width: ptr(800), Height: ptr(600),
		Variants: []media.ImageVariant{{Size: "thumbnail", URL: "/media/x/file-" + fmt.Sprint(n) + "_thumbnail.png", Width: 150, Height: 150}}, Folder: "/", CreatedAt: t0}
	if alt != "" {
		m.AltText = &alt
	}
	f.files = append(f.files, m)
	return m
}
func (f *fakeMedia) Upload(_ context.Context, _ *uuid.UUID, file io.Reader, name, _ string, size int64) (*media.Media, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	head := make([]byte, 512)
	n, _ := file.Read(head)
	if !media.ExtensionMatchesMIME(fileExt(name), sniff(head[:n])) {
		return nil, media.ErrInvalidMimeType
	}
	if size > 10<<20 {
		return nil, media.ErrFileTooLarge
	}
	f.nextID++
	u := fmt.Sprintf("/media/x/up-%d.png", f.nextID)
	m := &media.Media{ID: fid(f.nextID), Filename: fmt.Sprintf("up-%d.png", f.nextID), OriginalFilename: name, MimeType: "image/png", FileSize: size, PublicURL: &u, Folder: "/", CreatedAt: t0.Add(time.Hour)}
	f.files = append([]*media.Media{m}, f.files...)
	return m, nil
}
func fileExt(n string) string {
	if i := strings.LastIndex(n, "."); i >= 0 {
		return n[i:]
	}
	return ""
}
func sniff(b []byte) string { return httpDetect(b) }
func (f *fakeMedia) Get(_ context.Context, id uuid.UUID) (*media.Media, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, m := range f.files {
		if m.ID == id {
			c := *m
			return &c, nil
		}
	}
	return nil, media.ErrMediaNotFound
}
func (f *fakeMedia) List(_ context.Context, fl *media.ListMediaFilter, limit, offset int) (*media.PagedResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*media.Media
	for _, m := range f.files {
		if fl.MimeType != "" && !strings.HasPrefix(m.MimeType, fl.MimeType) {
			continue
		}
		if fl.Search != "" && !strings.Contains(strings.ToLower(m.OriginalFilename), strings.ToLower(fl.Search)) {
			continue
		}
		c := *m
		out = append(out, &c)
	}
	total := len(out)
	start := min(offset, total)
	return &media.PagedResult{Items: out[start:min(start+limit, total)], Total: total}, nil
}
func (f *fakeMedia) Update(_ context.Context, id uuid.UUID, in *media.UpdateMediaInput) (*media.Media, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, m := range f.files {
		if m.ID == id {
			if in.AltText != nil {
				m.AltText = in.AltText
			}
			if in.Caption != nil {
				m.Caption = in.Caption
			}
			c := *m
			return &c, nil
		}
	}
	return nil, media.ErrMediaNotFound
}
func (f *fakeMedia) Delete(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.files = slices.DeleteFunc(f.files, func(m *media.Media) bool { return m.ID == id })
	return nil
}
func (f *fakeMedia) Usage(_ context.Context, m *media.Media) ([]media.Usage, error) {
	return f.usage[m.ID], nil
}
func (f *fakeMedia) Config() media.Config { return media.Config{MaxSizeMB: 10, BaseURL: "/media"} }

// fakeAuthors is the author picker's store: people, and a site organisation
// name that resolves to the organisation byline.
type fakeAuthors struct {
	list []*person.Person
	org  string
}

func (f *fakeAuthors) List(context.Context) ([]*person.Person, error) { return f.list, nil }
func (f *fakeAuthors) Resolve(_ context.Context, name string) (entity.Byline, error) {
	if f.org != "" && strings.EqualFold(f.org, name) {
		return entity.Byline{Org: true, Name: f.org}, nil
	}
	for _, a := range f.list {
		if strings.EqualFold(a.Name, name) {
			return entity.Byline{PersonID: &a.ID, Name: a.Name}, nil
		}
	}
	a := &person.Person{ID: fid(900 + len(f.list)), Name: name, Slug: author.Slugify(name)}
	f.list = append(f.list, a)
	return entity.Byline{PersonID: &a.ID, Name: name}, nil
}

func httpDetect(b []byte) string { return http.DetectContentType(b) }

// fakeGroups is GroupStore in memory: it returns canned members and
// candidates, records the writes and returns the errors set on it.
type fakeGroups struct {
	mu         sync.Mutex
	members    map[uuid.UUID][]entity.Member // by any member id
	candidates []entity.Candidate
	lastQuery  string
	linked     [][]uuid.UUID
	unlinked   []uuid.UUID
	originals  []uuid.UUID
	err        error // returned by every call when set
}

func newFakeGroups() *fakeGroups { return &fakeGroups{members: map[uuid.UUID][]entity.Member{}} }

// setGroup makes ms one group, reachable by each member's id.
func (f *fakeGroups) setGroup(ms ...entity.Member) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, m := range ms {
		f.members[m.ID] = ms
	}
}

func (f *fakeGroups) Members(_ context.Context, id uuid.UUID) ([]entity.Member, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.members[id]), f.err
}

func (f *fakeGroups) LinkCandidates(_ context.Context, _ uuid.UUID, q string, _ int) ([]entity.Candidate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastQuery = q
	return slices.Clone(f.candidates), f.err
}

func (f *fakeGroups) Link(_ context.Context, ids ...uuid.UUID) (uuid.UUID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return uuid.Nil, f.err
	}
	f.linked = append(f.linked, slices.Clone(ids))
	return uuid.New(), nil
}

func (f *fakeGroups) Unlink(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.unlinked = append(f.unlinked, id)
	return nil
}

func (f *fakeGroups) SetOriginal(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.originals = append(f.originals, id)
	return nil
}

// fakeCopier is EntityCopier in memory: it records the copies asked for and
// answers with a draft in the asked locale, or the error set for the source.
type fakeCopier struct {
	mu      sync.Mutex
	calls   []fakeCopyCall
	errFor  map[uuid.UUID]error // by source id
	missing []entity.MissingParent
	nextID  int
	warn    string // passed to o.Warn on every call when set
}

type fakeCopyCall struct {
	User, Src uuid.UUID
	Opts      entity.CopyOptions
}

func newFakeCopier() *fakeCopier { return &fakeCopier{errFor: map[uuid.UUID]error{}, nextID: 700} }

func (f *fakeCopier) CreateCopy(_ context.Context, userID, srcID uuid.UUID, o entity.CopyOptions) (*entity.Entity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fakeCopyCall{User: userID, Src: srcID, Opts: o})
	if err := f.errFor[srcID]; err != nil {
		return nil, err
	}
	if f.warn != "" && o.Warn != nil {
		o.Warn(f.warn)
	}
	f.nextID++
	status := entity.StatusDraft
	if o.Publish {
		status = entity.StatusPublished
	}
	return &entity.Entity{ID: fid(f.nextID), Name: o.Name, Slug: o.Slug, Locale: o.Locale, Status: status}, nil
}

func (f *fakeCopier) MissingParents(context.Context, uuid.UUID, string) ([]entity.MissingParent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.missing), nil
}

// fakeTermGroups is TermGrouper in memory.
type fakeTermGroups struct {
	mu         sync.Mutex
	locales    map[uuid.UUID]string
	members    map[uuid.UUID][]taxonomy.TermMember // by any member id
	candidates []taxonomy.TermMember
	linked     [][]uuid.UUID
	unlinked   []uuid.UUID
	copies     []fakeTermCopy
	err        error // returned by every call when set
}

type fakeTermCopy struct {
	Src                uuid.UUID
	Locale, Name, Slug string
}

func newFakeTermGroups() *fakeTermGroups {
	return &fakeTermGroups{locales: map[uuid.UUID]string{}, members: map[uuid.UUID][]taxonomy.TermMember{}}
}

func (f *fakeTermGroups) Locale(_ context.Context, id uuid.UUID) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.locales[id], f.err
}

func (f *fakeTermGroups) SetLocale(_ context.Context, id uuid.UUID, locale string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.locales[id] = locale
	return nil
}

func (f *fakeTermGroups) Members(_ context.Context, id uuid.UUID) ([]taxonomy.TermMember, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.members[id]), f.err
}

func (f *fakeTermGroups) Candidates(context.Context, uuid.UUID, string, int) ([]taxonomy.TermMember, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.candidates), f.err
}

func (f *fakeTermGroups) Link(_ context.Context, ids ...uuid.UUID) (uuid.UUID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return uuid.Nil, f.err
	}
	f.linked = append(f.linked, slices.Clone(ids))
	return uuid.New(), nil
}

func (f *fakeTermGroups) Unlink(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.unlinked = append(f.unlinked, id)
	return nil
}

func (f *fakeTermGroups) CreateTermCopy(_ context.Context, srcID uuid.UUID, locale, name, slug string) (*taxonomy.Term, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	f.copies = append(f.copies, fakeTermCopy{srcID, locale, name, slug})
	return &taxonomy.Term{ID: fid(800 + len(f.copies)), Name: name, Slug: slug}, nil
}
