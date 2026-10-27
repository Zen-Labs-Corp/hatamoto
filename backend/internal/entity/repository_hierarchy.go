package entity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"hatamoto.dev/core/internal/urlpattern"
)

// HierarchyStore reads the parent tree. PostgresEntityRepository implements
// it; the service uses it when its repository does.
type HierarchyStore interface {
	// Ancestors returns the parent chain of id, root first, without id itself.
	// A chain deeper than urlpattern.MaxPathDepth is cut at that depth, which
	// is also what ends a cycle.
	Ancestors(ctx context.Context, id uuid.UUID) ([]BreadcrumbPathItem, error)
	// Descendants returns every published descendant of id (children,
	// grandchildren, ...), parents before children.
	Descendants(ctx context.Context, id uuid.UUID) ([]*Descendant, error)
	// SubtreeHeight is how many levels of rows (any status) lie below id: 0
	// for a leaf, 1 with children only. Counting stops at twice
	// urlpattern.MaxPathDepth, which is also what ends a cycle.
	SubtreeHeight(ctx context.Context, id uuid.UUID) (int, error)
	// HasChildren reports whether any row has id as its parent.
	HasChildren(ctx context.Context, id uuid.UUID) (bool, error)
	// Child returns the base row (no variant) with this slug directly under
	// parent, or a root row when parent is nil; ErrEntityNotFound when none.
	// Slugs are unique per schema, not among all siblings: schemaSlug narrows
	// the lookup to one schema, and without it ErrAmbiguousSlug is returned
	// when rows of several schemas match.
	Child(ctx context.Context, parent *uuid.UUID, slug, schemaSlug string) (*Entity, error)
}

// Child implements HierarchyStore.
func (r *PostgresEntityRepository) Child(ctx context.Context, parent *uuid.UUID, slug, schemaSlug string) (*Entity, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()
	rows, err := conn.Query(ctx, `
		SELECT
			e.id, e.schema_id, e.name, e.slug, e.locale, e.status, e.attributes, e.metadata,
			e.version, e.created_by, e.updated_by, e.created_at, e.updated_at, e.published_at, e.parent_id, e.author_id, e.byline_person_id, e.byline_org,
			s.id, s.name, s.slug, s.description, s.schema, s.is_builtin, s.created_at, s.updated_at, s.settings
		FROM hatamoto_entities e
		JOIN hatamoto_entity_schemas s ON e.schema_id = s.id
		WHERE e.slug = $1 AND e.parent_id IS NOT DISTINCT FROM $2 AND e.variant_key IS NULL
			AND ($3 = '' OR s.slug = $3)
			AND (e.locale = (SELECT public.hatamoto_site_language()) OR NOT EXISTS (
				SELECT 1 FROM hatamoto_entities x
				WHERE x.schema_id = e.schema_id AND x.slug = e.slug AND x.parent_id IS NOT DISTINCT FROM e.parent_id
				  AND x.variant_key IS NULL AND x.locale = (SELECT public.hatamoto_site_language())))
		ORDER BY e.created_at, e.id
		LIMIT 2`, slug, parent, schemaSlug)
	if err != nil {
		return nil, fmt.Errorf("failed to get entity by parent and slug: %w", err)
	}
	defer rows.Close()
	var found []*Entity
	for rows.Next() {
		var e Entity
		var s EntitySchema
		if err := rows.Scan(
			&e.ID, &e.SchemaID, &e.Name, &e.Slug, &e.Locale, &e.Status, &e.Attributes, &e.Metadata,
			&e.Version, &e.CreatedBy, &e.UpdatedBy, &e.CreatedAt, &e.UpdatedAt, &e.PublishedAt, &e.ParentID, &e.AuthorID, &e.BylinePersonID, &e.BylineOrg,
			&s.ID, &s.Name, &s.Slug, &s.Description, &s.Schema, &s.IsBuiltin, &s.CreatedAt, &s.UpdatedAt, &s.Settings,
		); err != nil {
			return nil, fmt.Errorf("failed to get entity by parent and slug: %w", err)
		}
		e.Schema = &s
		found = append(found, &e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to get entity by parent and slug: %w", err)
	}
	switch len(found) {
	case 0:
		return nil, ErrEntityNotFound
	case 1:
		return found[0], nil
	}
	return nil, ErrAmbiguousSlug
}

// SubtreeHeight implements HierarchyStore.
func (r *PostgresEntityRepository) SubtreeHeight(ctx context.Context, id uuid.UUID) (int, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return 0, err
	}
	defer conn.Release()
	var h int
	err = conn.QueryRow(ctx, `
		WITH RECURSIVE down(id, depth) AS (
			SELECT c.id, 1 FROM hatamoto_entities c WHERE c.parent_id = $1
			UNION ALL
			SELECT c.id, down.depth + 1 FROM hatamoto_entities c JOIN down ON c.parent_id = down.id WHERE down.depth < $2
		)
		SELECT COALESCE(max(depth), 0) FROM down`, id, urlpattern.MaxPathDepth*2).Scan(&h)
	if err != nil {
		return 0, fmt.Errorf("failed to measure subtree: %w", err)
	}
	return h, nil
}

