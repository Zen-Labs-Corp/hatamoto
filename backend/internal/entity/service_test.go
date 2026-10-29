package entity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"hatamoto.dev/core/internal/testutil"
)

// ---------------------------------------------------------------------------
// Mock Repositories
// ---------------------------------------------------------------------------

// mockSchemaRepository is an in-memory mock of SchemaRepository.
type mockSchemaRepository struct {
	schemas      map[uuid.UUID]*EntitySchema
	slugIndex    map[string]uuid.UUID
	entityCounts map[uuid.UUID]int
	createErr    error
	getByIDErr   error
	getBySlugErr error
	updateErr    error
	deleteErr    error
	listErr      error
	countErr     error
}

func newMockSchemaRepository() *mockSchemaRepository {
	return &mockSchemaRepository{
		schemas:      make(map[uuid.UUID]*EntitySchema),
		slugIndex:    make(map[string]uuid.UUID),
		entityCounts: make(map[uuid.UUID]int),
	}
}

func (m *mockSchemaRepository) Create(_ context.Context, input *CreateSchemaInput) (*EntitySchema, error) {
	if m.createErr != nil {
		return nil, m.createErr
	}
	if _, exists := m.slugIndex[input.Slug]; exists {
		return nil, ErrSlugAlreadyExists
	}
	id := uuid.New()
	now := time.Now()
	s := &EntitySchema{
		ID:          id,
		Name:        input.Name,
		Slug:        input.Slug,
		Description: input.Description,
		Schema:      input.Schema,
		Settings:    input.Settings,
		IsBuiltin:   false,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	m.schemas[id] = s
	m.slugIndex[input.Slug] = id
	return s, nil
}

func (m *mockSchemaRepository) GetByID(_ context.Context, id uuid.UUID) (*EntitySchema, error) {
	if m.getByIDErr != nil {
		return nil, m.getByIDErr
	}
	s, ok := m.schemas[id]
	if !ok {
		return nil, ErrSchemaNotFound
	}
	return s, nil
}

// InTx has no real transaction to open: fn runs with a nil tx, which the
// mock's *Tx methods ignore.
func (m *mockSchemaRepository) InTx(_ context.Context, fn func(tx pgx.Tx) error) error {
	return fn(nil)
}

func (m *mockSchemaRepository) GetByIDForUpdate(ctx context.Context, _ pgx.Tx, id uuid.UUID) (*EntitySchema, error) {
	return m.GetByID(ctx, id)
}

func (m *mockSchemaRepository) UpdateTx(ctx context.Context, _ pgx.Tx, id uuid.UUID, input *UpdateSchemaInput) (*EntitySchema, error) {
	return m.Update(ctx, id, input)
}

func (m *mockSchemaRepository) GetBySlug(_ context.Context, slug string) (*EntitySchema, error) {
	if m.getBySlugErr != nil {
		return nil, m.getBySlugErr
	}
	id, ok := m.slugIndex[slug]
	if !ok {
		return nil, ErrSchemaNotFound
	}
	return m.schemas[id], nil
}

func (m *mockSchemaRepository) Update(_ context.Context, id uuid.UUID, input *UpdateSchemaInput) (*EntitySchema, error) {
	if m.updateErr != nil {
		return nil, m.updateErr
	}
	s, ok := m.schemas[id]
	if !ok {
		return nil, ErrSchemaNotFound
	}
	if input.Name != nil {
		s.Name = *input.Name
	}
	if input.Description != nil {
		s.Description = input.Description
	}
	if input.Schema != nil {
		s.Schema = *input.Schema
	}
	if input.Settings != nil {
		s.Settings = *input.Settings
	}
	s.UpdatedAt = time.Now()
	return s, nil
}

func (m *mockSchemaRepository) Delete(_ context.Context, id uuid.UUID) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}
	s, ok := m.schemas[id]
	if !ok {
		return ErrSchemaNotFound
	}
	delete(m.slugIndex, s.Slug)
	delete(m.schemas, id)
	return nil
}

func (m *mockSchemaRepository) List(_ context.Context) ([]*EntitySchema, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	result := make([]*EntitySchema, 0, len(m.schemas))
	for _, s := range m.schemas {
		result = append(result, s)
	}
	return result, nil
}

func (m *mockSchemaRepository) CountEntities(_ context.Context, schemaID uuid.UUID) (int, error) {
	if m.countErr != nil {
		return 0, m.countErr
	}
	return m.entityCounts[schemaID], nil
}

// addSchema is a test helper that directly inserts a schema into the mock store.
func (m *mockSchemaRepository) addSchema(s *EntitySchema) {
	m.schemas[s.ID] = s
	m.slugIndex[s.Slug] = s.ID
}

// mockEntityRepository is an in-memory mock of EntityRepository.
type mockEntityRepository struct {
	entities     map[uuid.UUID]*Entity
	slugIndex    map[string]uuid.UUID
	versions     map[uuid.UUID][]*EntityVersion // entityID -> versions
	createErr    error
	getByIDErr   error
	getBySlugErr error
	updateErr    error
	statusErr    error
	deleteErr    error
	listErr      error
	versionErr   error
}

func newMockEntityRepository() *mockEntityRepository {
	return &mockEntityRepository{
		entities:  make(map[uuid.UUID]*Entity),
		slugIndex: make(map[string]uuid.UUID),
		versions:  make(map[uuid.UUID][]*EntityVersion),
	}
}

func (m *mockEntityRepository) Create(_ context.Context, userID *uuid.UUID, input *CreateEntityInput) (*Entity, error) {
	if m.createErr != nil {
		return nil, m.createErr
	}
	if _, exists := m.slugIndex[input.Slug]; exists {
		return nil, ErrSlugAlreadyExists
	}
	id := uuid.New()
	now := time.Now()
	metadata := input.Metadata
	if len(metadata) == 0 {
		metadata = json.RawMessage(`{}`)
	}
	e := &Entity{
		ID:         id,
		SchemaID:   input.SchemaID,
		Name:       input.Name,
		Slug:       input.Slug,
		Status:     StatusDraft,
		Attributes: input.Attributes,
		Metadata:   metadata,
		Version:    1,
		CreatedBy:  userID,
		UpdatedBy:  userID,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	m.entities[id] = e
	m.slugIndex[input.Slug] = id
	return e, nil
}

func (m *mockEntityRepository) GetByID(_ context.Context, id uuid.UUID) (*Entity, error) {
	if m.getByIDErr != nil {
		return nil, m.getByIDErr
	}
	e, ok := m.entities[id]
	if !ok {
		return nil, ErrEntityNotFound
	}
	return e, nil
}

func (m *mockEntityRepository) GetBySlug(_ context.Context, slug string) (*Entity, error) {
	if m.getBySlugErr != nil {
		return nil, m.getBySlugErr
	}
	id, ok := m.slugIndex[slug]
	if !ok {
		return nil, ErrEntityNotFound
	}
	return m.entities[id], nil
}

func (m *mockEntityRepository) Update(_ context.Context, id uuid.UUID, userID *uuid.UUID, input *UpdateEntityInput) (*Entity, error) {
	if m.updateErr != nil {
		return nil, m.updateErr
	}
	e, ok := m.entities[id]
	if !ok {
		return nil, ErrEntityNotFound
	}
	if input.Name != nil {
		e.Name = *input.Name
	}
	if input.Attributes != nil {
		e.Attributes = *input.Attributes
		e.Version++
	}
	if input.Metadata != nil {
		e.Metadata = *input.Metadata
	}
	e.UpdatedBy = userID
	e.UpdatedAt = time.Now()
	return e, nil
}

func (m *mockEntityRepository) UpdateStatus(_ context.Context, id uuid.UUID, userID *uuid.UUID, status EntityStatus) (*Entity, error) {
	if m.statusErr != nil {
		return nil, m.statusErr
	}
	e, ok := m.entities[id]
	if !ok {
		return nil, ErrEntityNotFound
	}
	e.Status = status
	e.UpdatedBy = userID
	e.UpdatedAt = time.Now()
	if status == StatusPublished {
		now := time.Now()
		e.PublishedAt = &now
	}
	// Like Postgres' UPDATE ... RETURNING: no joined schema.
	out := *e
	out.Schema = nil
	return &out, nil
}

func (m *mockEntityRepository) Delete(_ context.Context, id uuid.UUID) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}
	e, ok := m.entities[id]
	if !ok {
		return ErrEntityNotFound
	}
	delete(m.slugIndex, e.Slug)
	delete(m.entities, id)
	return nil
}

