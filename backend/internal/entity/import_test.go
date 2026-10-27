package entity

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/testutil"
)

// importRepo is the in-memory mock plus the ImportStore methods.
type importRepo struct {
	*mockEntityRepository
	byKey map[string]uuid.UUID
}

func newImportRepo() *importRepo {
	return &importRepo{mockEntityRepository: newMockEntityRepository(), byKey: map[string]uuid.UUID{}}
}

func (r *importRepo) GetByImportKey(_ context.Context, key string) (*Entity, error) {
	id, ok := r.byKey[key]
	if !ok {
		return nil, ErrEntityNotFound
	}
	return r.entities[id], nil
}

func (r *importRepo) ImportUpsert(_ context.Context, rec *ImportRecord) (*Entity, bool, error) {
	var meta map[string]any
	_ = json.Unmarshal(rec.Metadata, &meta)
	key, _ := meta["import_key"].(string)
	e := &Entity{
		SchemaID: rec.SchemaID, Name: rec.Name, Slug: rec.Slug, Status: rec.Status, Attributes: rec.Attributes,
		Metadata: rec.Metadata, ParentID: rec.ParentID, AuthorID: rec.AuthorID, BylinePersonID: rec.BylinePersonID, BylineOrg: rec.BylineOrg, Version: 1,
		CreatedAt: rec.CreatedAt, UpdatedAt: rec.UpdatedAt,
	}
	if !rec.PublishedAt.IsZero() {
		p := rec.PublishedAt
		e.PublishedAt = &p
	}
	created := rec.ID == nil
	if created {
		e.ID = uuid.New()
	} else {
		e.ID = *rec.ID
	}
	r.entities[e.ID] = e
	r.byKey[key] = e.ID
	return e, created, nil
}

// fakeBylines is a BylineResolver over in-memory people: one person and one
// mirror author id per name; "Acme" is the site organisation.
type fakeBylines struct {
	people  map[string]uuid.UUID
	mirrors map[uuid.UUID]uuid.UUID
}

func (f *fakeBylines) Resolve(_ context.Context, name string) (Byline, error) {
	if name == "Acme" {
		return Byline{Org: true, Name: name}, nil
	}
	if f.people == nil {
		f.people, f.mirrors = map[string]uuid.UUID{}, map[uuid.UUID]uuid.UUID{}
	}
	id, ok := f.people[name]
	if !ok {
		id = uuid.New()
		f.people[name] = id
	}
	return Byline{PersonID: &id, Name: name}, nil
}

func (f *fakeBylines) Name(_ context.Context, id *uuid.UUID) (string, error) {
	if id == nil {
		return "Acme", nil
	}
	for n, p := range f.people {
		if p == *id {
			return n, nil
		}
	}
	return "", ErrAuthorNotFound
}

func (f *fakeBylines) Check(_ context.Context, id uuid.UUID) error {
	for _, p := range f.people {
		if p == id {
			return nil
		}
	}
	return ErrAuthorNotFound
}

func (f *fakeBylines) Mirror(_ context.Context, id uuid.UUID) (*uuid.UUID, error) {
	m, ok := f.mirrors[id]
	if !ok {
		m = uuid.New()
		f.mirrors[id] = m
	}
	return &m, nil
}

func (f *fakeBylines) PersonChanged(context.Context, uuid.UUID) error  { return nil }
func (f *fakeBylines) PersonDeleting(context.Context, uuid.UUID) error { return nil }

func importFixture(t *testing.T) (*Service, *importRepo, *EntitySchema, *recordingSink) {
	t.Helper()
	sr := newMockSchemaRepository()
	repo := newImportRepo()
	svc := NewService(sr, repo, testLogger())
	sink := &recordingSink{}
	svc.SetEvents(sink)
	schema := seedSchema(t, sr, func(s *EntitySchema) {
		s.Slug = "page"
		s.Schema = json.RawMessage(`{"type":"object","required":["title"],"properties":{"title":{"type":"string"},"content":{"type":"string"}}}`)
	})
	return svc, repo, schema, sink
}

