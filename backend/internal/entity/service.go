package entity

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"golang.org/x/text/unicode/norm"
)

// EntityLinkSync is the v2 linking surface the entity service calls after a
// successful update. Kept as an interface so:
//   - entity does not need to import internal/linking/v2 (one-way dep)
//   - tests can pass a noop and assert without DB
//
// Both methods must accept a fresh context.Background() — the request
// context the HTTP handler holds is cancelled when the response writes,
// and the v2 sync runs in a goroutine that outlives the request.
type EntityLinkSync interface {
	// Reindex refreshes hatamoto_entity_link_index for sourceEntityID based on
	// whatever <a data-entity-id> tags currently live in its stored content.
	// See internal/linking/v2/indexer.go.
	Reindex(ctx context.Context, sourceEntityID uuid.UUID) error

	// RewriteForEntity rewrites the href of every <a data-entity-id="X">
	// across the codebase to newCanonicalURL when X's own slug changes. See internal/linking/v2/rewriter.go.
	RewriteForEntity(ctx context.Context, targetEntityID uuid.UUID, newCanonicalURL, reason string, actor *uuid.UUID) error

	// ResolveCanonical returns the post-update canonical URL for the entity
	// whose slug changed, so the rewriter has a destination href.
	ResolveCanonical(ent *Entity) (string, error)
}

// noopLinkSync is the default if the server hasn't wired up v2 yet. Lets
// the entity service ship without a hard dependency on v2 during the
// rollout window.
type noopLinkSync struct{}

func (noopLinkSync) Reindex(context.Context, uuid.UUID) error { return nil }
func (noopLinkSync) RewriteForEntity(context.Context, uuid.UUID, string, string, *uuid.UUID) error {
	return nil
}
func (noopLinkSync) ResolveCanonical(*Entity) (string, error) { return "", nil }

// Service provides business logic for entity management.
type Service struct {
	schemaRepo     SchemaRepository
	entityRepo     EntityRepository
	validator      *Validator
	logger         *slog.Logger
	linkSync       EntityLinkSync
	events         EventSink
	beforeSave     BeforeSaveFunc
	urls           *URLRegistry
	onSchemaChange func()
	bylines        BylineResolver
	pathChanged    PathChangeFunc
	patternChanged SchemaPatternFunc
	live           LiveFunc
	reuse          ReuseStatus
}

// NewService creates a new entity service.
func NewService(schemaRepo SchemaRepository, entityRepo EntityRepository, logger *slog.Logger) *Service {
	return &Service{
		schemaRepo: schemaRepo,
		entityRepo: entityRepo,
		validator:  NewValidator(),
		logger:     logger,
		linkSync:   noopLinkSync{},
		urls:       NewURLRegistry(schemaRepo, logger),
	}
}

// URLRegistry exposes the schema URL registry. It is invalidated by
// CreateSchema, UpdateSchema and DeleteSchema.
func (s *Service) URLRegistry() *URLRegistry { return s.urls }

// SetOnSchemaChange registers a callback run after a schema is created,
// updated or deleted (after the URL registry was invalidated), for example to
// purge caches derived from schema settings.
func (s *Service) SetOnSchemaChange(fn func()) { s.onSchemaChange = fn }

func (s *Service) schemaChanged() {
	s.urls.Invalidate()
	if s.onSchemaChange != nil {
		s.onSchemaChange()
	}
}

// SetLinkSync installs the v2 linking sync hook. Called by the server
// during dependency wiring (server.go) after both the entity service and
// the v2 package are constructed. Safe to call before any request flows
// through UpdateEntity.
func (s *Service) SetLinkSync(sync EntityLinkSync) {
	if sync == nil {
		s.linkSync = noopLinkSync{}
		return
	}
	s.linkSync = sync
}

// --- Schema Operations ---