func (m *mockEntityRepository) List(_ context.Context, filter *ListEntitiesFilter, page *KeysetPage) (*PagedResult[*Entity], error) {
	if m.listErr != nil {
		return nil, m.listErr
	}

	var items []*Entity
	for _, e := range m.entities {
		if filter != nil {
			if filter.SchemaID != nil && e.SchemaID != *filter.SchemaID {
				continue
			}
			if filter.Status != nil && e.Status != *filter.Status {
				continue
			}
		}
		items = append(items, e)
	}

	limit := 20
	if page != nil && page.Limit > 0 {
		limit = page.Limit
	}

	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}

	return &PagedResult[*Entity]{
		Items:   items,
		HasMore: hasMore,
	}, nil
}

func (m *mockEntityRepository) CountBySchemaStatus(_ context.Context) ([]SchemaStatusCount, error) {
	return nil, nil
}

func (m *mockEntityRepository) CountByParent(_ context.Context, _ int) ([]ParentCount, error) {
	return nil, nil
}

func (m *mockEntityRepository) CountChildren(_ context.Context, _ []uuid.UUID) (map[uuid.UUID]int, error) {
	return map[uuid.UUID]int{}, nil
}

func (m *mockEntityRepository) ListWithOffset(_ context.Context, filter *ListEntitiesFilter, page *OffsetPage) (*OffsetPagedResult[*Entity], error) {
	if m.listErr != nil {
		return nil, m.listErr
	}

	var items []*Entity
	for _, e := range m.entities {
		if filter != nil {
			if filter.SchemaID != nil && e.SchemaID != *filter.SchemaID {
				continue
			}
			if filter.Status != nil && e.Status != *filter.Status {
				continue
			}
		}
		items = append(items, e)
	}

	limit := 20
	pageNum := 1
	if page != nil {
		if page.Limit > 0 {
			limit = page.Limit
		}
		if page.Page > 0 {
			pageNum = page.Page
		}
	}
	total := len(items)
	totalPages := max((total+limit-1)/limit, 1)

	offset := (pageNum - 1) * limit
	if offset >= len(items) {
		items = []*Entity{}
	} else {
		end := min(offset+limit, len(items))
		items = items[offset:end]
	}

	return &OffsetPagedResult[*Entity]{
		Items:      items,
		Total:      total,
		Page:       pageNum,
		Limit:      limit,
		TotalPages: totalPages,
	}, nil
}

func (m *mockEntityRepository) PruneVersions(_ context.Context, _ int) (int64, error) {
	return 0, nil
}

func (m *mockEntityRepository) CreateVersion(_ context.Context, entityID uuid.UUID, version int, attributes json.RawMessage, changedBy *uuid.UUID, changeSummary *string) (*EntityVersion, error) {
	v := &EntityVersion{
		ID:            uuid.New(),
		EntityID:      entityID,
		Version:       version,
		Attributes:    attributes,
		ChangedBy:     changedBy,
		ChangeSummary: changeSummary,
		CreatedAt:     time.Now(),
	}
	m.versions[entityID] = append(m.versions[entityID], v)
	return v, nil
}

func (m *mockEntityRepository) GetVersion(_ context.Context, entityID uuid.UUID, version int) (*EntityVersion, error) {
	if m.versionErr != nil {
		return nil, m.versionErr
	}
	for _, v := range m.versions[entityID] {
		if v.Version == version {
			return v, nil
		}
	}
	return nil, ErrVersionNotFound
}

func (m *mockEntityRepository) ListVersions(_ context.Context, entityID uuid.UUID, limit int) ([]*EntityVersion, error) {
	if m.versionErr != nil {
		return nil, m.versionErr
	}
	all := m.versions[entityID]
	if limit > 0 && limit < len(all) {
		return all[:limit], nil
	}
	return all, nil
}

// addEntity is a test helper that directly inserts an entity into the mock store.
func (m *mockEntityRepository) addEntity(e *Entity) {
	m.entities[e.ID] = e
	m.slugIndex[e.Slug] = e.ID
}

// ---------------------------------------------------------------------------
// Test Helpers
// ---------------------------------------------------------------------------

