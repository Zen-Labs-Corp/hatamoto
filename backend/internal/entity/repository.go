package entity

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"hatamoto.dev/core/internal/database"
)

// SchemaRepository defines the interface for entity schema data operations.
type SchemaRepository interface {
	Create(ctx context.Context, input *CreateSchemaInput) (*EntitySchema, error)
	GetByID(ctx context.Context, id uuid.UUID) (*EntitySchema, error)
	GetBySlug(ctx context.Context, slug string) (*EntitySchema, error)
	Update(ctx context.Context, id uuid.UUID, input *UpdateSchemaInput) (*EntitySchema, error)
	Delete(ctx context.Context, id uuid.UUID) error
	List(ctx context.Context) ([]*EntitySchema, error)
	CountEntities(ctx context.Context, schemaID uuid.UUID) (int, error)

	// InTx runs fn in one transaction: it commits when fn returns nil and
	// rolls back otherwise.
	InTx(ctx context.Context, fn func(tx pgx.Tx) error) error
	// GetByIDForUpdate reads a schema and locks its row until tx ends, so a
	// read-modify-write of its settings cannot interleave with another one.
	GetByIDForUpdate(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*EntitySchema, error)
	// UpdateTx is Update inside tx.
	UpdateTx(ctx context.Context, tx pgx.Tx, id uuid.UUID, input *UpdateSchemaInput) (*EntitySchema, error)
}

// EntityRepository defines the interface for entity data operations.
type EntityRepository interface {
	Create(ctx context.Context, userID *uuid.UUID, input *CreateEntityInput) (*Entity, error)
	GetByID(ctx context.Context, id uuid.UUID) (*Entity, error)
	GetBySlug(ctx context.Context, slug string) (*Entity, error)
	Update(ctx context.Context, id uuid.UUID, userID *uuid.UUID, input *UpdateEntityInput) (*Entity, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, userID *uuid.UUID, status EntityStatus) (*Entity, error)
	Delete(ctx context.Context, id uuid.UUID) error
	List(ctx context.Context, filter *ListEntitiesFilter, page *KeysetPage) (*PagedResult[*Entity], error)
	ListWithOffset(ctx context.Context, filter *ListEntitiesFilter, page *OffsetPage) (*OffsetPagedResult[*Entity], error)
	CountBySchemaStatus(ctx context.Context) ([]SchemaStatusCount, error)
	CountByParent(ctx context.Context, limit int) ([]ParentCount, error)
	CountChildren(ctx context.Context, parentIDs []uuid.UUID) (map[uuid.UUID]int, error)

	// Version operations
	CreateVersion(ctx context.Context, entityID uuid.UUID, version int, attributes json.RawMessage, changedBy *uuid.UUID, changeSummary *string) (*EntityVersion, error)
	PruneVersions(ctx context.Context, keep int) (int64, error)
	GetVersion(ctx context.Context, entityID uuid.UUID, version int) (*EntityVersion, error)
	ListVersions(ctx context.Context, entityID uuid.UUID, limit int) ([]*EntityVersion, error)
}

// PostgresSchemaRepository implements SchemaRepository using PostgreSQL.
type PostgresSchemaRepository struct {
	db *database.DB
}

// NewPostgresSchemaRepository creates a new PostgreSQL schema repository.
func NewPostgresSchemaRepository(db *database.DB) *PostgresSchemaRepository {
	return &PostgresSchemaRepository{db: db}
}

// Create inserts a new entity schema.
func (r *PostgresSchemaRepository) Create(ctx context.Context, input *CreateSchemaInput) (*EntitySchema, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	id := uuid.New()
	now := time.Now()

	query := `
		INSERT INTO hatamoto_entity_schemas (
			id, name, slug, description, schema, is_builtin, created_at, updated_at, settings
		) VALUES ($1, $2, $3, $4, $5, false, $6, $7, $8)
		RETURNING id, name, slug, description, schema, is_builtin, created_at, updated_at, settings
	`

	settings := input.Settings
	if len(settings) == 0 {
		settings = json.RawMessage(`{}`)
	}

	var s EntitySchema
	err = conn.QueryRow(ctx, query,
		id, input.Name, input.Slug, input.Description, input.Schema, now, now, settings,
	).Scan(&s.ID, &s.Name, &s.Slug, &s.Description, &s.Schema, &s.IsBuiltin, &s.CreatedAt, &s.UpdatedAt, &s.Settings)

	if err != nil {
		if isPgUniqueViolation(err) {
			return nil, ErrSlugAlreadyExists
		}
		return nil, fmt.Errorf("failed to create entity schema: %w", err)
	}

	return &s, nil
}

// schemaQuerier is the QueryRow slice shared by a pooled connection and a tx.
type schemaQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

const schemaColumns = `id, name, slug, description, schema, is_builtin, created_at, updated_at, settings`

func getSchemaByID(ctx context.Context, q schemaQuerier, id uuid.UUID, suffix string) (*EntitySchema, error) {
	var s EntitySchema
	err := q.QueryRow(ctx, `SELECT `+schemaColumns+` FROM hatamoto_entity_schemas WHERE id = $1`+suffix, id).Scan(
		&s.ID, &s.Name, &s.Slug, &s.Description, &s.Schema, &s.IsBuiltin, &s.CreatedAt, &s.UpdatedAt, &s.Settings,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrSchemaNotFound
		}
		return nil, fmt.Errorf("failed to get entity schema: %w", err)
	}
	return &s, nil
}

// GetByID retrieves an entity schema by ID.
func (r *PostgresSchemaRepository) GetByID(ctx context.Context, id uuid.UUID) (*EntitySchema, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()
	return getSchemaByID(ctx, conn, id, "")
}

// InTx runs fn in one transaction.
func (r *PostgresSchemaRepository) InTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	return r.db.WithTx(ctx, fn)
}

// GetByIDForUpdate reads a schema and locks its row until tx ends.
func (r *PostgresSchemaRepository) GetByIDForUpdate(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*EntitySchema, error) {
	return getSchemaByID(ctx, tx, id, " FOR NO KEY UPDATE")
}