// CreateSchema creates a new entity schema.
func (s *Service) CreateSchema(ctx context.Context, input *CreateSchemaInput) (*EntitySchema, error) {
	// Validate the JSON schema itself
	if err := s.validator.ValidateSchema(input.Schema); err != nil {
		return nil, err
	}
	if err := ValidateSchemaSettingsForWrite(input.Settings); err != nil {
		return nil, err
	}

	// Auto-generate slug if not provided
	if input.Slug == "" {
		input.Slug = generateSlug(input.Name)
	} else {
		input.Slug = normalizeSlug(input.Slug)
	}
	if err := ValidateEditorSetting(input.Slug, input.Schema, input.Settings); err != nil {
		return nil, err
	}

	schema, err := s.schemaRepo.Create(ctx, input)
	if err != nil {
		s.logger.Error("failed to create schema",
			slog.String("name", input.Name),
			slog.String("error", err.Error()),
		)
		return nil, err
	}
	s.schemaChanged()

	s.logger.Info("created entity schema",
		slog.String("schema_id", schema.ID.String()),
		slog.String("name", schema.Name),
		slog.String("slug", schema.Slug),
	)

	return schema, nil
}

// GetSchema retrieves an entity schema by ID.
func (s *Service) GetSchema(ctx context.Context, id uuid.UUID) (*EntitySchema, error) {
	return s.schemaRepo.GetByID(ctx, id)
}

// GetSchemaBySlug retrieves an entity schema by slug.
func (s *Service) GetSchemaBySlug(ctx context.Context, slug string) (*EntitySchema, error) {
	return s.schemaRepo.GetBySlug(ctx, slug)
}

// UpdateSchema modifies an entity schema.
func (s *Service) UpdateSchema(ctx context.Context, id uuid.UUID, input *UpdateSchemaInput) (*EntitySchema, error) {
	// Check if schema exists; a built-in one only changes as checkBuiltinUpdate allows
	existing, err := s.schemaRepo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	if existing.IsBuiltin {
		if err := checkBuiltinUpdate(existing, input); err != nil {
			return nil, err
		}
	}

	// Validate new schema if provided
	if input.Schema != nil {
		if err := s.validator.ValidateSchema(*input.Schema); err != nil {
			return nil, err
		}
	}

	if input.Settings != nil {
		if err := ValidateSchemaSettingsForWrite(*input.Settings); err != nil {
			return nil, err
		}
	}

	if input.Schema != nil || input.Settings != nil {
		schemaJSON, settings := existing.Schema, existing.Settings
		if input.Schema != nil {
			schemaJSON = *input.Schema
		}
		if input.Settings != nil {
			settings = *input.Settings
		}
		if err := ValidateEditorSetting(existing.Slug, schemaJSON, settings); err != nil {
			return nil, err
		}
	}

	schema, err := s.schemaRepo.Update(ctx, id, input)
	if err != nil {
		s.logger.Error("failed to update schema",
			slog.String("schema_id", id.String()),
			slog.String("error", err.Error()),
		)
		return nil, err
	}
	s.schemaChanged()
	if s.patternChanged != nil && urlRouting(existing.Settings) != urlRouting(schema.Settings) {
		s.patternChanged(ctx, existing, schema)
	}

	s.logger.Info("updated entity schema",
		slog.String("schema_id", schema.ID.String()),
		slog.String("name", schema.Name),
	)

	return schema, nil
}

// DeleteSchema removes an entity schema.
func (s *Service) DeleteSchema(ctx context.Context, id uuid.UUID) error {
	// Check if schema exists and is not built-in
	existing, err := s.schemaRepo.GetByID(ctx, id)
	if err != nil {
		return err
	}

	if existing.IsBuiltin {
		return ErrCannotDeleteBuiltin
	}

	// Check if schema is in use
	count, err := s.schemaRepo.CountEntities(ctx, id)
	if err != nil {
		return err
	}

	if count > 0 {
		return fmt.Errorf("%w: %d entities are using this schema", ErrSchemaInUse, count)
	}

	if err := s.schemaRepo.Delete(ctx, id); err != nil {
		s.logger.Error("failed to delete schema",
			slog.String("schema_id", id.String()),
			slog.String("error", err.Error()),
		)
		return err
	}
	s.schemaChanged()

	s.logger.Info("deleted entity schema",
		slog.String("schema_id", id.String()),
	)

	return nil
}

// ListSchemas retrieves all entity schemas.
func (s *Service) ListSchemas(ctx context.Context) ([]*EntitySchema, error) {
	return s.schemaRepo.List(ctx)
}

// --- Entity Operations ---