func TestImport_ParentsDatesIdempotencyAndOneEvent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, repo, _, sink := importFixture(t)
	authors := &fakeBylines{}
	svc.SetBylines(authors)

	created := time.Date(2019, 3, 4, 5, 6, 7, 0, time.UTC)
	updated := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	published := time.Date(2019, 3, 5, 0, 0, 0, 0, time.UTC)
	batch := ImportBatch{Source: "test", Items: []ImportItem{
		// The child comes before its parent and the parent before the grandparent.
		{Key: "c", SchemaSlug: "page", Name: "Local", Status: StatusPublished, ParentKey: "b",
			Attributes: json.RawMessage(`{"title":"Local","content":"<p>ok</p><script>alert(1)</script>"}`),
			CreatedAt:  created, UpdatedAt: updated, PublishedAt: published, AuthorName: "Jane Guest"},
		{Key: "b", SchemaSlug: "page", Name: "SEO", ParentKey: "a", Attributes: json.RawMessage(`{"title":"SEO"}`)},
		{Key: "a", SchemaSlug: "page", Name: "Services", Status: StatusPublished, Attributes: json.RawMessage(`{"title":"Services"}`)},
		{Key: "bad", SchemaSlug: "page", Name: "Bad", Attributes: json.RawMessage(`{"nothing":1}`)}, // title missing
	}}

	res, err := svc.Import(ctx, &testUserID, batch)
	testutil.RequireNoError(t, err)
	testutil.Equal(t, 3, res.Created)
	testutil.Equal(t, 0, res.Updated)
	testutil.Equal(t, 1, res.Failed)
	testutil.Error(t, res.Items[3].Err)

	child := repo.entities[res.Items[0].ID]
	mid := repo.entities[res.Items[1].ID]
	root := repo.entities[res.Items[2].ID]
	testutil.RequireNotNil(t, child.ParentID)
	testutil.Equal(t, mid.ID, *child.ParentID)
	testutil.Equal(t, root.ID, *mid.ParentID)
	testutil.Nil(t, root.ParentID)

	// Dates, status and sanitising.
	testutil.True(t, child.CreatedAt.Equal(created), "created_at kept")
	testutil.True(t, child.UpdatedAt.Equal(updated), "updated_at kept")
	testutil.True(t, child.PublishedAt != nil && child.PublishedAt.Equal(published), "published_at kept")
	testutil.Equal(t, StatusPublished, child.Status)
	testutil.Equal(t, StatusDraft, mid.Status)
	testutil.True(t, !strings.Contains(string(child.Attributes), "script"), "script removed: %s", child.Attributes)
	// A published item with no published_at takes its creation date.
	testutil.True(t, root.PublishedAt != nil && root.PublishedAt.Equal(root.CreatedAt))

	// The author was found or created by name and the byline recorded.
	testutil.RequireNotNil(t, child.BylinePersonID)
	testutil.Equal(t, authors.people["Jane Guest"], *child.BylinePersonID)
	testutil.RequireNotNil(t, child.AuthorID)
	testutil.Equal(t, authors.mirrors[*child.BylinePersonID], *child.AuthorID)
	testutil.True(t, strings.Contains(string(child.Attributes), `"author":"Jane Guest"`), "%s", child.Attributes)

	// One event for the batch, none per entity.
	testutil.Equal(t, []EventKind{EventImportCompleted}, sink.kinds())
	sum := sink.events[0].Import
	testutil.RequireNotNil(t, sum)
	testutil.Equal(t, ImportSummary{Source: "test", Created: 3, Failed: 1}, *sum)

	// Re-running the same batch updates in place.
	n := len(repo.entities)
	res2, err := svc.Import(ctx, &testUserID, batch)
	testutil.RequireNoError(t, err)
	testutil.Equal(t, 0, res2.Created)
	testutil.Equal(t, 3, res2.Updated)
	testutil.Equal(t, n, len(repo.entities))
	for i := range 3 {
		testutil.Equal(t, res.Items[i].ID, res2.Items[i].ID)
	}
	testutil.Equal(t, []EventKind{EventImportCompleted, EventImportCompleted}, sink.kinds())
}

func TestImport_ParentErrors(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, repo, _, _ := importFixture(t)
	attrs := json.RawMessage(`{"title":"x"}`)
	res, err := svc.Import(ctx, nil, ImportBatch{Items: []ImportItem{
		{Key: "x", SchemaSlug: "page", Name: "X", ParentKey: "y", Attributes: attrs},
		{Key: "y", SchemaSlug: "page", Name: "Y", ParentKey: "x", Attributes: attrs},
		{Key: "orphan", SchemaSlug: "page", Name: "Orphan", ParentKey: "nope", Attributes: attrs},
		{Key: "dup", SchemaSlug: "page", Name: "Dup", Attributes: attrs},
		{Key: "dup", SchemaSlug: "page", Name: "Dup 2", Attributes: attrs},
		{Key: "", SchemaSlug: "page", Name: "No key", Attributes: attrs},
		{Key: "sched", SchemaSlug: "page", Name: "Sched", Status: StatusScheduled, Attributes: attrs},
		{Key: "weird", SchemaSlug: "page", Name: "Weird", Status: "nonsense", Attributes: attrs},
		{Key: "unknown", SchemaSlug: "nope", Name: "Unknown", Attributes: attrs},
	}})
	testutil.RequireNoError(t, err)
	testutil.Equal(t, 1, res.Created) // the first "dup"
	testutil.Equal(t, 8, res.Failed)
	testutil.Error(t, res.Items[4].Err) // second "dup"
	testutil.Error(t, res.Items[5].Err)
	testutil.Error(t, res.Items[6].Err)
	testutil.ErrorIs(t, res.Items[0].Err, ErrParentCycle)
	testutil.ErrorIs(t, res.Items[1].Err, ErrParentCycle)
	testutil.ErrorIs(t, res.Items[2].Err, ErrParentNotFound)
	testutil.ErrorIs(t, res.Items[7].Err, ErrInvalidStatus)
	testutil.Equal(t, 1, len(repo.entities))
}

func TestImport_BatchLimits(t *testing.T) {
	t.Parallel()
	svc, _, _, sink := importFixture(t)
	_, err := svc.Import(context.Background(), nil, ImportBatch{})
	testutil.ErrorIs(t, err, ErrImportEmpty)
	_, err = svc.Import(context.Background(), nil, ImportBatch{Items: make([]ImportItem, MaxImportBatch+1)})
	testutil.ErrorIs(t, err, ErrImportTooLarge)
	testutil.Equal(t, 0, len(sink.kinds()))

	plain := NewService(newMockSchemaRepository(), newMockEntityRepository(), testLogger())
	_, err = plain.Import(context.Background(), nil, ImportBatch{Items: []ImportItem{{Key: "k"}}})
	testutil.True(t, errors.Is(err, ErrImportUnsupported))
}

func TestImportDates(t *testing.T) {
	t.Parallel()
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	c := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	created, updated, published := importDates(&ImportItem{}, StatusDraft, now)
	testutil.True(t, created.Equal(now) && updated.Equal(now) && published.IsZero())
	created, updated, published = importDates(&ImportItem{CreatedAt: c, UpdatedAt: c.Add(-time.Hour)}, StatusPublished, now)
	testutil.True(t, created.Equal(c) && updated.Equal(c) && published.Equal(c), "updated before created is raised to created")
}