// GetBySlug retrieves an entity schema by slug.
func (r *PostgresSchemaRepository) GetBySlug(ctx context.Context, slug string) (*EntitySchema, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	query := `
		SELECT id, name, slug, description, schema, is_builtin, created_at, updated_at, settings
		FROM hatamoto_entity_schemas
		WHERE slug = $1
	`

	var s EntitySchema
	err = conn.QueryRow(ctx, query, slug).Scan(
		&s.ID, &s.Name, &s.Slug, &s.Description, &s.Schema, &s.IsBuiltin, &s.CreatedAt, &s.UpdatedAt, &s.Settings,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrSchemaNotFound
		}
		return nil, fmt.Errorf("failed to get entity schema by slug: %w", err)
	}

	return &s, nil
}

// Update modifies an entity schema.
func (r *PostgresSchemaRepository) Update(ctx context.Context, id uuid.UUID, input *UpdateSchemaInput) (*EntitySchema, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()
	return r.update(ctx, conn, id, input)
}

// UpdateTx is Update inside tx.
func (r *PostgresSchemaRepository) UpdateTx(ctx context.Context, tx pgx.Tx, id uuid.UUID, input *UpdateSchemaInput) (*EntitySchema, error) {
	return r.update(ctx, tx, id, input)
}

func (r *PostgresSchemaRepository) update(ctx context.Context, q schemaQuerier, id uuid.UUID, input *UpdateSchemaInput) (*EntitySchema, error) {
	query := `
		UPDATE hatamoto_entity_schemas
		SET name = COALESCE($2, name),
		    description = COALESCE($3, description),
		    schema = COALESCE($4, schema),
		    settings = COALESCE($5, settings),
		    updated_at = NOW()
		WHERE id = $1
		RETURNING ` + schemaColumns

	var s EntitySchema
	err := q.QueryRow(ctx, query, id, input.Name, input.Description, input.Schema, input.Settings).Scan(
		&s.ID, &s.Name, &s.Slug, &s.Description, &s.Schema, &s.IsBuiltin, &s.CreatedAt, &s.UpdatedAt, &s.Settings,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrSchemaNotFound
		}
		if isPgUniqueViolation(err) {
			return nil, r.patternConflict(ctx, id)
		}
		return nil, fmt.Errorf("failed to update entity schema: %w", err)
	}

	return &s, nil
}

// patternConflict explains a settings change the slug indexes refused: the new
// URL patterns make slugs unique across the schema, and some already repeat
// (under different parents).
func (r *PostgresSchemaRepository) patternConflict(ctx context.Context, id uuid.UUID) error {
	msg := "the new URL pattern needs slugs that are unique across the whole schema, but some are used more than once"
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return fmt.Errorf("%w: settings.url_pattern: %s", ErrInvalidSchema, msg)
	}
	defer conn.Release()
	rows, err := conn.Query(ctx, `
		SELECT slug FROM hatamoto_entities WHERE schema_id = $1 AND variant_key IS NULL
		GROUP BY slug, locale HAVING count(*) > 1 ORDER BY slug LIMIT 5`, id)
	if err != nil {
		return fmt.Errorf("%w: settings.url_pattern: %s", ErrInvalidSchema, msg)
	}
	defer rows.Close()
	var slugs []string
	for rows.Next() {
		var sl string
		if rows.Scan(&sl) == nil {
			slugs = append(slugs, sl)
		}
	}
	if len(slugs) > 0 {
		msg += " (" + strings.Join(slugs, ", ") + "); rename them or keep a {path} or {parent} pattern"
	}
	return fmt.Errorf("%w: settings.url_pattern: %s", ErrInvalidSchema, msg)
}

// Delete removes an entity schema.
func (r *PostgresSchemaRepository) Delete(ctx context.Context, id uuid.UUID) error {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	result, err := conn.Exec(ctx, `DELETE FROM hatamoto_entity_schemas WHERE id = $1`, id)
	if err != nil {
		// Check for foreign key constraint (schema is in use)
		if isPgForeignKeyViolation(err) {
			return ErrSchemaInUse
		}
		return fmt.Errorf("failed to delete entity schema: %w", err)
	}

	if result.RowsAffected() == 0 {
		return ErrSchemaNotFound
	}

	return nil
}

// List retrieves all entity schemas for the tenant.
func (r *PostgresSchemaRepository) List(ctx context.Context) ([]*EntitySchema, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	query := `
		SELECT id, name, slug, description, schema, is_builtin, created_at, updated_at, settings
		FROM hatamoto_entity_schemas
		ORDER BY name ASC
	`

	rows, err := conn.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to list entity schemas: %w", err)
	}
	defer rows.Close()

	var schemas []*EntitySchema
	for rows.Next() {
		var s EntitySchema
		if err := rows.Scan(
			&s.ID, &s.Name, &s.Slug, &s.Description, &s.Schema, &s.IsBuiltin, &s.CreatedAt, &s.UpdatedAt, &s.Settings,
		); err != nil {
			return nil, fmt.Errorf("failed to scan entity schema: %w", err)
		}
		schemas = append(schemas, &s)
	}

	return schemas, rows.Err()
}

// CountEntities returns the number of entities using a schema.
func (r *PostgresSchemaRepository) CountEntities(ctx context.Context, schemaID uuid.UUID) (int, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return 0, err
	}
	defer conn.Release()

	var count int
	err = conn.QueryRow(ctx, `SELECT COUNT(*) FROM hatamoto_entities WHERE schema_id = $1`, schemaID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count entities: %w", err)
	}

	return count, nil
}

// PostgresEntityRepository implements EntityRepository using PostgreSQL.
type PostgresEntityRepository struct {
	db *database.DB
}

// NewPostgresEntityRepository creates a new PostgreSQL entity repository.
func NewPostgresEntityRepository(db *database.DB) *PostgresEntityRepository {
	return &PostgresEntityRepository{db: db}
}