// CreateEntity creates a new entity.
func (s *Service) CreateEntity(ctx context.Context, userID *uuid.UUID, input *CreateEntityInput) (*Entity, error) {
	// An empty locale stays empty: the database stores the site language.
	if input.Locale != "" {
		locale, err := NormalizeLocale(input.Locale)
		if err != nil {
			return nil, err
		}
		input.Locale = locale
	}
	// Get the schema
	schema, err := s.schemaRepo.GetByID(ctx, input.SchemaID)
	if err != nil {
		return nil, err
	}

	// Validate attributes against schema
	if err := s.validator.ValidateAttributes(schema.Schema, input.Attributes); err != nil {
		return nil, err
	}

	// Server-side sanitize rich-text HTML at the write boundary (defense in
	// depth vs. the client sanitizer). Runs after schema validation, which only
	// checks structure, not HTML safety.
	if cleaned, err := sanitizeAttributes(input.Attributes); err != nil {
		return nil, err
	} else {
		input.Attributes = cleaned
	}
	if input.Attributes, err = normalizeDocRaw(docWrite, nil, input.Attributes, EligibleForPageDoc(schema)); err != nil {
		return nil, err
	}

	// Auto-generate slug if not provided
	if input.Slug == "" {
		input.Slug = generateSlug(input.Name)
	} else {
		input.Slug = normalizeSlug(input.Slug)
	}

	if s.beforeSave != nil {
		draft := &BeforeSaveInput{Operation: "create", SchemaSlug: schema.Slug, Name: input.Name, Slug: input.Slug, Locale: input.Locale, Attributes: input.Attributes}
		attrs, err := s.runBeforeSave(ctx, draft, schema)
		if err != nil {
			return nil, err
		}
		input.Name, input.Attributes = draft.Name, attrs
		if draft.Slug != "" {
			input.Slug = normalizeSlug(draft.Slug)
		}
	}

	if input.ParentID != nil {
		if err := s.checkParent(ctx, nil, *input.ParentID); err != nil {
			return nil, err
		}
	}
	if err := s.creditCreate(ctx, input); err != nil {
		return nil, err
	}
	input.Status = lockedStatus(schema, input.Attributes)

	entity, err := s.entityRepo.Create(ctx, userID, input)
	if err != nil {
		s.logger.Error("failed to create entity",
			slog.String("name", input.Name),
			slog.String("schema_id", input.SchemaID.String()),
			slog.String("error", err.Error()),
		)
		return nil, err
	}

	// Attach schema to response
	entity.Schema = schema
	s.personChanged(ctx, entity)

	s.logger.Info("created entity",
		slog.String("entity_id", entity.ID.String()),
		slog.String("name", entity.Name),
		slog.String("schema", schema.Slug),
	)

	s.fireLive(ctx, entity.ID)
	s.emit(ctx, EventCreated, entity, schema.Slug)

	return entity, nil
}

// GetEntity retrieves an entity by ID.
func (s *Service) GetEntity(ctx context.Context, id uuid.UUID) (*Entity, error) {
	return s.entityRepo.GetByID(ctx, id)
}

// GetEntityBySlug retrieves an entity by slug.
func (s *Service) GetEntityBySlug(ctx context.Context, slug string) (*Entity, error) {
	return s.entityRepo.GetBySlug(ctx, slug)
}

// GetEntityByParentSlug retrieves the row with slug directly under parent (a
// root row when parent is nil). Slugs are unique per schema, not among all
// siblings: with schemaSlug the lookup is exact; without it, rows of several
// schemas sharing the slug give ErrAmbiguousSlug rather than a guess.
func (s *Service) GetEntityByParentSlug(ctx context.Context, parent *uuid.UUID, slug, schemaSlug string) (*Entity, error) {
	hs, ok := s.entityRepo.(HierarchyStore)
	if !ok {
		return nil, errors.New("entity repository has no hierarchy support")
	}
	return hs.Child(ctx, parent, slug, schemaSlug)
}

