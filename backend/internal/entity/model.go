// Package entity provides entity domain models and repository interfaces for the Entity Management system.
package entity

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// EntityStatus represents the status of an entity.
type EntityStatus string

// Available entity statuses.
const (
	StatusDraft EntityStatus = "draft"
	// StatusScheduled holds an entity that is finished but not yet due.
	// The public repository filters on status = 'published', so a scheduled
	// row is invisible to readers for free — no date guard is needed on the
	// eight public query sites. worker.PublishWorker flips it to published
	// once published_at falls due, preserving published_at rather than
	// stamping now() the way UpdateStatus does.
	StatusScheduled EntityStatus = "scheduled"
	StatusPublished EntityStatus = "published"
	StatusArchived  EntityStatus = "archived"
)

// Common errors.
var (
	ErrSchemaNotFound    = errors.New("entity schema not found")
	ErrEntityNotFound    = errors.New("entity not found")
	ErrVersionConflict   = errors.New("entity was saved by someone else")
	ErrSlugAlreadyExists = errors.New("slug already exists")
	ErrInvalidStatus     = errors.New("invalid entity status")
	ErrInvalidSchema     = errors.New("invalid JSON schema")
	ErrValidationFailed  = errors.New("attribute validation failed")
	ErrVersionNotFound   = errors.New("entity version not found")
	// ErrRestoreInvalid marks a version whose attributes no longer fit the
	// entity type's current schema.
	ErrRestoreInvalid      = errors.New("version does not fit the current schema")
	ErrCannotDeleteBuiltin = errors.New("cannot delete built-in schema")
	ErrCannotModifyBuiltin = errors.New("cannot modify built-in schema")
	ErrSchemaInUse         = errors.New("schema is in use by entities")
	ErrParentNotFound      = errors.New("parent entity not found")
	ErrParentCycle         = errors.New("parent would make the entity its own ancestor")
	ErrParentTooDeep       = errors.New("entity nesting is too deep")
	ErrAmbiguousSlug       = errors.New("several entities of different schemas share this slug: say which schema")
	ErrHasChildren         = errors.New("entity has child entities: move or delete them first")
	ErrAuthorNotFound      = errors.New("author not found")
	// ErrInvalidDoc marks an attributes.page_doc that fails validation. It does
	// not wrap ErrValidationFailed: the API maps it to 422, not 400.
	ErrInvalidDoc = errors.New("invalid page_doc")
	// ErrPageSectionsRemoved rejects a write that changes the layout through
	// page_sections on a page that has a page_doc (maps to 422).
	ErrPageSectionsRemoved = errors.New("page_sections can no longer change a page that has a page_doc; send attributes.page_doc (docs/BUILDER.md)")
)

// EntitySchema represents a JSON Schema definition for entities.
type EntitySchema struct {
	ID          uuid.UUID       `json:"id"`
	Name        string          `json:"name"`
	Slug        string          `json:"slug"`
	Description *string         `json:"description,omitempty"`
	Schema      json.RawMessage `json:"schema"`
	IsBuiltin   bool            `json:"is_builtin"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
	// Settings holds optional per-schema routing configuration (see
	// SchemaURLSettings). An empty object means "use the built-in defaults".
	Settings json.RawMessage `json:"settings"`
}

// Entity represents a data entity following a schema.
type Entity struct {
	ID          uuid.UUID       `json:"id"`
	SchemaID    uuid.UUID       `json:"schema_id"`
	Name        string          `json:"name"`
	Slug        string          `json:"slug"`
	Locale      string          `json:"locale"`
	Status      EntityStatus    `json:"status"`
	Attributes  json.RawMessage `json:"attributes"`
	Metadata    json.RawMessage `json:"metadata,omitempty"`
	Editorial   json.RawMessage `json:"editorial,omitempty"`
	Version     int             `json:"version"`
	CreatedBy   *uuid.UUID      `json:"created_by,omitempty"`
	UpdatedBy   *uuid.UUID      `json:"updated_by,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
	PublishedAt *time.Time      `json:"published_at,omitempty"`

	// Hierarchy and content classification fields
	ParentID *uuid.UUID `json:"parent_id,omitempty"`
	// AuthorID references hatamoto_authors (a byline, not a user account).
	AuthorID *uuid.UUID `json:"author_id,omitempty"`
	// BylinePersonID is the person entity credited as the author; BylineOrg
	// credits the site organisation instead. They are exclusive. This is the
	// source of bylines; AuthorID is only dual-written so a rollback still
	// renders them. BylinePersonID has no foreign key: integrity is kept in code.
	BylinePersonID *uuid.UUID `json:"byline_person_id,omitempty"`
	BylineOrg      bool       `json:"byline_org,omitempty"`

	// Joined fields (not stored in DB)
	Schema *EntitySchema `json:"schema,omitempty"`
}