// Create inserts a new entity.
func (r *PostgresEntityRepository) Create(ctx context.Context, userID *uuid.UUID, input *CreateEntityInput) (*Entity, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	id := uuid.New()
	now := time.Now()

	// Default metadata to empty object if not provided
	metadata := input.Metadata
	if len(metadata) == 0 {
		metadata = json.RawMessage(`{}`)
	}

	query := `
		INSERT INTO hatamoto_entities (
			id, schema_id, name, slug, locale, status, attributes, metadata,
			version, created_by, updated_by, created_at, updated_at, parent_id, author_id,
			byline_person_id, byline_org, published_at
		) VALUES ($1, $2, $3, $4, COALESCE(NULLIF($15::text, ''), public.hatamoto_site_language()), COALESCE($14::text, 'draft'), $5, $6, 1, $7, $7, $8, $9, $10, $11, $12, $13,
			CASE WHEN $14::text = 'published' THEN now() END)
		RETURNING id, schema_id, name, slug, locale, status, attributes, metadata,
		          version, created_by, updated_by, created_at, updated_at, published_at, parent_id, author_id, byline_person_id, byline_org
	`

	var e Entity
	err = conn.QueryRow(ctx, query,
		id, input.SchemaID, input.Name, input.Slug, input.Attributes, metadata,
		userID, now, now, input.ParentID, input.AuthorID,
		input.BylinePersonID, input.BylineOrg, statusArg(input.Status), input.Locale,
	).Scan(
		&e.ID, &e.SchemaID, &e.Name, &e.Slug, &e.Locale, &e.Status, &e.Attributes, &e.Metadata,
		&e.Version, &e.CreatedBy, &e.UpdatedBy, &e.CreatedAt, &e.UpdatedAt, &e.PublishedAt, &e.ParentID, &e.AuthorID, &e.BylinePersonID, &e.BylineOrg,
	)

	if err != nil {
		if isPgUniqueViolation(err) {
			return nil, slugConflict(err)
		}
		if isPgForeignKeyViolation(err) {
			return nil, fkViolation(err)
		}
		return nil, fmt.Errorf("failed to create entity: %w", err)
	}

	return &e, nil
}

// GetByID retrieves an entity by ID with its schema.
func (r *PostgresEntityRepository) GetByID(ctx context.Context, id uuid.UUID) (*Entity, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	query := `
		SELECT
			e.id, e.schema_id, e.name, e.slug, e.locale, e.status, e.attributes, e.metadata,
			e.version, e.created_by, e.updated_by, e.created_at, e.updated_at, e.published_at, e.parent_id, e.author_id, e.byline_person_id, e.byline_org,
			s.id, s.name, s.slug, s.description, s.schema, s.is_builtin, s.created_at, s.updated_at, s.settings
		FROM hatamoto_entities e
		JOIN hatamoto_entity_schemas s ON e.schema_id = s.id
		WHERE e.id = $1
	`

	var e Entity
	var s EntitySchema

	err = conn.QueryRow(ctx, query, id).Scan(
		&e.ID, &e.SchemaID, &e.Name, &e.Slug, &e.Locale, &e.Status, &e.Attributes, &e.Metadata,
		&e.Version, &e.CreatedBy, &e.UpdatedBy, &e.CreatedAt, &e.UpdatedAt, &e.PublishedAt, &e.ParentID, &e.AuthorID, &e.BylinePersonID, &e.BylineOrg,
		&s.ID, &s.Name, &s.Slug, &s.Description, &s.Schema, &s.IsBuiltin, &s.CreatedAt, &s.UpdatedAt, &s.Settings,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrEntityNotFound
		}
		return nil, fmt.Errorf("failed to get entity: %w", err)
	}

	e.Schema = &s
	return &e, nil
}

// GetBySlug retrieves an entity by slug.
func (r *PostgresEntityRepository) GetBySlug(ctx context.Context, slug string) (*Entity, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	query := `
		SELECT
			e.id, e.schema_id, e.name, e.slug, e.locale, e.status, e.attributes, e.metadata,
			e.version, e.created_by, e.updated_by, e.created_at, e.updated_at, e.published_at, e.parent_id, e.author_id, e.byline_person_id, e.byline_org,
			s.id, s.name, s.slug, s.description, s.schema, s.is_builtin, s.created_at, s.updated_at, s.settings
		FROM hatamoto_entities e
		JOIN hatamoto_entity_schemas s ON e.schema_id = s.id
		WHERE e.slug = $1
		ORDER BY (e.locale = (SELECT public.hatamoto_site_language())) DESC, e.parent_id NULLS FIRST, e.created_at, e.id
		LIMIT 1
	`

	var e Entity
	var s EntitySchema

	err = conn.QueryRow(ctx, query, slug).Scan(
		&e.ID, &e.SchemaID, &e.Name, &e.Slug, &e.Locale, &e.Status, &e.Attributes, &e.Metadata,
		&e.Version, &e.CreatedBy, &e.UpdatedBy, &e.CreatedAt, &e.UpdatedAt, &e.PublishedAt, &e.ParentID, &e.AuthorID, &e.BylinePersonID, &e.BylineOrg,
		&s.ID, &s.Name, &s.Slug, &s.Description, &s.Schema, &s.IsBuiltin, &s.CreatedAt, &s.UpdatedAt, &s.Settings,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrEntityNotFound
		}
		return nil, fmt.Errorf("failed to get entity by slug: %w", err)
	}

	e.Schema = &s
	return &e, nil
}