// UpdateEntity modifies an entity.
func (s *Service) UpdateEntity(ctx context.Context, id uuid.UUID, userID *uuid.UUID, input *UpdateEntityInput) (*Entity, error) {
	// Get current entity to validate against its schema
	existing, err := s.entityRepo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	if input.Locale != nil {
		locale, err := NormalizeLocale(*input.Locale)
		if err != nil {
			return nil, err
		}
		input.Locale = &locale
	}

	// Validate new attributes if provided
	if input.Attributes != nil {
		if err := s.validator.ValidateAttributes(existing.Schema.Schema, *input.Attributes); err != nil {
			return nil, err
		}
		// Sanitize rich-text HTML before persisting (see CreateEntity).
		// Only values that differ from the stored attributes are sanitised, so
		// an unchanged save leaves legacy content byte-identical.
		// The baseline (existing) is read above, outside the write: Update has
		// no transaction/row lock to share, and a concurrent save in between can
		// only make a previously stored value count as unchanged. Accepted risk;
		// tighten with SELECT ... FOR UPDATE in the repo Update if needed.
		cleaned, err := sanitizeAttributesAgainst(*input.Attributes, existing.Attributes)
		if err != nil {
			return nil, err
		}
		input.Attributes = &cleaned
	}
	if input.Attributes != nil {
		attrs, err := normalizeDocRaw(docWrite, existing.Attributes, *input.Attributes, EligibleForPageDoc(existing.Schema))
		if err != nil {
			return nil, err
		}
		input.Attributes = &attrs
	} else if attrs, changed := convertUnconverted(existing); changed {
		// Any write of a page that still has only page_sections converts it once.
		input.Attributes = &attrs
	}

	if s.beforeSave != nil {
		draft := &BeforeSaveInput{Operation: "update", ID: &id, SchemaSlug: existing.Schema.Slug, Name: existing.Name, Slug: existing.Slug, Locale: existing.Locale, Attributes: existing.Attributes}
		if input.Name != nil {
			draft.Name = *input.Name
		}
		if input.Slug != nil {
			draft.Slug = *input.Slug
		}
		if input.Locale != nil {
			draft.Locale = *input.Locale
		}
		if input.Attributes != nil {
			draft.Attributes = *input.Attributes
		}
		origName, origSlug, origAttrs := draft.Name, draft.Slug, draft.Attributes
		attrs, err := s.runBeforeSave(ctx, draft, existing.Schema)
		if err != nil {
			return nil, err
		}
		if draft.Name != origName {
			input.Name = &draft.Name
		}
		if draft.Slug != origSlug {
			slug := normalizeSlug(draft.Slug)
			input.Slug = &slug
		}
		if !bytes.Equal(attrs, origAttrs) {
			input.Attributes = &attrs
		}
	}

	if input.ParentID != nil && !input.ClearParent {
		if err := s.checkParent(ctx, &id, *input.ParentID); err != nil {
			return nil, err
		}
	}
	if err := s.creditUpdate(ctx, existing, input); err != nil {
		return nil, err
	}

	attrs := existing.Attributes
	if input.Attributes != nil {
		attrs = *input.Attributes
	}
	if st := lockedStatus(existing.Schema, attrs); st != nil {
		input.Status = st
	}

	entity, err := s.entityRepo.Update(ctx, id, userID, input)
	if err != nil {
		s.logger.Error("failed to update entity",
			slog.String("entity_id", id.String()),
			slog.String("error", err.Error()),
		)
		return nil, err
	}

	// Attach schema to response
	entity.Schema = existing.Schema
	s.personChanged(ctx, entity)

	s.logger.Info("updated entity",
		slog.String("entity_id", entity.ID.String()),
		slog.String("name", entity.Name),
		slog.Int("version", entity.Version),
	)

	// v2 linking sync (ADR §4.3, Phase 1 §E).
	//
	// Reindex runs after every successful update so hatamoto_entity_link_index
	// always reflects what's in attributes.content. The rewriter fires only
	// when this entity's own slug changed — otherwise the
	// existing index rows pointing at it stay valid.
	//
	// Both calls use context.Background() because the HTTP handler will
	// cancel the request ctx as soon as the response writes, and these
	// goroutines outlive that. They go through the EntityLinkSync interface
	// (raw DB access inside v2) rather than recursing back through
	// s.UpdateEntity, which would re-trigger this hook ad infinitum.
	s.fireLinkSync(existing, entity, userID)
	s.firePathChange(ctx, existing, entity, userID)

	s.emitSaved(ctx, existing, entity)

	return entity, nil
}