var (
	testUserID = uuid.MustParse("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")

	// A minimal valid JSON Schema for testing.
	validSchemaJSON = json.RawMessage(`{
		"type": "object",
		"properties": {
			"title": {"type": "string"},
			"body":  {"type": "string"}
		}
	}`)

	// A schema with required fields for stricter validation tests.
	strictSchemaJSON = json.RawMessage(`{
		"type": "object",
		"required": ["title"],
		"properties": {
			"title": {"type": "string", "minLength": 1},
			"body":  {"type": "string"}
		}
	}`)
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

// newTestService creates a Service wired to fresh mock repositories and returns
// the service along with both mocks so tests can seed data or inject errors.
func newTestService() (*Service, *mockSchemaRepository, *mockEntityRepository) {
	schemaRepo := newMockSchemaRepository()
	entityRepo := newMockEntityRepository()
	svc := NewService(schemaRepo, entityRepo, testLogger())
	return svc, schemaRepo, entityRepo
}

// seedSchema inserts a valid schema into the mock and returns it.
func seedSchema(t *testing.T, sr *mockSchemaRepository, opts ...func(*EntitySchema)) *EntitySchema {
	t.Helper()
	s := &EntitySchema{
		ID:        uuid.New(),
		Name:      "Blog Post",
		Slug:      "blog-post",
		Schema:    validSchemaJSON,
		IsBuiltin: false,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	for _, opt := range opts {
		opt(s)
	}
	sr.addSchema(s)
	return s
}

// seedEntity inserts a valid entity referencing the given schema into the mock.
func seedEntity(t *testing.T, er *mockEntityRepository, schema *EntitySchema, opts ...func(*Entity)) *Entity {
	t.Helper()
	e := &Entity{
		ID:         uuid.New(),
		SchemaID:   schema.ID,
		Name:       "Test Entity",
		Slug:       fmt.Sprintf("test-entity-%s", uuid.New().String()[:8]),
		Status:     StatusDraft,
		Attributes: json.RawMessage(`{"title":"Hello","body":"World"}`),
		Metadata:   json.RawMessage(`{}`),
		Version:    1,
		CreatedBy:  &testUserID,
		UpdatedBy:  &testUserID,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
		Schema:     schema,
	}
	for _, opt := range opts {
		opt(e)
	}
	er.addEntity(e)
	return e
}

// ---------------------------------------------------------------------------
// CreateEntity Tests
// ---------------------------------------------------------------------------

func TestService_CreateEntity(t *testing.T) {
	t.Parallel()

	t.Run("valid input creates entity in draft status", func(t *testing.T) {
		t.Parallel()
		svc, sr, _ := newTestService()
		schema := seedSchema(t, sr)

		input := &CreateEntityInput{
			SchemaID:   schema.ID,
			Name:       "My First Post",
			Attributes: json.RawMessage(`{"title":"My First Post","body":"Content here"}`),
		}

		entity, err := svc.CreateEntity(context.Background(), &testUserID, input)
		testutil.RequireNoError(t, err)
		testutil.Equal(t, "My First Post", entity.Name)
		testutil.Equal(t, StatusDraft, entity.Status)
		testutil.Equal(t, 1, entity.Version)
		testutil.NotNil(t, entity.Schema, "schema should be attached to response")
		testutil.Equal(t, schema.ID, entity.Schema.ID)
	})

	t.Run("slug is auto-generated from name when not provided", func(t *testing.T) {
		t.Parallel()
		svc, sr, _ := newTestService()
		schema := seedSchema(t, sr)

		input := &CreateEntityInput{
			SchemaID:   schema.ID,
			Name:       "Hello World Post",
			Attributes: json.RawMessage(`{"title":"Hello"}`),
		}

		entity, err := svc.CreateEntity(context.Background(), &testUserID, input)
		testutil.RequireNoError(t, err)
		testutil.Equal(t, "hello-world-post", entity.Slug)
	})

	t.Run("user-provided slug is normalized", func(t *testing.T) {
		t.Parallel()
		svc, sr, _ := newTestService()
		schema := seedSchema(t, sr)

		input := &CreateEntityInput{
			SchemaID:   schema.ID,
			Name:       "My Post",
			Slug:       "  Custom--SLUG!!  ",
			Attributes: json.RawMessage(`{"title":"My Post"}`),
		}

		entity, err := svc.CreateEntity(context.Background(), &testUserID, input)
		testutil.RequireNoError(t, err)
		testutil.Equal(t, "custom-slug", entity.Slug)
	})

	t.Run("slug with special characters is cleaned", func(t *testing.T) {
		t.Parallel()
		svc, sr, _ := newTestService()
		schema := seedSchema(t, sr)

		input := &CreateEntityInput{
			SchemaID:   schema.ID,
			Name:       "Cafe au Lait",
			Attributes: json.RawMessage(`{"title":"Cafe au Lait"}`),
		}

		entity, err := svc.CreateEntity(context.Background(), &testUserID, input)
		testutil.RequireNoError(t, err)
		testutil.Equal(t, "cafe-au-lait", entity.Slug)
	})

	t.Run("returns error for non-existent schema", func(t *testing.T) {
		t.Parallel()
		svc, _, _ := newTestService()

		input := &CreateEntityInput{
			SchemaID:   uuid.New(),
			Name:       "Orphan Entity",
			Attributes: json.RawMessage(`{"title":"Orphan"}`),
		}

		entity, err := svc.CreateEntity(context.Background(), &testUserID, input)
		testutil.Nil(t, entity)
		testutil.ErrorIs(t, err, ErrSchemaNotFound)
	})

	t.Run("returns error when attributes fail schema validation", func(t *testing.T) {
		t.Parallel()
		svc, sr, _ := newTestService()
		schema := seedSchema(t, sr, func(s *EntitySchema) {
			s.Schema = strictSchemaJSON
		})

		input := &CreateEntityInput{
			SchemaID:   schema.ID,
			Name:       "Invalid Entity",
			Attributes: json.RawMessage(`{"body":"missing required title"}`),
		}

		entity, err := svc.CreateEntity(context.Background(), &testUserID, input)
		testutil.Nil(t, entity)
		testutil.ErrorIs(t, err, ErrValidationFailed)
	})

	t.Run("returns error when attributes have wrong types", func(t *testing.T) {
		t.Parallel()
		svc, sr, _ := newTestService()
		schema := seedSchema(t, sr, func(s *EntitySchema) {
			s.Schema = strictSchemaJSON
		})

		input := &CreateEntityInput{
			SchemaID:   schema.ID,
			Name:       "Bad Types",
			Attributes: json.RawMessage(`{"title": 12345}`),
		}

		entity, err := svc.CreateEntity(context.Background(), &testUserID, input)
		testutil.Nil(t, entity)
		testutil.ErrorIs(t, err, ErrValidationFailed)
	})

	t.Run("returns error when repository create fails", func(t *testing.T) {
		t.Parallel()
		svc, sr, er := newTestService()
		schema := seedSchema(t, sr)

		er.createErr = errors.New("connection refused")

		input := &CreateEntityInput{
			SchemaID:   schema.ID,
			Name:       "Will Fail",
			Attributes: json.RawMessage(`{"title":"Will Fail"}`),
		}

		entity, err := svc.CreateEntity(context.Background(), &testUserID, input)
		testutil.Nil(t, entity)
		testutil.EqualError(t, err, "connection refused")
	})

	t.Run("returns error for duplicate slug", func(t *testing.T) {
		t.Parallel()
		svc, sr, _ := newTestService()
		schema := seedSchema(t, sr)

		input := &CreateEntityInput{
			SchemaID:   schema.ID,
			Name:       "First Post",
			Slug:       "unique-slug",
			Attributes: json.RawMessage(`{"title":"First"}`),
		}

		_, err := svc.CreateEntity(context.Background(), &testUserID, input)
		testutil.RequireNoError(t, err)

		// Second entity with the same slug
		input2 := &CreateEntityInput{
			SchemaID:   schema.ID,
			Name:       "Second Post",
			Slug:       "unique-slug",
			Attributes: json.RawMessage(`{"title":"Second"}`),
		}

		entity, err := svc.CreateEntity(context.Background(), &testUserID, input2)
		testutil.Nil(t, entity)
		testutil.ErrorIs(t, err, ErrSlugAlreadyExists)
	})

	t.Run("nil userID is accepted", func(t *testing.T) {
		t.Parallel()
		svc, sr, _ := newTestService()
		schema := seedSchema(t, sr)

		input := &CreateEntityInput{
			SchemaID:   schema.ID,
			Name:       "System Entity",
			Attributes: json.RawMessage(`{"title":"System"}`),
		}

		entity, err := svc.CreateEntity(context.Background(), nil, input)
		testutil.RequireNoError(t, err)
		testutil.Nil(t, entity.CreatedBy)
	})

	t.Run("empty attributes are allowed with permissive schema", func(t *testing.T) {
		t.Parallel()
		svc, sr, _ := newTestService()
		schema := seedSchema(t, sr) // uses validSchemaJSON — no required fields

		input := &CreateEntityInput{
			SchemaID:   schema.ID,
			Name:       "Empty Attrs",
			Attributes: json.RawMessage(`{}`),
		}

		entity, err := svc.CreateEntity(context.Background(), &testUserID, input)
		testutil.RequireNoError(t, err)
		testutil.Equal(t, "Empty Attrs", entity.Name)
	})
}

// ---------------------------------------------------------------------------
// UpdateEntity Tests
// ---------------------------------------------------------------------------

func TestService_UpdateEntity(t *testing.T) {
	t.Parallel()

	t.Run("partial update changes only specified fields", func(t *testing.T) {
		t.Parallel()
		svc, sr, er := newTestService()
		schema := seedSchema(t, sr)
		existing := seedEntity(t, er, schema)

		newName := "Updated Title"
		input := &UpdateEntityInput{
			Name: &newName,
		}

		updated, err := svc.UpdateEntity(context.Background(), existing.ID, &testUserID, input)
		testutil.RequireNoError(t, err)
		testutil.Equal(t, "Updated Title", updated.Name)
		// Attributes should remain unchanged
		testutil.JSONEqual(t, `{"title":"Hello","body":"World"}`, string(updated.Attributes))
	})

	t.Run("updating attributes increments version", func(t *testing.T) {
		t.Parallel()
		svc, sr, er := newTestService()
		schema := seedSchema(t, sr)
		existing := seedEntity(t, er, schema)
		originalVersion := existing.Version

		newAttrs := json.RawMessage(`{"title":"Updated Title","body":"Updated Body"}`)
		input := &UpdateEntityInput{
			Attributes: &newAttrs,
		}

		updated, err := svc.UpdateEntity(context.Background(), existing.ID, &testUserID, input)
		testutil.RequireNoError(t, err)
		testutil.Equal(t, originalVersion+1, updated.Version)
		testutil.JSONEqual(t, `{"title":"Updated Title","body":"Updated Body"}`, string(updated.Attributes))
	})

	t.Run("updating only name does not increment version", func(t *testing.T) {
		t.Parallel()
		svc, sr, er := newTestService()
		schema := seedSchema(t, sr)
		existing := seedEntity(t, er, schema)
		originalVersion := existing.Version

		newName := "Just a Name Change"
		input := &UpdateEntityInput{
			Name: &newName,
		}

		updated, err := svc.UpdateEntity(context.Background(), existing.ID, &testUserID, input)
		testutil.RequireNoError(t, err)
		testutil.Equal(t, originalVersion, updated.Version, "version should not increment when only name changes")
	})

	t.Run("schema is attached to response from existing entity", func(t *testing.T) {
		t.Parallel()
		svc, sr, er := newTestService()
		schema := seedSchema(t, sr)
		existing := seedEntity(t, er, schema)

		newName := "Schema Test"
		input := &UpdateEntityInput{
			Name: &newName,
		}

		updated, err := svc.UpdateEntity(context.Background(), existing.ID, &testUserID, input)
		testutil.RequireNoError(t, err)
		testutil.RequireNotNil(t, updated.Schema)
		testutil.Equal(t, schema.ID, updated.Schema.ID)
	})

	t.Run("returns error for non-existent entity", func(t *testing.T) {
		t.Parallel()
		svc, _, _ := newTestService()

		newName := "Ghost"
		input := &UpdateEntityInput{
			Name: &newName,
		}

		updated, err := svc.UpdateEntity(context.Background(), uuid.New(), &testUserID, input)
		testutil.Nil(t, updated)
		testutil.ErrorIs(t, err, ErrEntityNotFound)
	})

	t.Run("returns error when new attributes fail schema validation", func(t *testing.T) {
		t.Parallel()
		svc, sr, er := newTestService()
		schema := seedSchema(t, sr, func(s *EntitySchema) {
			s.Schema = strictSchemaJSON
		})
		existing := seedEntity(t, er, schema)

		badAttrs := json.RawMessage(`{"body":"no title field"}`)
		input := &UpdateEntityInput{
			Attributes: &badAttrs,
		}

		updated, err := svc.UpdateEntity(context.Background(), existing.ID, &testUserID, input)
		testutil.Nil(t, updated)
		testutil.ErrorIs(t, err, ErrValidationFailed)
	})

	t.Run("returns error when repository update fails", func(t *testing.T) {
		t.Parallel()
		svc, sr, er := newTestService()
		schema := seedSchema(t, sr)
		existing := seedEntity(t, er, schema)

		er.updateErr = errors.New("disk full")

		newName := "Will Fail"
		input := &UpdateEntityInput{
			Name: &newName,
		}

		updated, err := svc.UpdateEntity(context.Background(), existing.ID, &testUserID, input)
		testutil.Nil(t, updated)
		testutil.EqualError(t, err, "disk full")
	})

	t.Run("metadata can be updated independently", func(t *testing.T) {
		t.Parallel()
		svc, sr, er := newTestService()
		schema := seedSchema(t, sr)
		existing := seedEntity(t, er, schema)
		originalVersion := existing.Version

		newMeta := json.RawMessage(`{"seo_title":"Custom SEO Title"}`)
		input := &UpdateEntityInput{
			Metadata: &newMeta,
		}

		updated, err := svc.UpdateEntity(context.Background(), existing.ID, &testUserID, input)
		testutil.RequireNoError(t, err)
		testutil.JSONEqual(t, `{"seo_title":"Custom SEO Title"}`, string(updated.Metadata))
		testutil.Equal(t, originalVersion, updated.Version, "metadata-only change should not bump version")
	})
}

// ---------------------------------------------------------------------------
// UpdateEntityStatus (Publish / Unpublish) Tests
// ---------------------------------------------------------------------------

func TestService_UpdateEntityStatus(t *testing.T) {
	t.Parallel()

	t.Run("publish a draft entity", func(t *testing.T) {
		t.Parallel()
		svc, sr, er := newTestService()
		schema := seedSchema(t, sr)
		existing := seedEntity(t, er, schema)

		published, err := svc.UpdateEntityStatus(context.Background(), existing.ID, &testUserID, StatusPublished)
		testutil.RequireNoError(t, err)
		testutil.Equal(t, StatusPublished, published.Status)
		testutil.NotNil(t, published.PublishedAt, "published_at should be set when publishing")
	})

	t.Run("unpublish a published entity by setting to draft", func(t *testing.T) {
		t.Parallel()
		svc, sr, er := newTestService()
		schema := seedSchema(t, sr)
		existing := seedEntity(t, er, schema, func(e *Entity) {
			e.Status = StatusPublished
			now := time.Now()
			e.PublishedAt = &now
		})

		unpublished, err := svc.UpdateEntityStatus(context.Background(), existing.ID, &testUserID, StatusDraft)
		testutil.RequireNoError(t, err)
		testutil.Equal(t, StatusDraft, unpublished.Status)
	})

	t.Run("archive a published entity", func(t *testing.T) {
		t.Parallel()
		svc, sr, er := newTestService()
		schema := seedSchema(t, sr)
		existing := seedEntity(t, er, schema, func(e *Entity) {
			e.Status = StatusPublished
		})

		archived, err := svc.UpdateEntityStatus(context.Background(), existing.ID, &testUserID, StatusArchived)
		testutil.RequireNoError(t, err)
		testutil.Equal(t, StatusArchived, archived.Status)
	})

	t.Run("publish an already published entity is idempotent", func(t *testing.T) {
		t.Parallel()
		svc, sr, er := newTestService()
		schema := seedSchema(t, sr)
		existing := seedEntity(t, er, schema, func(e *Entity) {
			e.Status = StatusPublished
			now := time.Now()
			e.PublishedAt = &now
		})

		result, err := svc.UpdateEntityStatus(context.Background(), existing.ID, &testUserID, StatusPublished)
		testutil.RequireNoError(t, err)
		testutil.Equal(t, StatusPublished, result.Status)
	})

	t.Run("returns error for invalid status string", func(t *testing.T) {
		t.Parallel()
		svc, sr, er := newTestService()
		schema := seedSchema(t, sr)
		existing := seedEntity(t, er, schema)

		result, err := svc.UpdateEntityStatus(context.Background(), existing.ID, &testUserID, EntityStatus("bogus"))
		testutil.Nil(t, result)
		testutil.ErrorIs(t, err, ErrInvalidStatus)
	})

	t.Run("returns error for empty status", func(t *testing.T) {
		t.Parallel()
		svc, _, _ := newTestService()

		result, err := svc.UpdateEntityStatus(context.Background(), uuid.New(), &testUserID, EntityStatus(""))
		testutil.Nil(t, result)
		testutil.ErrorIs(t, err, ErrInvalidStatus)
	})

	t.Run("returns error when entity not found", func(t *testing.T) {
		t.Parallel()
		svc, _, _ := newTestService()

		result, err := svc.UpdateEntityStatus(context.Background(), uuid.New(), &testUserID, StatusPublished)
		testutil.Nil(t, result)
		testutil.ErrorIs(t, err, ErrEntityNotFound)
	})

	t.Run("returns error when repository fails", func(t *testing.T) {
		t.Parallel()
		svc, sr, er := newTestService()
		schema := seedSchema(t, sr)
		existing := seedEntity(t, er, schema)

		er.statusErr = errors.New("timeout")

		result, err := svc.UpdateEntityStatus(context.Background(), existing.ID, &testUserID, StatusPublished)
		testutil.Nil(t, result)
		testutil.EqualError(t, err, "timeout")
	})
}

// ---------------------------------------------------------------------------
// DeleteEntity Tests
// ---------------------------------------------------------------------------

func TestService_DeleteEntity(t *testing.T) {
	t.Parallel()

	t.Run("successfully deletes existing entity", func(t *testing.T) {
		t.Parallel()
		svc, sr, er := newTestService()
		schema := seedSchema(t, sr)
		existing := seedEntity(t, er, schema)

		err := svc.DeleteEntity(context.Background(), existing.ID)
		testutil.RequireNoError(t, err)

		// Verify it is gone
		_, getErr := svc.GetEntity(context.Background(), existing.ID)
		testutil.ErrorIs(t, getErr, ErrEntityNotFound)
	})

	t.Run("returns error when entity not found", func(t *testing.T) {
		t.Parallel()
		svc, _, _ := newTestService()

		err := svc.DeleteEntity(context.Background(), uuid.New())
		testutil.ErrorIs(t, err, ErrEntityNotFound)
	})

	t.Run("returns error when repository fails", func(t *testing.T) {
		t.Parallel()
		svc, _, er := newTestService()

		er.deleteErr = errors.New("permission denied")

		err := svc.DeleteEntity(context.Background(), uuid.New())
		testutil.EqualError(t, err, "permission denied")
	})
}

// ---------------------------------------------------------------------------
// GetEntity Tests
// ---------------------------------------------------------------------------

func TestService_GetEntity(t *testing.T) {
	t.Parallel()

	t.Run("returns entity when found", func(t *testing.T) {
		t.Parallel()
		svc, sr, er := newTestService()
		schema := seedSchema(t, sr)
		existing := seedEntity(t, er, schema)

		entity, err := svc.GetEntity(context.Background(), existing.ID)
		testutil.RequireNoError(t, err)
		testutil.Equal(t, existing.ID, entity.ID)
		testutil.Equal(t, existing.Name, entity.Name)
	})

	t.Run("returns error when not found", func(t *testing.T) {
		t.Parallel()
		svc, _, _ := newTestService()

		entity, err := svc.GetEntity(context.Background(), uuid.New())
		testutil.Nil(t, entity)
		testutil.ErrorIs(t, err, ErrEntityNotFound)
	})

	t.Run("returns error when repository fails", func(t *testing.T) {
		t.Parallel()
		svc, _, er := newTestService()

		er.getByIDErr = errors.New("connection reset")

		entity, err := svc.GetEntity(context.Background(), uuid.New())
		testutil.Nil(t, entity)
		testutil.EqualError(t, err, "connection reset")
	})
}

// ---------------------------------------------------------------------------
// GetEntityBySlug Tests
// ---------------------------------------------------------------------------

func TestService_GetEntityBySlug(t *testing.T) {
	t.Parallel()

	t.Run("returns entity when slug matches", func(t *testing.T) {
		t.Parallel()
		svc, sr, er := newTestService()
		schema := seedSchema(t, sr)
		existing := seedEntity(t, er, schema, func(e *Entity) {
			e.Slug = "my-specific-slug"
		})

		entity, err := svc.GetEntityBySlug(context.Background(), "my-specific-slug")
		testutil.RequireNoError(t, err)
		testutil.Equal(t, existing.ID, entity.ID)
	})

	t.Run("returns error when slug not found", func(t *testing.T) {
		t.Parallel()
		svc, _, _ := newTestService()

		entity, err := svc.GetEntityBySlug(context.Background(), "no-such-slug")
		testutil.Nil(t, entity)
		testutil.ErrorIs(t, err, ErrEntityNotFound)
	})
}

// ---------------------------------------------------------------------------
// ListEntities Tests
// ---------------------------------------------------------------------------

func TestService_ListEntities(t *testing.T) {
	t.Parallel()

	t.Run("returns all entities when no filter is applied", func(t *testing.T) {
		t.Parallel()
		svc, sr, er := newTestService()
		schema := seedSchema(t, sr)
		seedEntity(t, er, schema)
		seedEntity(t, er, schema)
		seedEntity(t, er, schema)

		result, err := svc.ListEntities(context.Background(), nil, nil)
		testutil.RequireNoError(t, err)
		testutil.Len(t, result.Items, 3)
	})

	t.Run("filters by schema ID", func(t *testing.T) {
		t.Parallel()
		svc, sr, er := newTestService()

		schema1 := seedSchema(t, sr, func(s *EntitySchema) {
			s.Slug = "type-a"
		})
		schema2 := seedSchema(t, sr, func(s *EntitySchema) {
			s.Slug = "type-b"
		})

		seedEntity(t, er, schema1)
		seedEntity(t, er, schema1)
		seedEntity(t, er, schema2)

		result, err := svc.ListEntities(context.Background(), &ListEntitiesFilter{
			SchemaID: &schema1.ID,
		}, nil)
		testutil.RequireNoError(t, err)
		testutil.Len(t, result.Items, 2)
		for _, e := range result.Items {
			testutil.Equal(t, schema1.ID, e.SchemaID)
		}
	})

	t.Run("filters by status", func(t *testing.T) {
		t.Parallel()
		svc, sr, er := newTestService()
		schema := seedSchema(t, sr)

		seedEntity(t, er, schema, func(e *Entity) { e.Status = StatusDraft })
		seedEntity(t, er, schema, func(e *Entity) { e.Status = StatusPublished })
		seedEntity(t, er, schema, func(e *Entity) { e.Status = StatusPublished })
		seedEntity(t, er, schema, func(e *Entity) { e.Status = StatusArchived })

		published := StatusPublished
		result, err := svc.ListEntities(context.Background(), &ListEntitiesFilter{
			Status: &published,
		}, nil)
		testutil.RequireNoError(t, err)
		testutil.Len(t, result.Items, 2)
		for _, e := range result.Items {
			testutil.Equal(t, StatusPublished, e.Status)
		}
	})

	t.Run("respects pagination limit", func(t *testing.T) {
		t.Parallel()
		svc, sr, er := newTestService()
		schema := seedSchema(t, sr)

		for range 5 {
			seedEntity(t, er, schema)
		}

		result, err := svc.ListEntities(context.Background(), nil, &KeysetPage{Limit: 2})
		testutil.RequireNoError(t, err)
		testutil.LessOrEqual(t, len(result.Items), 2)
		testutil.True(t, result.HasMore)
	})

	t.Run("returns empty list when no entities exist", func(t *testing.T) {
		t.Parallel()
		svc, _, _ := newTestService()

		result, err := svc.ListEntities(context.Background(), nil, nil)
		testutil.RequireNoError(t, err)
		testutil.Empty(t, result.Items)
		testutil.False(t, result.HasMore)
	})

	t.Run("returns error when repository fails", func(t *testing.T) {
		t.Parallel()
		svc, _, er := newTestService()

		er.listErr = errors.New("query timeout")

		result, err := svc.ListEntities(context.Background(), nil, nil)
		testutil.Nil(t, result)
		testutil.EqualError(t, err, "query timeout")
	})
}

// ---------------------------------------------------------------------------
// ListEntitiesWithOffset Tests
// ---------------------------------------------------------------------------

func TestService_ListEntitiesWithOffset(t *testing.T) {
	t.Parallel()

	t.Run("returns paginated results with total count", func(t *testing.T) {
		t.Parallel()
		svc, sr, er := newTestService()
		schema := seedSchema(t, sr)

		for range 5 {
			seedEntity(t, er, schema)
		}

		result, err := svc.ListEntitiesWithOffset(context.Background(), nil, &OffsetPage{Page: 1, Limit: 2})
		testutil.RequireNoError(t, err)
		testutil.Equal(t, 5, result.Total)
		testutil.Equal(t, 1, result.Page)
		testutil.Equal(t, 2, result.Limit)
		testutil.Equal(t, 3, result.TotalPages)
		testutil.LessOrEqual(t, len(result.Items), 2)
	})

	t.Run("returns empty results for page beyond range", func(t *testing.T) {
		t.Parallel()
		svc, sr, er := newTestService()
		schema := seedSchema(t, sr)

		seedEntity(t, er, schema)
		seedEntity(t, er, schema)

		result, err := svc.ListEntitiesWithOffset(context.Background(), nil, &OffsetPage{Page: 99, Limit: 10})
		testutil.RequireNoError(t, err)
		testutil.Empty(t, result.Items)
		testutil.Equal(t, 2, result.Total)
	})
}

// ---------------------------------------------------------------------------
// Schema CRUD Tests
// ---------------------------------------------------------------------------

func TestService_CreateSchema(t *testing.T) {
	t.Parallel()

	t.Run("creates schema with auto-generated slug", func(t *testing.T) {
		t.Parallel()
		svc, _, _ := newTestService()

		input := &CreateSchemaInput{
			Name:   "Blog Post",
			Schema: validSchemaJSON,
		}

		schema, err := svc.CreateSchema(context.Background(), input)
		testutil.RequireNoError(t, err)
		testutil.Equal(t, "Blog Post", schema.Name)
		testutil.Equal(t, "blog-post", schema.Slug)
		testutil.False(t, schema.IsBuiltin)
	})

	t.Run("normalizes user-provided slug", func(t *testing.T) {
		t.Parallel()
		svc, _, _ := newTestService()

		input := &CreateSchemaInput{
			Name:   "Case Study",
			Slug:   " Case--STUDY!! ",
			Schema: validSchemaJSON,
		}

		schema, err := svc.CreateSchema(context.Background(), input)
		testutil.RequireNoError(t, err)
		testutil.Equal(t, "case-study", schema.Slug)
	})

	t.Run("returns error for invalid JSON schema", func(t *testing.T) {
		t.Parallel()
		svc, _, _ := newTestService()

		input := &CreateSchemaInput{
			Name:   "Bad Schema",
			Schema: json.RawMessage(`{"not_a_schema": true}`),
		}

		schema, err := svc.CreateSchema(context.Background(), input)
		testutil.Nil(t, schema)
		testutil.ErrorIs(t, err, ErrInvalidSchema)
	})

	t.Run("returns error for empty schema", func(t *testing.T) {
		t.Parallel()
		svc, _, _ := newTestService()

		input := &CreateSchemaInput{
			Name:   "Empty",
			Schema: json.RawMessage(``),
		}

		schema, err := svc.CreateSchema(context.Background(), input)
		testutil.Nil(t, schema)
		testutil.ErrorIs(t, err, ErrInvalidSchema)
	})
}

func TestService_UpdateSchema(t *testing.T) {
	t.Parallel()

	t.Run("updates non-builtin schema name", func(t *testing.T) {
		t.Parallel()
		svc, sr, _ := newTestService()
		existing := seedSchema(t, sr)

		newName := "Updated Name"
		updated, err := svc.UpdateSchema(context.Background(), existing.ID, &UpdateSchemaInput{
			Name: &newName,
		})
		testutil.RequireNoError(t, err)
		testutil.Equal(t, "Updated Name", updated.Name)
	})

	t.Run("rejects update of builtin schema", func(t *testing.T) {
		t.Parallel()
		svc, sr, _ := newTestService()
		existing := seedSchema(t, sr, func(s *EntitySchema) {
			s.IsBuiltin = true
		})

		newName := "Nope"
		updated, err := svc.UpdateSchema(context.Background(), existing.ID, &UpdateSchemaInput{
			Name: &newName,
		})
		testutil.Nil(t, updated)
		testutil.ErrorIs(t, err, ErrCannotModifyBuiltin)
	})

	t.Run("validates new schema JSON when provided", func(t *testing.T) {
		t.Parallel()
		svc, sr, _ := newTestService()
		existing := seedSchema(t, sr)

		badSchema := json.RawMessage(`{"no_type_field": true}`)
		updated, err := svc.UpdateSchema(context.Background(), existing.ID, &UpdateSchemaInput{
			Schema: &badSchema,
		})
		testutil.Nil(t, updated)
		testutil.ErrorIs(t, err, ErrInvalidSchema)
	})

	t.Run("returns error for non-existent schema", func(t *testing.T) {
		t.Parallel()
		svc, _, _ := newTestService()

		newName := "Ghost"
		updated, err := svc.UpdateSchema(context.Background(), uuid.New(), &UpdateSchemaInput{
			Name: &newName,
		})
		testutil.Nil(t, updated)
		testutil.ErrorIs(t, err, ErrSchemaNotFound)
	})
}

func TestService_DeleteSchema(t *testing.T) {
	t.Parallel()

	t.Run("deletes unused non-builtin schema", func(t *testing.T) {
		t.Parallel()
		svc, sr, _ := newTestService()
		existing := seedSchema(t, sr)

		err := svc.DeleteSchema(context.Background(), existing.ID)
		testutil.RequireNoError(t, err)

		// Verify it is gone
		_, getErr := svc.GetSchema(context.Background(), existing.ID)
		testutil.ErrorIs(t, getErr, ErrSchemaNotFound)
	})

	t.Run("rejects deletion of builtin schema", func(t *testing.T) {
		t.Parallel()
		svc, sr, _ := newTestService()
		existing := seedSchema(t, sr, func(s *EntitySchema) {
			s.IsBuiltin = true
		})

		err := svc.DeleteSchema(context.Background(), existing.ID)
		testutil.ErrorIs(t, err, ErrCannotDeleteBuiltin)
	})

	t.Run("rejects deletion when schema is in use", func(t *testing.T) {
		t.Parallel()
		svc, sr, _ := newTestService()
		existing := seedSchema(t, sr)
		sr.entityCounts[existing.ID] = 5

		err := svc.DeleteSchema(context.Background(), existing.ID)
		testutil.ErrorIs(t, err, ErrSchemaInUse)
	})

	t.Run("returns error for non-existent schema", func(t *testing.T) {
		t.Parallel()
		svc, _, _ := newTestService()

		err := svc.DeleteSchema(context.Background(), uuid.New())
		testutil.ErrorIs(t, err, ErrSchemaNotFound)
	})
}

// ---------------------------------------------------------------------------
// Version Operations Tests
// ---------------------------------------------------------------------------

func TestService_ListVersions(t *testing.T) {
	t.Parallel()

	t.Run("returns versions for existing entity", func(t *testing.T) {
		t.Parallel()
		svc, sr, er := newTestService()
		schema := seedSchema(t, sr)
		existing := seedEntity(t, er, schema)

		// Seed two version records
		er.versions[existing.ID] = []*EntityVersion{
			{ID: uuid.New(), EntityID: existing.ID, Version: 1, Attributes: json.RawMessage(`{"title":"v1"}`)},
			{ID: uuid.New(), EntityID: existing.ID, Version: 2, Attributes: json.RawMessage(`{"title":"v2"}`)},
		}

		versions, err := svc.ListVersions(context.Background(), existing.ID, 50)
		testutil.RequireNoError(t, err)
		testutil.Len(t, versions, 2)
	})

	t.Run("returns error when entity does not exist", func(t *testing.T) {
		t.Parallel()
		svc, _, _ := newTestService()

		versions, err := svc.ListVersions(context.Background(), uuid.New(), 10)
		testutil.Nil(t, versions)
		testutil.ErrorIs(t, err, ErrEntityNotFound)
	})
}

func TestService_GetVersion(t *testing.T) {
	t.Parallel()

	t.Run("returns specific version", func(t *testing.T) {
		t.Parallel()
		svc, sr, er := newTestService()
		schema := seedSchema(t, sr)
		existing := seedEntity(t, er, schema)

		er.versions[existing.ID] = []*EntityVersion{
			{ID: uuid.New(), EntityID: existing.ID, Version: 1, Attributes: json.RawMessage(`{"title":"v1"}`)},
		}

		ver, err := svc.GetVersion(context.Background(), existing.ID, 1)
		testutil.RequireNoError(t, err)
		testutil.Equal(t, 1, ver.Version)
	})

	t.Run("returns error for non-existent version", func(t *testing.T) {
		t.Parallel()
		svc, sr, er := newTestService()
		schema := seedSchema(t, sr)
		existing := seedEntity(t, er, schema)
		_ = existing

		ver, err := svc.GetVersion(context.Background(), existing.ID, 999)
		testutil.Nil(t, ver)
		testutil.ErrorIs(t, err, ErrVersionNotFound)
	})
}

func TestService_RestoreVersion(t *testing.T) {
	t.Parallel()

	t.Run("restores entity to a previous version", func(t *testing.T) {
		t.Parallel()
		svc, sr, er := newTestService()
		schema := seedSchema(t, sr)
		existing := seedEntity(t, er, schema, func(e *Entity) {
			e.Attributes = json.RawMessage(`{"title":"Current","body":"current body"}`)
			e.Version = 3
		})

		// Seed an older version snapshot
		oldAttrs := json.RawMessage(`{"title":"Old Title","body":"old body"}`)
		er.versions[existing.ID] = []*EntityVersion{
			{ID: uuid.New(), EntityID: existing.ID, Version: 1, Attributes: oldAttrs},
		}

		restored, err := svc.RestoreVersion(context.Background(), existing.ID, &testUserID, 1)
		testutil.RequireNoError(t, err)
		testutil.JSONEqual(t, `{"title":"Old Title","body":"old body"}`, string(restored.Attributes))
		testutil.Equal(t, 4, restored.Version, "version should increment on restore")
		testutil.NotNil(t, restored.Schema)
	})

	t.Run("returns error when restored attributes violate current schema", func(t *testing.T) {
		t.Parallel()
		svc, sr, er := newTestService()
		schema := seedSchema(t, sr, func(s *EntitySchema) {
			s.Schema = strictSchemaJSON // requires "title"
		})
		existing := seedEntity(t, er, schema)

		// Old version has no "title" — will fail validation against current schema
		er.versions[existing.ID] = []*EntityVersion{
			{ID: uuid.New(), EntityID: existing.ID, Version: 1, Attributes: json.RawMessage(`{"body":"no title"}`)},
		}

		restored, err := svc.RestoreVersion(context.Background(), existing.ID, &testUserID, 1)
		testutil.Nil(t, restored)
		testutil.Error(t, err)
		testutil.Contains(t, err.Error(), "cannot restore version 1")
	})

	t.Run("returns error when version does not exist", func(t *testing.T) {
		t.Parallel()
		svc, sr, er := newTestService()
		schema := seedSchema(t, sr)
		existing := seedEntity(t, er, schema)
		_ = existing

		restored, err := svc.RestoreVersion(context.Background(), existing.ID, &testUserID, 999)
		testutil.Nil(t, restored)
		testutil.ErrorIs(t, err, ErrVersionNotFound)
	})

	t.Run("returns error when entity does not exist", func(t *testing.T) {
		t.Parallel()
		svc, _, er := newTestService()

		fakeID := uuid.New()
		// Seed a version for a non-existent entity — GetVersion will find it but
		// GetByID will fail.
		er.versions[fakeID] = []*EntityVersion{
			{ID: uuid.New(), EntityID: fakeID, Version: 1, Attributes: json.RawMessage(`{}`)},
		}

		restored, err := svc.RestoreVersion(context.Background(), fakeID, &testUserID, 1)
		testutil.Nil(t, restored)
		testutil.ErrorIs(t, err, ErrEntityNotFound)
	})
}

// ---------------------------------------------------------------------------
// ComputeDiff Tests
// ---------------------------------------------------------------------------

func TestService_ComputeDiff(t *testing.T) {
	t.Parallel()

	t.Run("detects changed fields", func(t *testing.T) {
		t.Parallel()
		svc, sr, er := newTestService()
		schema := seedSchema(t, sr)
		existing := seedEntity(t, er, schema)

		er.versions[existing.ID] = []*EntityVersion{
			{ID: uuid.New(), EntityID: existing.ID, Version: 1, Attributes: json.RawMessage(`{"title":"Old","body":"Same"}`)},
			{ID: uuid.New(), EntityID: existing.ID, Version: 2, Attributes: json.RawMessage(`{"title":"New","body":"Same"}`)},
		}

		diffs, err := svc.ComputeDiff(context.Background(), existing.ID, 1, 2)
		testutil.RequireNoError(t, err)
		testutil.Len(t, diffs, 1, "only title changed")
		testutil.Equal(t, "title", diffs[0].Field)
	})

	t.Run("detects added fields", func(t *testing.T) {
		t.Parallel()
		svc, sr, er := newTestService()
		schema := seedSchema(t, sr)
		existing := seedEntity(t, er, schema)

		er.versions[existing.ID] = []*EntityVersion{
			{ID: uuid.New(), EntityID: existing.ID, Version: 1, Attributes: json.RawMessage(`{"title":"A"}`)},
			{ID: uuid.New(), EntityID: existing.ID, Version: 2, Attributes: json.RawMessage(`{"title":"A","body":"New field"}`)},
		}

		diffs, err := svc.ComputeDiff(context.Background(), existing.ID, 1, 2)
		testutil.RequireNoError(t, err)

		var bodyDiff *VersionDiff
		for i := range diffs {
			if diffs[i].Field == "body" {
				bodyDiff = &diffs[i]
			}
		}
		testutil.RequireNotNil(t, bodyDiff, "should detect added 'body' field")
		testutil.Nil(t, bodyDiff.OldValue)
		testutil.Equal(t, "New field", bodyDiff.NewValue)
	})

	t.Run("detects removed fields", func(t *testing.T) {
		t.Parallel()
		svc, sr, er := newTestService()
		schema := seedSchema(t, sr)
		existing := seedEntity(t, er, schema)

		er.versions[existing.ID] = []*EntityVersion{
			{ID: uuid.New(), EntityID: existing.ID, Version: 1, Attributes: json.RawMessage(`{"title":"A","body":"Remove me"}`)},
			{ID: uuid.New(), EntityID: existing.ID, Version: 2, Attributes: json.RawMessage(`{"title":"A"}`)},
		}

		diffs, err := svc.ComputeDiff(context.Background(), existing.ID, 1, 2)
		testutil.RequireNoError(t, err)

		var bodyDiff *VersionDiff
		for i := range diffs {
			if diffs[i].Field == "body" {
				bodyDiff = &diffs[i]
			}
		}
		testutil.RequireNotNil(t, bodyDiff, "should detect removed 'body' field")
		testutil.Equal(t, "Remove me", bodyDiff.OldValue)
		testutil.Nil(t, bodyDiff.NewValue)
	})

	t.Run("no diffs for identical versions", func(t *testing.T) {
		t.Parallel()
		svc, sr, er := newTestService()
		schema := seedSchema(t, sr)
		existing := seedEntity(t, er, schema)

		er.versions[existing.ID] = []*EntityVersion{
			{ID: uuid.New(), EntityID: existing.ID, Version: 1, Attributes: json.RawMessage(`{"title":"Same"}`)},
			{ID: uuid.New(), EntityID: existing.ID, Version: 2, Attributes: json.RawMessage(`{"title":"Same"}`)},
		}

		diffs, err := svc.ComputeDiff(context.Background(), existing.ID, 1, 2)
		testutil.RequireNoError(t, err)
		testutil.Empty(t, diffs)
	})

	t.Run("returns error when from-version not found", func(t *testing.T) {
		t.Parallel()
		svc, sr, er := newTestService()
		schema := seedSchema(t, sr)
		existing := seedEntity(t, er, schema)
		_ = existing

		diffs, err := svc.ComputeDiff(context.Background(), existing.ID, 1, 2)
		testutil.Nil(t, diffs)
		testutil.ErrorIs(t, err, ErrVersionNotFound)
	})
}

// ---------------------------------------------------------------------------
// ValidateEntityAttributes Tests
// ---------------------------------------------------------------------------

func TestService_ValidateEntityAttributes(t *testing.T) {
	t.Parallel()

	t.Run("returns valid for conforming attributes", func(t *testing.T) {
		t.Parallel()
		svc, sr, _ := newTestService()
		schema := seedSchema(t, sr, func(s *EntitySchema) {
			s.Schema = strictSchemaJSON
		})

		result := svc.ValidateEntityAttributes(
			context.Background(),
			schema.ID,
			json.RawMessage(`{"title":"Good"}`),
		)
		testutil.True(t, result.Valid)
		testutil.Empty(t, result.Errors)
	})

	t.Run("returns invalid for non-conforming attributes", func(t *testing.T) {
		t.Parallel()
		svc, sr, _ := newTestService()
		schema := seedSchema(t, sr, func(s *EntitySchema) {
			s.Schema = strictSchemaJSON
		})

		result := svc.ValidateEntityAttributes(
			context.Background(),
			schema.ID,
			json.RawMessage(`{"body":"missing title"}`),
		)
		testutil.False(t, result.Valid)
		testutil.NotEmpty(t, result.Errors)
	})

	t.Run("returns invalid when schema not found", func(t *testing.T) {
		t.Parallel()
		svc, _, _ := newTestService()

		result := svc.ValidateEntityAttributes(
			context.Background(),
			uuid.New(),
			json.RawMessage(`{}`),
		)
		testutil.False(t, result.Valid)
		testutil.NotEmpty(t, result.Errors)
		testutil.Contains(t, result.Errors[0].Message, "schema not found")
	})
}

// ---------------------------------------------------------------------------
// Slug Generation Edge Cases (via service layer)
// ---------------------------------------------------------------------------

func TestGenerateSlug_EdgeCases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "unicode accented characters are stripped",
			input: "Cafe au Lait",
			want:  "cafe-au-lait",
		},
		{
			name:  "leading and trailing dashes removed",
			input: "---hello---",
			want:  "hello",
		},
		{
			name:  "multiple consecutive special chars collapse to single dash",
			input: "a!!!b@@@c",
			want:  "a-b-c",
		},
		{
			name:  "numbers are preserved",
			input: "Top 10 Things in 2026",
			want:  "top-10-things-in-2026",
		},
		{
			name:  "mixed case normalized to lowercase",
			input: "CamelCase AND UPPER",
			want:  "camelcase-and-upper",
		},
		{
			name:  "underscores replaced by dashes",
			input: "snake_case_slug",
			want:  "snake-case-slug",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := generateSlug(tt.input)
			testutil.Equal(t, tt.want, got)
		})
	}
}