// Update modifies an entity (creates a version snapshot first).
func (r *PostgresEntityRepository) Update(ctx context.Context, id uuid.UUID, userID *uuid.UUID, input *UpdateEntityInput) (*Entity, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	// Start transaction
	tx, err := conn.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	// Get current entity state with FOR UPDATE lock to prevent race conditions
	var currentVersion int
	var currentAttributes json.RawMessage
	err = tx.QueryRow(ctx,
		`SELECT version, attributes FROM hatamoto_entities WHERE id = $1 FOR UPDATE`,
		id,
	).Scan(&currentVersion, &currentAttributes)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrEntityNotFound
		}
		return nil, fmt.Errorf("failed to get current entity: %w", err)
	}

	if input.ExpectedVersion != nil && *input.ExpectedVersion != currentVersion {
		return nil, &VersionConflictError{Current: currentVersion}
	}

	// Coalesce-on-write: only snapshot a version (and bump the entity version)
	// when the new attributes are ACTUALLY different from the current ones. A
	// save that doesn't change attributes must not create a redundant version.
	changingAttrs := input.Attributes != nil && !jsonRawEqual(*input.Attributes, currentAttributes)
	if changingAttrs {
		// An autosave right after the same person's autosave adds no version:
		// the earlier one already holds the state before the run.
		join := false
		if input.Autosave && userID != nil {
			err = tx.QueryRow(ctx, `
				SELECT EXISTS (
					SELECT 1 FROM (
						SELECT changed_by, change_summary, created_at FROM hatamoto_entity_versions
						WHERE entity_id = $1 ORDER BY version DESC LIMIT 1
					) last
					WHERE last.change_summary = $2 AND last.changed_by = $3 AND last.created_at > NOW() - make_interval(secs => $4))`,
				id, AutosaveSummary, userID, AutosaveWindow.Seconds(),
			).Scan(&join)
			if err != nil {
				return nil, fmt.Errorf("failed to look up the last version: %w", err)
			}
		}
		if !join {
			_, err = tx.Exec(ctx,
				`INSERT INTO hatamoto_entity_versions (entity_id, version, attributes, changed_by, change_summary)
				 VALUES ($1, $2, $3, $4, $5)`,
				id, currentVersion, currentAttributes, userID, input.ChangeSummary,
			)
			if err != nil {
				return nil, fmt.Errorf("failed to create version snapshot: %w", err)
			}
		}
	}
	// Pass attributes to the UPDATE only when they really change so the version
	// counter (bumped when $4 IS NOT NULL) doesn't advance on a no-op save.
	var attrsArg *json.RawMessage
	if changingAttrs {
		attrsArg = input.Attributes
	}

	// The entities_updated_at trigger stamps NOW() on every UPDATE unless
	// hatamoto.import is 'on' (transaction-local), so keeping the date needs both.
	if input.KeepUpdatedAt {
		if _, err := tx.Exec(ctx, `SELECT set_config('hatamoto.import', 'on', true)`); err != nil {
			return nil, fmt.Errorf("failed to keep updated_at: %w", err)
		}
	}

	// Update entity
	query := `
		UPDATE hatamoto_entities
		SET name = COALESCE($2, name),
		    slug = COALESCE($3, slug),
		    locale = COALESCE($16, locale),
		    attributes = COALESCE($4, attributes),
		    metadata = COALESCE($5, metadata),
		    parent_id = CASE WHEN $7 THEN NULL ELSE COALESCE($8, parent_id) END,
		    author_id = CASE WHEN $9 THEN NULL ELSE COALESCE($10, author_id) END,
		    byline_person_id = CASE WHEN $11 THEN NULL ELSE COALESCE($12, byline_person_id) END,
		    byline_org = COALESCE($13, byline_org),
		    status = COALESCE($14::text, status),
		    published_at = CASE WHEN $14::text = 'published' AND published_at IS NULL THEN NOW() ELSE published_at END,
		    updated_by = $6,
		    version = CASE WHEN $4 IS NOT NULL THEN version + 1 ELSE version END,
		    updated_at = CASE WHEN $15 THEN updated_at ELSE NOW() END
		WHERE id = $1
		RETURNING id, schema_id, name, slug, locale, status, attributes, metadata,
		          version, created_by, updated_by, created_at, updated_at, published_at, parent_id, author_id, byline_person_id, byline_org
	`

	var e Entity
	err = tx.QueryRow(ctx, query, id, input.Name, input.Slug, attrsArg, input.Metadata, userID,
		input.ClearParent, input.ParentID, input.ClearAuthor, input.AuthorID,
		input.ClearBylinePerson, input.BylinePersonID, input.BylineOrg, statusArg(input.Status), input.KeepUpdatedAt, input.Locale).Scan(
		&e.ID, &e.SchemaID, &e.Name, &e.Slug, &e.Locale, &e.Status, &e.Attributes, &e.Metadata,
		&e.Version, &e.CreatedBy, &e.UpdatedBy, &e.CreatedAt, &e.UpdatedAt, &e.PublishedAt, &e.ParentID, &e.AuthorID, &e.BylinePersonID, &e.BylineOrg,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrEntityNotFound
		}
		if isPgUniqueViolation(err) {
			return nil, slugConflict(err)
		}
		if isPgForeignKeyViolation(err) {
			return nil, fkViolation(err)
		}
		return nil, fmt.Errorf("failed to update entity: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return &e, nil
}

// UpdateStatus changes an entity's status.
func (r *PostgresEntityRepository) UpdateStatus(ctx context.Context, id uuid.UUID, userID *uuid.UUID, status EntityStatus) (*Entity, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	// Stamp published_at on the FIRST publish only. Re-publishing an already
	// published entity (editor "Publish" on a live post, unpublish/republish)
	// must keep its original publication date: overwriting it with NOW() made
	// imported posts claim they were published on the day of an edit. A
	// published_at that is still in the future (a scheduled post published
	// early by hand) is replaced with NOW() so the page never shows a date
	// that has not happened yet. The scheduled-publish worker does not come
	// through here (see worker/publish_worker.go).
	var publishedAt any = nil
	if status == StatusPublished {
		publishedAt = time.Now()
	}

	query := `
		UPDATE hatamoto_entities
		SET status = $2,
		    updated_by = $3,
		    published_at = CASE
		        WHEN $4::timestamptz IS NULL THEN published_at
		        WHEN published_at IS NULL OR published_at > $4::timestamptz THEN $4::timestamptz
		        ELSE published_at
		    END,
		    updated_at = NOW()
		WHERE id = $1
		RETURNING id, schema_id, name, slug, locale, status, attributes, metadata,
		          version, created_by, updated_by, created_at, updated_at, published_at
	`

	var e Entity
	err = conn.QueryRow(ctx, query, id, status, userID, publishedAt).Scan(
		&e.ID, &e.SchemaID, &e.Name, &e.Slug, &e.Locale, &e.Status, &e.Attributes, &e.Metadata,
		&e.Version, &e.CreatedBy, &e.UpdatedBy, &e.CreatedAt, &e.UpdatedAt, &e.PublishedAt,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrEntityNotFound
		}
		return nil, fmt.Errorf("failed to update entity status: %w", err)
	}

	return &e, nil
}