// emitSaved announces an update. A save that took the entity live (a person
// made public) is a publish: it retires the automatic redirects at its path
// and emits entity.published, as UpdateEntityStatus does.
func (s *Service) emitSaved(ctx context.Context, before, after *Entity) {
	if before.Status != StatusPublished && after.Status == StatusPublished {
		s.fireLive(ctx, after.ID)
		s.emit(ctx, EventPublished, after, before.Schema.Slug)
		return
	}
	if after.Status == StatusPublished && before.Locale != after.Locale {
		// A locale change can take a published entity live (into the site
		// language): retire the automatic redirects at its path, as a publish does.
		s.fireLive(ctx, after.ID)
	}
	s.emit(ctx, EventUpdated, after, before.Schema.Slug)
}

// fireLinkSync dispatches the post-Update v2 hooks in background goroutines.
// Logs all errors at warn level — partial linking-sync failure must never
// fail the user-facing update.
func (s *Service) fireLinkSync(before, after *Entity, actor *uuid.UUID) {
	sync := s.linkSync
	if sync == nil {
		return
	}

	go func() {
		bgCtx := context.Background()
		if err := sync.Reindex(bgCtx, after.ID); err != nil {
			s.logger.Warn("link sync reindex failed",
				slog.String("entity_id", after.ID.String()),
				slog.String("error", err.Error()),
			)
		}
	}()

	if before.Slug == after.Slug {
		return
	}
	// With a path observer installed it rewrites the inbound links of a
	// published entity (and of everything below it) itself, in the same pass
	// that writes the redirects; a second, concurrent rewrite would only
	// double the link versions.
	if s.pathChanged != nil && after.Status == StatusPublished {
		return
	}

	go func() {
		bgCtx := context.Background()
		newURL, err := sync.ResolveCanonical(after)
		if err != nil || newURL == "" {
			s.logger.Warn("link sync skipped: cannot resolve canonical URL",
				slog.String("entity_id", after.ID.String()),
				slog.Any("error", err),
			)
			return
		}
		if err := sync.RewriteForEntity(bgCtx, after.ID, newURL, "slug_change", actor); err != nil {
			s.logger.Warn("link sync rewrite failed",
				slog.String("entity_id", after.ID.String()),
				slog.String("new_url", newURL),
				slog.String("error", err.Error()),
			)
		}
	}()
}

// UpdateEntityStatus changes an entity's status.
func (s *Service) UpdateEntityStatus(ctx context.Context, id uuid.UUID, userID *uuid.UUID, status EntityStatus) (*Entity, error) {
	if !ValidStatus(status) {
		return nil, ErrInvalidStatus
	}

	// Best-effort pre-read so a transition to published can be told apart
	// from a re-save. Copy the status: repositories may mutate in place.
	// The schema slug is read here too: UpdateStatus's RETURNING row carries
	// no joined schema, and subscribers key on the slug.
	var prevStatus EntityStatus
	var schemaSlug string
	var reuse bool
	prev, prevErr := s.entityRepo.GetByID(ctx, id)
	if prevErr == nil {
		prevStatus = prev.Status
		if prev.Schema != nil {
			schemaSlug = prev.Schema.Slug
			reuse = IsReuseSchema(prev.Schema)
		}
	}
	if schemaSlug == personSchemaSlug {
		return nil, errPersonStatus
	}
	if reuse {
		return nil, errReuseStatus
	}

	entity, err := s.entityRepo.UpdateStatus(ctx, id, userID, status)
	if err != nil {
		s.logger.Error("failed to update entity status",
			slog.String("entity_id", id.String()),
			slog.String("status", string(status)),
			slog.String("error", err.Error()),
		)
		return nil, err
	}

	s.logger.Info("updated entity status",
		slog.String("entity_id", entity.ID.String()),
		slog.String("status", string(status)),
	)

	if status == StatusPublished {
		s.fireLive(ctx, entity.ID)
	}
	if status == StatusPublished && prevStatus != StatusPublished {
		s.emit(ctx, EventPublished, entity, schemaSlug)
	} else {
		s.emit(ctx, EventUpdated, entity, schemaSlug)
	}

	return entity, nil
}