func TestNormalizeSlug(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "trims whitespace and lowercases",
			input: "  My Slug  ",
			want:  "my-slug",
		},
		{
			name:  "collapses multiple dashes",
			input: "a---b---c",
			want:  "a-b-c",
		},
		{
			name:  "strips leading and trailing dashes",
			input: "--hello-world--",
			want:  "hello-world",
		},
		{
			name:  "already clean slug passes through",
			input: "clean-slug",
			want:  "clean-slug",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := normalizeSlug(tt.input)
			testutil.Equal(t, tt.want, got)
		})
	}
}

// ---------------------------------------------------------------------------
// computeJSONDiff Unit Tests
// ---------------------------------------------------------------------------

func TestComputeJSONDiff(t *testing.T) {
	t.Parallel()

	t.Run("returns error for invalid old JSON", func(t *testing.T) {
		t.Parallel()
		_, err := computeJSONDiff(json.RawMessage(`{invalid`), json.RawMessage(`{}`))
		testutil.Error(t, err)
		testutil.Contains(t, err.Error(), "failed to parse old JSON")
	})

	t.Run("returns error for invalid new JSON", func(t *testing.T) {
		t.Parallel()
		_, err := computeJSONDiff(json.RawMessage(`{}`), json.RawMessage(`{invalid`))
		testutil.Error(t, err)
		testutil.Contains(t, err.Error(), "failed to parse new JSON")
	})

	t.Run("empty objects produce no diffs", func(t *testing.T) {
		t.Parallel()
		diffs, err := computeJSONDiff(json.RawMessage(`{}`), json.RawMessage(`{}`))
		testutil.RequireNoError(t, err)
		testutil.Empty(t, diffs)
	})
}