// Schedule marks an entity scheduled for at: status scheduled and published_at
// at, which worker.PublishWorker promotes once it falls due.
func (r *PostgresEntityRepository) Schedule(ctx context.Context, id uuid.UUID, userID *uuid.UUID, at time.Time) (*Entity, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	var e Entity
	err = conn.QueryRow(ctx, `
		UPDATE hatamoto_entities
		SET status = 'scheduled', published_at = $2, updated_by = $3, updated_at = NOW()
		WHERE id = $1
		RETURNING id, schema_id, name, slug, locale, status, attributes, metadata,
		          version, created_by, updated_by, created_at, updated_at, published_at`,
		id, at, userID).Scan(
		&e.ID, &e.SchemaID, &e.Name, &e.Slug, &e.Locale, &e.Status, &e.Attributes, &e.Metadata,
		&e.Version, &e.CreatedBy, &e.UpdatedBy, &e.CreatedAt, &e.UpdatedAt, &e.PublishedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrEntityNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to schedule entity: %w", err)
	}
	return &e, nil
}

// Delete removes an entity.
func (r *PostgresEntityRepository) Delete(ctx context.Context, id uuid.UUID) error {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	result, err := conn.Exec(ctx, `DELETE FROM hatamoto_entities WHERE id = $1`, id)
	if err != nil {
		if isPgUniqueViolation(err) {
			// Children created since the check would be re-rooted onto a taken slug.
			return ErrHasChildren
		}
		return fmt.Errorf("failed to delete entity: %w", err)
	}

	if result.RowsAffected() == 0 {
		return ErrEntityNotFound
	}

	return nil
}