// ErrScheduleInPast is returned when a scheduled time is not in the future.
var ErrScheduleInPast = errors.New("scheduled time must be in the future")

// scheduler is implemented by repositories that can schedule an entity.
type scheduler interface {
	Schedule(ctx context.Context, id uuid.UUID, userID *uuid.UUID, at time.Time) (*Entity, error)
}

// ScheduleEntity sets the entity to publish automatically at the given time.
func (s *Service) ScheduleEntity(ctx context.Context, id uuid.UUID, userID *uuid.UUID, at time.Time) (*Entity, error) {
	sc, ok := s.entityRepo.(scheduler)
	if !ok {
		return nil, errors.New("scheduling is not supported by this store")
	}
	if !at.After(time.Now()) {
		return nil, ErrScheduleInPast
	}
	if prev, err := s.entityRepo.GetByID(ctx, id); err == nil && prev.Schema != nil {
		if prev.Schema.Slug == personSchemaSlug {
			return nil, errPersonStatus
		}
		if IsReuseSchema(prev.Schema) {
			return nil, errReuseStatus
		}
	}
	e, err := sc.Schedule(ctx, id, userID, at)
	if err != nil {
		return nil, err
	}
	var schemaSlug string
	if prev, err := s.entityRepo.GetByID(ctx, id); err == nil && prev.Schema != nil {
		schemaSlug = prev.Schema.Slug
	}
	s.emit(ctx, EventUpdated, e, schemaSlug)
	return e, nil
}

// DeleteEntity removes an entity.
func (s *Service) DeleteEntity(ctx context.Context, id uuid.UUID) error {
	// Deleting a parent would re-root its children: their URLs change without a
	// redirect, and siblings of one slug under other parents could collide.
	if hs, ok := s.entityRepo.(HierarchyStore); ok {
		has, err := hs.HasChildren(ctx, id)
		if err != nil {
			return err
		}
		if has {
			return ErrHasChildren
		}
	}
	// Best-effort pre-read: the row is gone after Delete and subscribers
	// want its name and schema.
	var deleted *Entity
	if s.events != nil {
		if prev, err := s.entityRepo.GetByID(ctx, id); err == nil {
			cp := *prev
			deleted = &cp
		}
	}

	if s.bylines != nil {
		if prev, err := s.entityRepo.GetByID(ctx, id); err == nil && prev.Schema != nil && prev.Schema.Slug == personSchemaSlug {
			if err := s.bylines.PersonDeleting(ctx, id); err != nil {
				return fmt.Errorf("failed to detach the person from their posts: %w", err)
			}
		}
	}

	if err := s.entityRepo.Delete(ctx, id); err != nil {
		s.logger.Error("failed to delete entity",
			slog.String("entity_id", id.String()),
			slog.String("error", err.Error()),
		)
		return err
	}

	s.logger.Info("deleted entity",
		slog.String("entity_id", id.String()),
	)

	if deleted == nil {
		deleted = &Entity{ID: id}
	}
	s.emit(ctx, EventDeleted, deleted, "")

	return nil
}

// ListEntities retrieves entities with filtering and keyset pagination.
func (s *Service) ListEntities(ctx context.Context, filter *ListEntitiesFilter, page *KeysetPage) (*PagedResult[*Entity], error) {
	return s.entityRepo.List(ctx, filter, page)
}

// ListEntitiesWithOffset retrieves entities with filtering and offset pagination.
func (s *Service) ListEntitiesWithOffset(ctx context.Context, filter *ListEntitiesFilter, page *OffsetPage) (*OffsetPagedResult[*Entity], error) {
	return s.entityRepo.ListWithOffset(ctx, filter, page)
}

// CountEntitiesBySchemaStatus counts entities per schema and status in one query.
func (s *Service) CountEntitiesBySchemaStatus(ctx context.Context) ([]SchemaStatusCount, error) {
	return s.entityRepo.CountBySchemaStatus(ctx)
}

// CountEntitiesByParent returns the limit parents with the most children, with their counts.
func (s *Service) CountEntitiesByParent(ctx context.Context, limit int) ([]ParentCount, error) {
	return s.entityRepo.CountByParent(ctx, limit)
}