// AutosaveSummary is the change summary of a background save; versions with it
// are pruned before any explicit version.
const AutosaveSummary = "Autosaved in the blog editor"

// AutosaveWindow is how long one person's autosaves share a version.
const AutosaveWindow = 5 * time.Minute

// EntityVersion represents a historical version of an entity's attributes.
type EntityVersion struct {
	ID            uuid.UUID       `json:"id"`
	EntityID      uuid.UUID       `json:"entity_id"`
	Version       int             `json:"version"`
	Attributes    json.RawMessage `json:"attributes"`
	ChangedBy     *uuid.UUID      `json:"changed_by,omitempty"`
	ChangeSummary *string         `json:"change_summary,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
}

// CreateSchemaInput represents input for creating a new entity schema.
type CreateSchemaInput struct {
	Name        string          `json:"name"`
	Slug        string          `json:"slug,omitempty"` // Auto-generated if empty
	Description *string         `json:"description,omitempty"`
	Schema      json.RawMessage `json:"schema"`
	Settings    json.RawMessage `json:"settings,omitempty"`
}

// UpdateSchemaInput represents input for updating an entity schema.
type UpdateSchemaInput struct {
	Name        *string          `json:"name,omitempty"`
	Description *string          `json:"description,omitempty"`
	Schema      *json.RawMessage `json:"schema,omitempty"`
	Settings    *json.RawMessage `json:"settings,omitempty"`
}

// CreateEntityInput represents input for creating a new entity.
type CreateEntityInput struct {
	SchemaID   uuid.UUID       `json:"schema_id"`
	Name       string          `json:"name"`
	Slug       string          `json:"slug,omitempty"`   // Auto-generated if empty
	Locale     string          `json:"locale,omitempty"` // BCP 47; empty stores the site language
	Attributes json.RawMessage `json:"attributes"`
	Metadata   json.RawMessage `json:"metadata,omitempty"`
	ParentID   *uuid.UUID      `json:"parent_id,omitempty"`
	AuthorID   *uuid.UUID      `json:"author_id,omitempty"`
	// BylinePersonID and BylineOrg set the byline (see Entity).
	BylinePersonID *uuid.UUID `json:"byline_person_id,omitempty"`
	BylineOrg      bool       `json:"byline_org,omitempty"`
	// Status is set by the service for a person or a block or part (see lockedStatus); a caller
	// cannot choose it. Unset stores a draft.
	Status *EntityStatus `json:"-"`
}

// UpdateEntityInput represents input for updating an entity.
type UpdateEntityInput struct {
	Name          *string          `json:"name,omitempty"`
	Slug          *string          `json:"slug,omitempty"`
	Locale        *string          `json:"locale,omitempty"` // re-tags the entity; nil leaves it as stored
	Attributes    *json.RawMessage `json:"attributes,omitempty"`
	Metadata      *json.RawMessage `json:"metadata,omitempty"`
	ChangeSummary *string          `json:"change_summary,omitempty"`
	// Autosave marks a background save. It is recorded with AutosaveSummary,
	// and a run of autosaves by one person within AutosaveWindow keeps a single
	// version (the state before the run), so history holds checkpoints, not
	// every pause in typing. Explicit saves always create a version.
	Autosave bool `json:"-"`
	// ExpectedVersion, when set, makes the write conditional: the repository
	// compares it with the stored version under its row lock and returns a
	// *VersionConflictError instead of writing when they differ.
	ExpectedVersion *int `json:"-"`
	// KeepUpdatedAt leaves updated_at as it is (the version still advances).
	// Internal: used by `builder convert`, so a lossless shape
	// change does not look like a content edit (JSON-LD dateModified, sitemap lastmod).
	KeepUpdatedAt bool `json:"-"`
	// ParentID moves the entity under a parent; ClearParent makes it a root.
	// Both unset leaves the parent alone. The same holds for AuthorID.
	ParentID    *uuid.UUID `json:"parent_id,omitempty"`
	ClearParent bool       `json:"clear_parent,omitempty"`
	AuthorID    *uuid.UUID `json:"author_id,omitempty"`
	ClearAuthor bool       `json:"clear_author,omitempty"`
	// BylinePersonID sets the credited person and ClearBylinePerson removes
	// it; BylineOrg sets or clears the site-organisation byline. Both unset
	// leaves the byline alone. The columns are independent: a caller that
	// switches from a person to the organisation sets both.
	BylinePersonID    *uuid.UUID `json:"byline_person_id,omitempty"`
	ClearBylinePerson bool       `json:"clear_byline_person,omitempty"`
	BylineOrg         *bool      `json:"byline_org,omitempty"`
	// Status is set by the service for a person or a block/part (see lockedStatus); unset
	// leaves the status alone.
	Status *EntityStatus `json:"-"`
}

// UpdateStatusInput represents input for changing an entity's status.
type UpdateStatusInput struct {
	Status EntityStatus `json:"status"`
}

// ListEntitiesFilter represents filter options for listing entities.
type ListEntitiesFilter struct {
	SchemaID *uuid.UUID `json:"schema_id,omitempty"`
	// ExcludeSchemaIDs drops the entities of these types.
	ExcludeSchemaIDs []uuid.UUID   `json:"exclude_schema_ids,omitempty"`
	Status           *EntityStatus `json:"status,omitempty"`
	Search           string        `json:"search,omitempty"`
	// AuthorID keeps entities with this byline.
	AuthorID *uuid.UUID `json:"author_id,omitempty"`
	// PersonID keeps entities credited to this person.
	PersonID *uuid.UUID `json:"person_id,omitempty"`
	// TermID keeps entities tagged with this term or one of its descendants.
	TermID *uuid.UUID `json:"term_id,omitempty"`
	// Under keeps the descendants (children, grandchildren, ...) of this entity.
	Under *uuid.UUID `json:"under,omitempty"`
	// UpdatedFrom (inclusive) and UpdatedBefore (exclusive) bound updated_at.
	UpdatedFrom   *time.Time `json:"updated_from,omitempty"`
	UpdatedBefore *time.Time `json:"updated_before,omitempty"`
	// Sort is name | status | updated | created (default updated, newest
	// first); Asc flips the direction. Only the offset listing honours it.
	Sort string `json:"sort,omitempty"`
	Asc  bool   `json:"asc,omitempty"`
}

// KeysetPage represents keyset pagination parameters.
type KeysetPage struct {
	Cursor    *string `json:"cursor,omitempty"` // Base64-encoded cursor
	Limit     int     `json:"limit"`
	Direction string  `json:"direction"` // "next" or "prev"
}

// PagedResult represents a paginated result set.
type PagedResult[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"next_cursor,omitempty"`
	PrevCursor *string `json:"prev_cursor,omitempty"`
	HasMore    bool    `json:"has_more"`
	Total      *int    `json:"total,omitempty"` // Only populated when requested
}