// List retrieves entities with filtering and keyset pagination.
func (r *PostgresEntityRepository) List(ctx context.Context, filter *ListEntitiesFilter, page *KeysetPage) (*PagedResult[*Entity], error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	// Build query with filters
	baseQuery := `
		SELECT ` + listEntityColumns + `
		FROM hatamoto_entities e
		WHERE 1=1
		  AND e.status <> 'redirected'
	`

	args := []any{}
	argIdx := 1

	var where string
	where, args, argIdx = appendListFilters(filter, args, argIdx)
	baseQuery += where

	// Keyset pagination
	limit := 20
	if page != nil && page.Limit > 0 && page.Limit <= 100 {
		limit = page.Limit
	}

	if page != nil && page.Cursor != nil {
		cursorData, err := base64.StdEncoding.DecodeString(*page.Cursor)
		if err == nil {
			var cursor struct {
				CreatedAt time.Time `json:"c"`
				ID        uuid.UUID `json:"i"`
			}
			if json.Unmarshal(cursorData, &cursor) == nil {
				if page.Direction == "prev" {
					baseQuery += fmt.Sprintf(" AND (e.created_at, e.id) > ($%d, $%d)", argIdx, argIdx+1)
				} else {
					baseQuery += fmt.Sprintf(" AND (e.created_at, e.id) < ($%d, $%d)", argIdx, argIdx+1)
				}
				args = append(args, cursor.CreatedAt, cursor.ID)
				argIdx += 2
			}
		}
	}

	// Order and limit
	if page != nil && page.Direction == "prev" {
		baseQuery += " ORDER BY e.created_at ASC, e.id ASC"
	} else {
		baseQuery += " ORDER BY e.created_at DESC, e.id DESC"
	}
	baseQuery += fmt.Sprintf(" LIMIT $%d", argIdx)
	args = append(args, limit+1) // Fetch one extra to check for more

	rows, err := conn.Query(ctx, baseQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list entities: %w", err)
	}
	defer rows.Close()

	var entities []*Entity
	for rows.Next() {
		e, err := scanListEntity(rows)
		if err != nil {
			return nil, err
		}
		entities = append(entities, e)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Reverse if paginating backwards
	if page != nil && page.Direction == "prev" {
		for i, j := 0, len(entities)-1; i < j; i, j = i+1, j-1 {
			entities[i], entities[j] = entities[j], entities[i]
		}
	}

	// Build result
	result := &PagedResult[*Entity]{
		Items:   entities,
		HasMore: len(entities) > limit,
	}

	if result.HasMore {
		result.Items = result.Items[:limit]
	}

	// Generate cursors
	if len(result.Items) > 0 {
		first := result.Items[0]
		last := result.Items[len(result.Items)-1]

		prevCursor, _ := json.Marshal(map[string]any{"c": first.CreatedAt, "i": first.ID})
		nextCursor, _ := json.Marshal(map[string]any{"c": last.CreatedAt, "i": last.ID})

		prevStr := base64.StdEncoding.EncodeToString(prevCursor)
		nextStr := base64.StdEncoding.EncodeToString(nextCursor)

		result.PrevCursor = &prevStr
		if result.HasMore {
			result.NextCursor = &nextStr
		}
	}

	return result, nil
}

// ListWithOffset retrieves entities with filtering and offset pagination.
func (r *PostgresEntityRepository) ListWithOffset(ctx context.Context, filter *ListEntitiesFilter, page *OffsetPage) (*OffsetPagedResult[*Entity], error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	// Default pagination values
	pageNum := 1
	limit := 20
	if page != nil {
		if page.Page > 0 {
			pageNum = page.Page
		}
		if page.Limit > 0 && page.Limit <= 100 {
			limit = page.Limit
		}
	}
	offset := (pageNum - 1) * limit

	// Build WHERE clause with filters. 'redirected' entities are historic
	// migration stubs kept as successor pointers for TipTap anchor
	// resolution — hidden from the catalog view.
	whereClause := "WHERE 1=1 AND e.status <> 'redirected'"
	args := []any{}
	argIdx := 1

	var filters string
	filters, args, argIdx = appendListFilters(filter, args, argIdx)
	whereClause += filters

	// Count total matching records
	countQuery := fmt.Sprintf(`SELECT COUNT(*) FROM hatamoto_entities e %s`, whereClause)
	var total int
	err = conn.QueryRow(ctx, countQuery, args...).Scan(&total)
	if err != nil {
		return nil, fmt.Errorf("failed to count entities: %w", err)
	}

	// Calculate total pages
	totalPages := max((total+limit-1)/limit, 1)

	// Build main query with pagination. An unfiltered, unsorted listing keeps
	// the API's historic newest-created-first order.
	orderClause := "ORDER BY e.created_at DESC, e.id DESC"
	if filter != nil && (filter.Sort != "" || filter.Asc) {
		orderClause = listOrder(filter)
	}
	dataQuery := fmt.Sprintf(`
		SELECT %s
		FROM hatamoto_entities e
		%s
		%s
		LIMIT $%d OFFSET $%d
	`, listEntityColumns, whereClause, orderClause, argIdx, argIdx+1)
	args = append(args, limit, offset)

	rows, err := conn.Query(ctx, dataQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list entities: %w", err)
	}
	defer rows.Close()

	var entities []*Entity
	for rows.Next() {
		e, err := scanListEntity(rows)
		if err != nil {
			return nil, err
		}
		entities = append(entities, e)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Ensure Items is never nil
	if entities == nil {
		entities = []*Entity{}
	}

	return &OffsetPagedResult[*Entity]{
		Items:      entities,
		Total:      total,
		Page:       pageNum,
		Limit:      limit,
		TotalPages: totalPages,
	}, nil
}

// SchemaStatusCount is how many entities of one schema have one status.
type SchemaStatusCount struct {
	SchemaID uuid.UUID
	Status   EntityStatus
	Count    int
}

// CountBySchemaStatus counts the catalog entities per schema and status in one
// grouped query (the admin list's type tabs and status select).
func (r *PostgresEntityRepository) CountBySchemaStatus(ctx context.Context) ([]SchemaStatusCount, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	rows, err := conn.Query(ctx, `SELECT schema_id, status, COUNT(*) FROM hatamoto_entities WHERE status <> 'redirected' GROUP BY schema_id, status`)
	if err != nil {
		return nil, fmt.Errorf("failed to count entities: %w", err)
	}
	defer rows.Close()
	var out []SchemaStatusCount
	for rows.Next() {
		var c SchemaStatusCount
		if err := rows.Scan(&c.SchemaID, &c.Status, &c.Count); err != nil {
			return nil, fmt.Errorf("failed to scan entity count: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ParentCount is one parent entity and how many entities sit directly under it.
type ParentCount struct {
	Parent *Entity
	Count  int
}

// CountByParent returns the limit parents with the most children (most first),
// each with its child count, in one query (the admin list's Parent filter).
func (r *PostgresEntityRepository) CountByParent(ctx context.Context, limit int) ([]ParentCount, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	rows, err := conn.Query(ctx, `SELECT `+listEntityColumns+`, c.n
		FROM (SELECT parent_id, COUNT(*) AS n FROM hatamoto_entities
		       WHERE parent_id IS NOT NULL AND status <> 'redirected'
		       GROUP BY parent_id ORDER BY n DESC, parent_id LIMIT $1) c
		JOIN hatamoto_entities e ON e.id = c.parent_id
		ORDER BY c.n DESC, e.id`, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to count entities per parent: %w", err)
	}
	defer rows.Close()
	var out []ParentCount
	for rows.Next() {
		var e Entity
		var n int
		if err := rows.Scan(
			&e.ID, &e.SchemaID, &e.Name, &e.Slug, &e.Locale, &e.Status, &e.Attributes, &e.Metadata,
			&e.Version, &e.CreatedBy, &e.UpdatedBy, &e.CreatedAt, &e.UpdatedAt, &e.PublishedAt, &e.ParentID, &e.AuthorID, &e.BylinePersonID, &e.BylineOrg, &n,
		); err != nil {
			return nil, fmt.Errorf("failed to scan parent count: %w", err)
		}
		out = append(out, ParentCount{Parent: &e, Count: n})
	}
	return out, rows.Err()
}

// CountChildren returns how many entities sit directly under each of parentIDs
// (redirected ones excluded, as in CountByParent); parents with none are absent.
func (r *PostgresEntityRepository) CountChildren(ctx context.Context, parentIDs []uuid.UUID) (map[uuid.UUID]int, error) {
	out := map[uuid.UUID]int{}
	if len(parentIDs) == 0 {
		return out, nil
	}
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	rows, err := conn.Query(ctx, `SELECT parent_id, COUNT(*) FROM hatamoto_entities
		WHERE parent_id = ANY($1) AND status <> 'redirected' GROUP BY parent_id`, parentIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to count child entities: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, fmt.Errorf("failed to scan child count: %w", err)
		}
		out[id] = n
	}
	return out, rows.Err()
}

// listEntityColumns is the admin list projection (scanListEntity order).
const listEntityColumns = `e.id, e.schema_id, e.name, e.slug, e.locale, e.status, e.attributes, e.metadata,
		       e.version, e.created_by, e.updated_by, e.created_at, e.updated_at, e.published_at, e.parent_id, e.author_id, e.byline_person_id, e.byline_org`

func scanListEntity(rows pgx.Rows) (*Entity, error) {
	var e Entity
	if err := rows.Scan(
		&e.ID, &e.SchemaID, &e.Name, &e.Slug, &e.Locale, &e.Status, &e.Attributes, &e.Metadata,
		&e.Version, &e.CreatedBy, &e.UpdatedBy, &e.CreatedAt, &e.UpdatedAt, &e.PublishedAt, &e.ParentID, &e.AuthorID, &e.BylinePersonID, &e.BylineOrg,
	); err != nil {
		return nil, fmt.Errorf("failed to scan entity: %w", err)
	}
	return &e, nil
}

// appendListFilters renders the list filters as " AND ..." clauses.
func appendListFilters(filter *ListEntitiesFilter, args []any, argIdx int) (string, []any, int) {
	if filter == nil {
		return "", args, argIdx
	}
	var b strings.Builder
	if filter.SchemaID != nil {
		fmt.Fprintf(&b, " AND e.schema_id = $%d", argIdx)
		args = append(args, *filter.SchemaID)
		argIdx++
	}
	if len(filter.ExcludeSchemaIDs) > 0 {
		ids := make([]string, len(filter.ExcludeSchemaIDs))
		for i, id := range filter.ExcludeSchemaIDs {
			ids[i] = id.String()
		}
		fmt.Fprintf(&b, " AND e.schema_id <> ALL($%d::uuid[])", argIdx)
		args = append(args, ids)
		argIdx++
	}
	if filter.Status != nil {
		fmt.Fprintf(&b, " AND e.status = $%d", argIdx)
		args = append(args, *filter.Status)
		argIdx++
	}
	if filter.Search != "" {
		fmt.Fprintf(&b, " AND (e.name ILIKE $%d OR e.slug ILIKE $%d)", argIdx, argIdx)
		args = append(args, "%"+filter.Search+"%")
		argIdx++
	}
	if filter.AuthorID != nil {
		fmt.Fprintf(&b, " AND e.author_id = $%d", argIdx)
		args = append(args, *filter.AuthorID)
		argIdx++
	}
	if filter.PersonID != nil {
		fmt.Fprintf(&b, " AND e.byline_person_id = $%d", argIdx)
		args = append(args, *filter.PersonID)
		argIdx++
	}
	if filter.TermID != nil {
		fmt.Fprintf(&b, ` AND EXISTS (
			WITH RECURSIVE sub(id) AS (
				SELECT id FROM hatamoto_terms WHERE id = $%d
				UNION ALL
				SELECT t.id FROM hatamoto_terms t JOIN sub ON t.parent_id = sub.id
			)
			SELECT 1 FROM hatamoto_entity_terms et WHERE et.entity_id = e.id AND et.term_id IN (SELECT id FROM sub))`, argIdx)
		args = append(args, *filter.TermID)
		argIdx++
	}
	if filter.Under != nil {
		fmt.Fprintf(&b, ` AND e.id IN (
			WITH RECURSIVE sub(id, depth) AS (
				SELECT id, 1 FROM hatamoto_entities WHERE parent_id = $%d
				UNION ALL
				SELECT c.id, sub.depth + 1 FROM hatamoto_entities c JOIN sub ON c.parent_id = sub.id WHERE sub.depth < 32
			)
			SELECT id FROM sub)`, argIdx)
		args = append(args, *filter.Under)
		argIdx++
	}
	if filter.UpdatedFrom != nil {
		fmt.Fprintf(&b, " AND e.updated_at >= $%d", argIdx)
		args = append(args, *filter.UpdatedFrom)
		argIdx++
	}
	if filter.UpdatedBefore != nil {
		fmt.Fprintf(&b, " AND e.updated_at < $%d", argIdx)
		args = append(args, *filter.UpdatedBefore)
		argIdx++
	}
	return b.String(), args, argIdx
}

// listOrder is the ORDER BY of the offset listing: a whitelisted column, then
// the id so pages never overlap.
func listOrder(filter *ListEntitiesFilter) string {
	col, asc := "e.updated_at", false
	if filter != nil {
		asc = filter.Asc
		switch filter.Sort {
		case "name":
			col = "lower(e.name)"
		case "status":
			col = "e.status"
		case "created":
			col = "e.created_at"
		case "", "updated":
		default:
			asc = false
		}
	}
	dir := "DESC"
	if asc {
		dir = "ASC"
	}
	return fmt.Sprintf("ORDER BY %s %s, e.id %s", col, dir, dir)
}

// jsonRawEqual reports whether two JSON blobs are semantically equal (key order
// and insignificant whitespace ignored), by canonicalizing both via json.Number.
func jsonRawEqual(a, b json.RawMessage) bool {
	var av, bv any
	da := json.NewDecoder(bytes.NewReader(a))
	da.UseNumber()
	db := json.NewDecoder(bytes.NewReader(b))
	db.UseNumber()
	if err := da.Decode(&av); err != nil {
		return false
	}
	if err := db.Decode(&bv); err != nil {
		return false
	}
	ca, err1 := json.Marshal(av)
	cb, err2 := json.Marshal(bv)
	if err1 != nil || err2 != nil {
		return false
	}
	return string(ca) == string(cb)
}

// PruneVersions coalesces identical-consecutive versions and then keeps only the
// newest `keep` versions per entity. Returns the number of rows deleted.
func (r *PostgresEntityRepository) PruneVersions(ctx context.Context, keep int) (int64, error) {
	if keep <= 0 {
		keep = 20
	}
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return 0, err
	}
	defer conn.Release()

	var total int64
	// 1. Coalesce: within a run of identical-consecutive versions keep the
	//    NEWEST (it carries who made the latest save). LEAD marks a row as a
	//    duplicate when it equals the NEXT version, so the earlier copies are
	//    removed. jsonb '=' is order-independent.
	tag, err := conn.Exec(ctx, `
		DELETE FROM hatamoto_entity_versions v
		USING (
			SELECT id, (attributes = LEAD(attributes) OVER (PARTITION BY entity_id ORDER BY version)) AS dup
			FROM hatamoto_entity_versions
		) d
		WHERE v.id = d.id AND d.dup`)
	if err != nil {
		return total, fmt.Errorf("coalesce versions: %w", err)
	}
	total += tag.RowsAffected()

	// 2. Keep `keep` per entity: explicit versions first (newest first), then
	//    autosaves, so a writing session never pushes out the real history.
	tag, err = conn.Exec(ctx, `
		DELETE FROM hatamoto_entity_versions
		WHERE id IN (
			SELECT id FROM (
				SELECT id, ROW_NUMBER() OVER (
					PARTITION BY entity_id
					ORDER BY (change_summary IS NOT DISTINCT FROM $2) ASC, version DESC) AS rn
				FROM hatamoto_entity_versions
			) t WHERE t.rn > $1
		)`, keep, AutosaveSummary)
	if err != nil {
		return total, fmt.Errorf("prune versions: %w", err)
	}
	total += tag.RowsAffected()
	return total, nil
}

// CreateVersion creates a new version record for an entity.
//
// The tenantID parameter is retained for API compatibility but no longer
// participates in the SQL (Path B Migration 079 dropped the column).
func (r *PostgresEntityRepository) CreateVersion(ctx context.Context, entityID uuid.UUID, version int, attributes json.RawMessage, changedBy *uuid.UUID, changeSummary *string) (*EntityVersion, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	id := uuid.New()

	query := `
		INSERT INTO hatamoto_entity_versions (id, entity_id, version, attributes, changed_by, change_summary)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, entity_id, version, attributes, changed_by, change_summary, created_at
	`

	var v EntityVersion
	err = conn.QueryRow(ctx, query, id, entityID, version, attributes, changedBy, changeSummary).Scan(
		&v.ID, &v.EntityID, &v.Version, &v.Attributes, &v.ChangedBy, &v.ChangeSummary, &v.CreatedAt,
	)

	if err != nil {
		return nil, fmt.Errorf("failed to create entity version: %w", err)
	}

	return &v, nil
}

// GetVersion retrieves a specific version of an entity.
func (r *PostgresEntityRepository) GetVersion(ctx context.Context, entityID uuid.UUID, version int) (*EntityVersion, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	query := `
		SELECT id, entity_id, version, attributes, changed_by, change_summary, created_at
		FROM hatamoto_entity_versions
		WHERE entity_id = $1 AND version = $2
	`

	var v EntityVersion
	err = conn.QueryRow(ctx, query, entityID, version).Scan(
		&v.ID, &v.EntityID, &v.Version, &v.Attributes, &v.ChangedBy, &v.ChangeSummary, &v.CreatedAt,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrVersionNotFound
		}
		return nil, fmt.Errorf("failed to get entity version: %w", err)
	}

	return &v, nil
}

// ListVersions retrieves version history for an entity.
func (r *PostgresEntityRepository) ListVersions(ctx context.Context, entityID uuid.UUID, limit int) ([]*EntityVersion, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	if limit <= 0 || limit > 100 {
		limit = 50
	}

	query := `
		SELECT id, entity_id, version, attributes, changed_by, change_summary, created_at
		FROM hatamoto_entity_versions
		WHERE entity_id = $1
		ORDER BY version DESC
		LIMIT $2
	`

	rows, err := conn.Query(ctx, query, entityID, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to list entity versions: %w", err)
	}
	defer rows.Close()

	var versions []*EntityVersion
	for rows.Next() {
		var v EntityVersion
		if err := rows.Scan(
			&v.ID, &v.EntityID, &v.Version, &v.Attributes, &v.ChangedBy, &v.ChangeSummary, &v.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan entity version: %w", err)
		}
		versions = append(versions, &v)
	}

	return versions, rows.Err()
}

func pgError(err error) *pgconn.PgError {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		return pg
	}
	return nil
}

// isPgUniqueViolation checks if an error is a PostgreSQL unique constraint violation.
func isPgUniqueViolation(err error) bool {
	pg := pgError(err)
	return pg != nil && pg.Code == "23505"
}

// isPgForeignKeyViolation checks if an error is a PostgreSQL foreign key violation.
func isPgForeignKeyViolation(err error) bool {
	pg := pgError(err)
	return pg != nil && pg.Code == "23503"
}

// slugConflict turns a unique violation of an entity write into
// ErrSlugAlreadyExists, saying which rule applied: the schema's URL pattern
// decides whether slugs are unique among siblings or across the schema.
func slugConflict(err error) error {
	switch pgError(err).ConstraintName {
	case "uq_entities_schema_parent_slug":
		return fmt.Errorf("%w: another entity of this schema and locale has this slug under the same parent", ErrSlugAlreadyExists)
	case "uq_entities_schema_slug":
		return fmt.Errorf("%w: another entity of this schema and locale has this slug (the schema's URL pattern has no {path} or {parent}, so a slug is unique across the whole schema and locale)", ErrSlugAlreadyExists)
	}
	return ErrSlugAlreadyExists
}

// fkViolation names which reference an entity write broke.
func fkViolation(err error) error {
	switch pgError(err).ConstraintName {
	case "hatamoto_entities_author_fkey":
		return ErrAuthorNotFound
	case "hatamoto_entities_parent_id_fkey":
		return ErrParentNotFound
	}
	return ErrSchemaNotFound
}

// statusArg is the status column argument for an optional status.
func statusArg(s *EntityStatus) *string {
	if s == nil {
		return nil
	}
	v := string(*s)
	return &v
}

// ScanMentioning calls fn, newest first, for each entity other than id whose
// attributes contain the id as text, until fn returns false. It is a sequence
// scan: used only by the block usage list, never on the render path.
func (r *PostgresEntityRepository) ScanMentioning(ctx context.Context, id uuid.UUID, fn func(*Entity) bool) error {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	rows, err := conn.Query(ctx, `
		SELECT e.id, e.name, e.slug, e.status, e.attributes, e.updated_at, s.slug
		FROM hatamoto_entities e
		JOIN hatamoto_entity_schemas s ON s.id = e.schema_id
		WHERE e.id <> $1 AND position($2 in e.attributes::text) > 0
		ORDER BY e.updated_at DESC, e.id`, id, id.String())
	if err != nil {
		return fmt.Errorf("scan entities for %s: %w", id, err)
	}
	defer rows.Close()
	for rows.Next() {
		var e Entity
		var schemaSlug string
		if err := rows.Scan(&e.ID, &e.Name, &e.Slug, &e.Status, &e.Attributes, &e.UpdatedAt, &schemaSlug); err != nil {
			return err
		}
		e.Schema = &EntitySchema{Slug: schemaSlug}
		if !fn(&e) {
			return nil
		}
	}
	return rows.Err()
}