// CountEntityChildren returns the direct child count of each of parentIDs (absent = none).
func (s *Service) CountEntityChildren(ctx context.Context, parentIDs []uuid.UUID) (map[uuid.UUID]int, error) {
	return s.entityRepo.CountChildren(ctx, parentIDs)
}

// --- Version Operations ---

// ListVersions retrieves version history for an entity.
func (s *Service) ListVersions(ctx context.Context, entityID uuid.UUID, limit int) ([]*EntityVersion, error) {
	// Verify entity exists
	if _, err := s.entityRepo.GetByID(ctx, entityID); err != nil {
		return nil, err
	}

	return s.entityRepo.ListVersions(ctx, entityID, limit)
}

// GetVersion retrieves a specific version of an entity.
func (s *Service) GetVersion(ctx context.Context, entityID uuid.UUID, version int) (*EntityVersion, error) {
	return s.entityRepo.GetVersion(ctx, entityID, version)
}

// RestoreVersion restores an entity to a previous version.
func (s *Service) RestoreVersion(ctx context.Context, entityID uuid.UUID, userID *uuid.UUID, version int) (*Entity, error) {
	// Get the version to restore
	ver, err := s.entityRepo.GetVersion(ctx, entityID, version)
	if err != nil {
		return nil, err
	}

	// Get current entity for validation
	existing, err := s.entityRepo.GetByID(ctx, entityID)
	if err != nil {
		return nil, err
	}

	// Validate the old attributes still conform to the current schema
	if err := s.validator.ValidateAttributes(existing.Schema.Schema, ver.Attributes); err != nil {
		return nil, fmt.Errorf("cannot restore version %d: %w: %w", version, ErrRestoreInvalid, err)
	}

	restoredAttrs, err := normalizeDocRaw(docRestore, existing.Attributes, ver.Attributes, EligibleForPageDoc(existing.Schema))
	if err != nil {
		return nil, err
	}
	if s.beforeSave != nil {
		draft := &BeforeSaveInput{Operation: "restore", ID: &entityID, SchemaSlug: existing.Schema.Slug, Name: existing.Name, Slug: existing.Slug, Locale: existing.Locale, Attributes: restoredAttrs}
		attrs, err := s.runBeforeSave(ctx, draft, existing.Schema)
		if err != nil {
			return nil, err
		}
		restoredAttrs = attrs
	}

	changeSummary := fmt.Sprintf("Restored from version %d", version)

	// Update with the old attributes
	entity, err := s.entityRepo.Update(ctx, entityID, userID, &UpdateEntityInput{
		Attributes:    &restoredAttrs,
		ChangeSummary: &changeSummary,
		Status:        lockedStatus(existing.Schema, restoredAttrs),
	})
	if err != nil {
		s.logger.Error("failed to restore entity version",
			slog.String("entity_id", entityID.String()),
			slog.Int("version", version),
			slog.String("error", err.Error()),
		)
		return nil, err
	}

	entity.Schema = existing.Schema

	s.logger.Info("restored entity version",
		slog.String("entity_id", entityID.String()),
		slog.Int("restored_from", version),
		slog.Int("new_version", entity.Version),
	)

	s.emitSaved(ctx, existing, entity)

	return entity, nil
}

// ComputeDiff computes the differences between two versions.
func (s *Service) ComputeDiff(ctx context.Context, entityID uuid.UUID, fromVersion, toVersion int) ([]VersionDiff, error) {
	from, err := s.entityRepo.GetVersion(ctx, entityID, fromVersion)
	if err != nil {
		return nil, err
	}

	to, err := s.entityRepo.GetVersion(ctx, entityID, toVersion)
	if err != nil {
		return nil, err
	}

	return computeJSONDiff(from.Attributes, to.Attributes)
}