// ValidStatus checks if a status is valid.
func ValidStatus(s EntityStatus) bool {
	switch s {
	case StatusDraft, StatusScheduled, StatusPublished, StatusArchived:
		return true
	default:
		return false
	}
}

// ParseStatus parses a string into an EntityStatus.
func ParseStatus(s string) (EntityStatus, error) {
	status := EntityStatus(s)
	if !ValidStatus(status) {
		return "", ErrInvalidStatus
	}
	return status, nil
}

// VersionDiff represents a difference between two entity versions.
type VersionDiff struct {
	Field    string `json:"field"`
	OldValue any    `json:"old_value"`
	NewValue any    `json:"new_value"`
}

// OffsetPage represents offset pagination parameters.
type OffsetPage struct {
	Page  int `json:"page"`
	Limit int `json:"limit"`
}

// OffsetPagedResult represents a paginated result set using offset pagination.
type OffsetPagedResult[T any] struct {
	Items      []T `json:"items"`
	Total      int `json:"total"`
	Page       int `json:"page"`
	Limit      int `json:"limit"`
	TotalPages int `json:"total_pages"`
}

// VersionConflictError is a conditional write (UpdateEntityInput.ExpectedVersion)
// that found another version stored. It matches ErrVersionConflict.
type VersionConflictError struct{ Current int }

func (e *VersionConflictError) Error() string {
	return fmt.Sprintf("%s: current version is %d", ErrVersionConflict, e.Current)
}

func (e *VersionConflictError) Is(target error) bool { return target == ErrVersionConflict }