// HasChildren implements HierarchyStore.
func (r *PostgresEntityRepository) HasChildren(ctx context.Context, id uuid.UUID) (bool, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return false, err
	}
	defer conn.Release()
	var has bool
	err = conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM hatamoto_entities WHERE parent_id = $1)`, id).Scan(&has)
	return has, err
}

// Descendant is a published row below another entity.
type Descendant struct {
	Entity *Entity
	// Between holds the slugs of the rows between the starting entity and
	// this one, root first (empty for a direct child).
	Between []string
}

// Ancestors implements HierarchyStore.
func (r *PostgresEntityRepository) Ancestors(ctx context.Context, id uuid.UUID) ([]BreadcrumbPathItem, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()
	rows, err := conn.Query(ctx, `
		WITH RECURSIVE up(id, name, slug, parent_id, depth) AS (
			SELECT p.id, p.name, p.slug, p.parent_id, 1
			FROM hatamoto_entities e JOIN hatamoto_entities p ON p.id = e.parent_id WHERE e.id = $1
			UNION ALL
			SELECT q.id, q.name, q.slug, q.parent_id, up.depth + 1
			FROM hatamoto_entities q JOIN up ON q.id = up.parent_id
			WHERE up.depth < $2
		)
		SELECT id, name, slug FROM up ORDER BY depth DESC`, id, urlpattern.MaxPathDepth*2)
	if err != nil {
		return nil, fmt.Errorf("failed to load ancestors: %w", err)
	}
	defer rows.Close()
	var out []BreadcrumbPathItem
	for rows.Next() {
		var it BreadcrumbPathItem
		if err := rows.Scan(&it.ID, &it.Name, &it.Slug); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// Descendants implements HierarchyStore.
func (r *PostgresEntityRepository) Descendants(ctx context.Context, id uuid.UUID) ([]*Descendant, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()
	rows, err := conn.Query(ctx, `
		WITH RECURSIVE down(id, between, depth) AS (
			SELECT c.id, ARRAY[]::text[], 1 FROM hatamoto_entities c WHERE c.parent_id = $1
			UNION ALL
			SELECT c.id, down.between || p.slug, down.depth + 1
			FROM hatamoto_entities c
			JOIN down ON c.parent_id = down.id
			JOIN hatamoto_entities p ON p.id = down.id
			WHERE down.depth < $2
		)
		SELECT e.id, e.schema_id, e.name, e.slug, e.locale, e.status, e.attributes, e.created_at, e.published_at, e.parent_id,
		       s.slug, down.between
		FROM down
		JOIN hatamoto_entities e ON e.id = down.id
		JOIN hatamoto_entity_schemas s ON s.id = e.schema_id
		WHERE e.status = 'published' AND e.variant_key IS NULL AND e.id <> $1
		ORDER BY down.depth, e.created_at, e.id`, id, urlpattern.MaxPathDepth)
	if err != nil {
		return nil, fmt.Errorf("failed to load descendants: %w", err)
	}
	defer rows.Close()
	var out []*Descendant
	for rows.Next() {
		var e Entity
		var schemaSlug string
		var between []string
		if err := rows.Scan(&e.ID, &e.SchemaID, &e.Name, &e.Slug, &e.Locale, &e.Status, &e.Attributes, &e.CreatedAt, &e.PublishedAt, &e.ParentID, &schemaSlug, &between); err != nil {
			return nil, err
		}
		e.Schema = &EntitySchema{ID: e.SchemaID, Slug: schemaSlug}
		out = append(out, &Descendant{Entity: &e, Between: between})
	}
	return out, rows.Err()
}

// ImportStore is the persistence the importer needs. PostgresEntityRepository
// implements it.
type ImportStore interface {
	// GetByImportKey returns the entity imported under key, ErrEntityNotFound
	// when there is none.
	GetByImportKey(ctx context.Context, key string) (*Entity, error)
	// ImportUpsert inserts rec, or updates the entity rec.ID names, keeping
	// the dates rec carries (the trigger-free import path). created is true
	// for an insert.
	ImportUpsert(ctx context.Context, rec *ImportRecord) (e *Entity, created bool, err error)
}

// ImportRecord is one validated, sanitised row ready to store.
type ImportRecord struct {
	ID         *uuid.UUID // existing entity to update; nil inserts
	SchemaID   uuid.UUID
	Name, Slug string
	Status     EntityStatus
	Attributes json.RawMessage
	Metadata   json.RawMessage
	ParentID   *uuid.UUID
	AuthorID   *uuid.UUID
	ActorID    *uuid.UUID

	BylinePersonID *uuid.UUID
	BylineOrg      bool

	// Locale is a BCP 47 tag; empty inserts the site language and keeps the
	// stored value on update.
	Locale string

	CreatedAt, UpdatedAt, PublishedAt time.Time
}

// GetByImportKey implements ImportStore.
func (r *PostgresEntityRepository) GetByImportKey(ctx context.Context, key string) (*Entity, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()
	var id uuid.UUID
	err = conn.QueryRow(ctx, `SELECT id FROM hatamoto_entities WHERE metadata ? 'import_key' AND metadata->>'import_key' = $1`, key).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrEntityNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to look up import key: %w", err)
	}
	return r.GetByID(ctx, id)
}

// ImportUpsert implements ImportStore.
func (r *PostgresEntityRepository) ImportUpsert(ctx context.Context, rec *ImportRecord) (*Entity, bool, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, false, err
	}
	defer conn.Release()
	tx, err := conn.Begin(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SET LOCAL hatamoto.import = 'on'`); err != nil {
		return nil, false, fmt.Errorf("failed to start import row: %w", err)
	}

	published := any(nil)
	if !rec.PublishedAt.IsZero() {
		published = rec.PublishedAt
	}
	var e Entity
	created := rec.ID == nil
	if created {
		err = tx.QueryRow(ctx, `
			INSERT INTO hatamoto_entities (schema_id, name, slug, locale, status, attributes, metadata, version,
				created_by, updated_by, created_at, updated_at, published_at, author_id, parent_id, byline_person_id, byline_org)
			VALUES ($1, $2, $3, COALESCE(NULLIF($15::text, ''), public.hatamoto_site_language()), $4, $5, $6, 1, $7, $7, $8, $9, $10, $11, $12, $13, $14)
			RETURNING id, schema_id, name, slug, locale, status, attributes, metadata, version, created_by, updated_by,
			          created_at, updated_at, published_at, parent_id, author_id, byline_person_id, byline_org`,
			rec.SchemaID, rec.Name, rec.Slug, rec.Status, rec.Attributes, rec.Metadata,
			rec.ActorID, rec.CreatedAt, rec.UpdatedAt, published, rec.AuthorID, rec.ParentID,
			rec.BylinePersonID, rec.BylineOrg, rec.Locale,
		).Scan(&e.ID, &e.SchemaID, &e.Name, &e.Slug, &e.Locale, &e.Status, &e.Attributes, &e.Metadata, &e.Version, &e.CreatedBy, &e.UpdatedBy,
			&e.CreatedAt, &e.UpdatedAt, &e.PublishedAt, &e.ParentID, &e.AuthorID, &e.BylinePersonID, &e.BylineOrg)
	} else {
		var cur json.RawMessage
		if err = tx.QueryRow(ctx, `SELECT attributes FROM hatamoto_entities WHERE id = $1 FOR UPDATE`, *rec.ID).Scan(&cur); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, false, ErrEntityNotFound
			}
			return nil, false, fmt.Errorf("failed to lock entity: %w", err)
		}
		changing := !jsonRawEqual(rec.Attributes, cur)
		if changing {
			if _, err = tx.Exec(ctx, `INSERT INTO hatamoto_entity_versions (entity_id, version, attributes, changed_by, change_summary)
				SELECT id, version, attributes, $2, 'import re-run' FROM hatamoto_entities WHERE id = $1`, *rec.ID, rec.ActorID); err != nil {
				return nil, false, fmt.Errorf("failed to create version snapshot: %w", err)
			}
		}
		err = tx.QueryRow(ctx, `
			UPDATE hatamoto_entities SET schema_id = $2, name = $3, slug = $4, status = $5, attributes = $6,
				metadata = $7, version = CASE WHEN $13 THEN version + 1 ELSE version END, updated_by = $8,
				created_at = $9, updated_at = $10, published_at = $11, author_id = $12, parent_id = $14,
				byline_person_id = $15, byline_org = $16, locale = COALESCE(NULLIF($17::text, ''), locale)
			WHERE id = $1
			RETURNING id, schema_id, name, slug, locale, status, attributes, metadata, version, created_by, updated_by,
			          created_at, updated_at, published_at, parent_id, author_id, byline_person_id, byline_org`,
			*rec.ID, rec.SchemaID, rec.Name, rec.Slug, rec.Status, rec.Attributes, rec.Metadata,
			rec.ActorID, rec.CreatedAt, rec.UpdatedAt, published, rec.AuthorID, changing, rec.ParentID, rec.BylinePersonID, rec.BylineOrg, rec.Locale,
		).Scan(&e.ID, &e.SchemaID, &e.Name, &e.Slug, &e.Locale, &e.Status, &e.Attributes, &e.Metadata, &e.Version, &e.CreatedBy, &e.UpdatedBy,
			&e.CreatedAt, &e.UpdatedAt, &e.PublishedAt, &e.ParentID, &e.AuthorID, &e.BylinePersonID, &e.BylineOrg)
	}
	if err != nil {
		switch {
		case isPgUniqueViolation(err) && pgError(err).ConstraintName == "uq_entities_import_key":
			return nil, false, fmt.Errorf("import key already used: %w", err)
		case isPgUniqueViolation(err):
			return nil, false, slugConflict(err)
		case isPgForeignKeyViolation(err):
			return nil, false, fkViolation(err)
		}
		return nil, false, fmt.Errorf("failed to store imported entity: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, false, fmt.Errorf("failed to commit import row: %w", err)
	}
	return &e, created, nil
}