// computeJSONDiff computes differences between two JSON objects.
func computeJSONDiff(oldJSON, newJSON json.RawMessage) ([]VersionDiff, error) {
	var oldMap, newMap map[string]any

	if err := json.Unmarshal(oldJSON, &oldMap); err != nil {
		return nil, fmt.Errorf("failed to parse old JSON: %w", err)
	}

	if err := json.Unmarshal(newJSON, &newMap); err != nil {
		return nil, fmt.Errorf("failed to parse new JSON: %w", err)
	}

	var diffs []VersionDiff

	// Check for changed/removed fields
	for key, oldVal := range oldMap {
		if newVal, exists := newMap[key]; exists {
			if !jsonEqual(oldVal, newVal) {
				diffs = append(diffs, VersionDiff{
					Field:    key,
					OldValue: oldVal,
					NewValue: newVal,
				})
			}
		} else {
			diffs = append(diffs, VersionDiff{
				Field:    key,
				OldValue: oldVal,
				NewValue: nil,
			})
		}
	}

	// Check for added fields
	for key, newVal := range newMap {
		if _, exists := oldMap[key]; !exists {
			diffs = append(diffs, VersionDiff{
				Field:    key,
				OldValue: nil,
				NewValue: newVal,
			})
		}
	}

	return diffs, nil
}

// jsonEqual checks if two JSON values are equal.
func jsonEqual(a, b any) bool {
	aJSON, err := json.Marshal(a)
	if err != nil {
		return false
	}

	bJSON, err := json.Marshal(b)
	if err != nil {
		return false
	}

	return string(aJSON) == string(bJSON)
}

// ValidateEntityAttributes validates attributes against a schema without creating an entity.
func (s *Service) ValidateEntityAttributes(ctx context.Context, schemaID uuid.UUID, attributes json.RawMessage) *ValidationResult {
	schema, err := s.schemaRepo.GetByID(ctx, schemaID)
	if err != nil {
		return &ValidationResult{
			Valid: false,
			Errors: []ValidationError{{
				Path:    "",
				Message: fmt.Sprintf("schema not found: %v", err),
			}},
		}
	}

	return s.validator.ValidateWithDetails(schema.Schema, attributes)
}

// --- Slug Generation ---

var (
	slugRegex    = regexp.MustCompile(`[^a-z0-9-]+`)
	multiDash    = regexp.MustCompile(`-+`)
	slugTrimDash = regexp.MustCompile(`^-+|-+$`)
)

// generateSlug creates a URL-friendly slug from a name.
func generateSlug(name string) string {
	// Normalize unicode characters
	slug := norm.NFKD.String(name)

	// Convert to lowercase
	slug = strings.ToLower(slug)

	// Remove accents/diacritics
	slug = removeAccents(slug)

	// Replace non-alphanumeric characters with dashes
	slug = slugRegex.ReplaceAllString(slug, "-")

	// Collapse multiple dashes
	slug = multiDash.ReplaceAllString(slug, "-")

	// Trim leading/trailing dashes
	slug = slugTrimDash.ReplaceAllString(slug, "")

	// Truncate to reasonable length
	if len(slug) > 100 {
		slug = slug[:100]
		// Ensure we don't cut in the middle of a word
		if lastDash := strings.LastIndex(slug, "-"); lastDash > 50 {
			slug = slug[:lastDash]
		}
	}

	return slug
}

// normalizeSlug cleans up a user-provided slug.
func normalizeSlug(slug string) string {
	slug = strings.ToLower(strings.TrimSpace(slug))
	slug = slugRegex.ReplaceAllString(slug, "-")
	slug = multiDash.ReplaceAllString(slug, "-")
	slug = slugTrimDash.ReplaceAllString(slug, "")
	return slug
}

// removeAccents removes diacritics from a string.
func removeAccents(s string) string {
	var result strings.Builder
	for _, r := range s {
		if unicode.Is(unicode.Mn, r) {
			continue // Skip combining marks
		}
		result.WriteRune(r)
	}
	return result.String()
}

// personChanged keeps the author mirror of a person entity in step after it
// was created or saved. The save itself succeeded, so a failure is logged.
func (s *Service) personChanged(ctx context.Context, e *Entity) {
	if s.bylines == nil || e.Schema == nil || e.Schema.Slug != personSchemaSlug {
		return
	}
	if err := s.bylines.PersonChanged(ctx, e.ID); err != nil {
		s.logger.Error("failed to sync the author mirror of a person",
			slog.String("entity_id", e.ID.String()), slog.String("error", err.Error()))
	}
}
