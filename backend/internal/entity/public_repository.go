// Package entity provides entity domain models and repository interfaces.
package entity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/urlpattern"
)

// PublicRepository provides read-only access to published entities.
// This bypasses RLS for public-facing content.
type PublicRepository interface {
	// GetPublishedBySlug retrieves a published entity by slug for a tenant.
	GetPublishedBySlug(ctx context.Context, slug string) (*Entity, error)

	// GetPublishedByID retrieves a published entity by ID.
	GetPublishedByID(ctx context.Context, entityID uuid.UUID) (*Entity, error)

	// GetPublishedByIDs retrieves multiple published entities by their IDs.
	GetPublishedByIDs(ctx context.Context, ids []uuid.UUID) ([]*Entity, error)

	// ListPublished retrieves paginated published base entities of one schema.
	ListPublished(ctx context.Context, schemaSlug string, page, perPage int) (*PublicPagedResult, error)

	// ListPublishedBySchemas is ListPublished over several schema slugs (a
	// schema and its aliases).
	ListPublishedBySchemas(ctx context.Context, schemaSlugs []string, page, perPage int) (*PublicPagedResult, error)

	// ListAllPublished retrieves all published entities for sitemap generation.
	ListAllPublished(ctx context.Context) ([]*SitemapEntry, error)

	// ListAllPublishedWithAttrs is ListAllPublished that also hydrates
	// SitemapEntry.Attrs with the named top-level attributes (the ones schema
	// URL overrides compare against).
	ListAllPublishedWithAttrs(ctx context.Context, attrKeys []string) ([]*SitemapEntry, error)

	// GetHomepage retrieves the homepage entity (slug = "home" or first published page).
	GetHomepage(ctx context.Context) (*Entity, error)

	// GetPublishedBySlugAndSchema retrieves a published entity by slug filtered to a specific schema slug.
	GetPublishedBySlugAndSchema(ctx context.Context, slug string, schemaSlug string) (*Entity, error)

	// GetPublishedBySlugAndSchemas is GetPublishedBySlugAndSchema over several
	// schema slugs (a schema and its aliases).
	GetPublishedBySlugAndSchemas(ctx context.Context, slug string, schemaSlugs []string) (*Entity, error)

	// ListPublishedByAttribute retrieves published entities where a specific JSONB attribute matches a value.
	// Returns the matching entities, the total count, and any error.
	ListPublishedByAttribute(ctx context.Context, schemaSlugs []string, attrKey string, attrValue string, limit int, offset int) ([]*Entity, int, error)
}

// PublicPagedResult represents a paginated result for public pages.
type PublicPagedResult struct {
	Items       []*Entity `json:"items"`
	Total       int       `json:"total"`
	Page        int       `json:"page"`
	PerPage     int       `json:"per_page"`
	TotalPages  int       `json:"total_pages"`
	HasNext     bool      `json:"has_next"`
	HasPrevious bool      `json:"has_previous"`
}

// SitemapEntry represents an entry in the sitemap.
type SitemapEntry struct {
	// ID is the entity row id (not serialised); the paths endpoint matches it
	// against the configured homepage.
	ID            uuid.UUID `json:"-"`
	Slug          string    `json:"slug"`
	SchemaSlug    string    `json:"schema_slug"`
	UpdatedAt     string    `json:"updated_at"`
	Priority      float64   `json:"priority"`
	ChangeFreq    string    `json:"change_freq"`
	NoIndex       bool      `json:"no_index"`
	IncludeInLLMs bool      `json:"include_in_llms"`
	// EntityFormat is the value of attributes.format, used by the sitemap
	// generator to route playbook entities with format='online_report' to
	// /reports/{slug} instead of /playbooks/{slug} — reports have their
	// own canonical URL family even though they share the playbook schema.
	// Empty string for entities where attributes.format is unset or not a
	// string. Only playbook-schema entities use this today.
	EntityFormat string `json:"entity_format,omitempty"`
	// ParentSlug is the slug of the entity's parent ("" for roots); it fills
	// {parent} in URL patterns.
	ParentSlug string `json:"parent_slug,omitempty"`
	// AncestorPath is the slash-joined slugs of every ancestor, root first
	// ("" for roots); it fills {path}. ParentSlug is its last segment.
	AncestorPath string `json:"ancestor_path,omitempty"`
	// PublishedAt (created_at when unset) fills {year}, {month} and {day}.
	PublishedAt time.Time `json:"-"`
	// Attrs holds the top-level attributes requested through
	// ListAllPublishedWithAttrs, flattened to strings.
	Attrs map[string]string `json:"-"`
}

// BreadcrumbPathItem represents a single item in an entity's ancestor chain.
type BreadcrumbPathItem struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
	Slug string    `json:"slug"`
}

// PostgresPublicRepository implements PublicRepository using PostgreSQL.
type PostgresPublicRepository struct {
	db *database.DB
}

// NewPostgresPublicRepository creates a new public repository.
func NewPostgresPublicRepository(db *database.DB) *PostgresPublicRepository {
	return &PostgresPublicRepository{db: db}
}

// GetPublishedBySlug retrieves a published entity by slug.
func (r *PostgresPublicRepository) GetPublishedBySlug(ctx context.Context, slug string) (*Entity, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire connection: %w", err)
	}
	defer conn.Release()

	query := `
		SELECT
			e.id, e.schema_id, e.name, e.slug, e.locale, e.status, e.attributes, e.metadata,
			e.version, e.created_by, e.updated_by, e.created_at, e.updated_at, e.published_at,
			COALESCE(e.editorial, '{}'::jsonb),
			e.parent_id,
			s.id, s.name, s.slug, s.description, s.schema, s.is_builtin, s.created_at, s.updated_at, s.settings
		FROM hatamoto_entities e
		JOIN hatamoto_entity_schemas s ON e.schema_id = s.id
		WHERE e.slug = $1 AND e.status = 'published' AND e.variant_key IS NULL AND ` + HiddenPersonFor("$2") + `
		ORDER BY ` + scopedLocaleFirstSQL("$2") + `, e.created_at, e.id
		LIMIT 1
	`

	var e Entity
	var s EntitySchema

	err = conn.QueryRow(ctx, query, slug, VisibleLocales(ctx)).Scan(
		&e.ID, &e.SchemaID, &e.Name, &e.Slug, &e.Locale, &e.Status, &e.Attributes, &e.Metadata,
		&e.Version, &e.CreatedBy, &e.UpdatedBy, &e.CreatedAt, &e.UpdatedAt, &e.PublishedAt,
		&e.Editorial,
		&e.ParentID,
		&s.ID, &s.Name, &s.Slug, &s.Description, &s.Schema, &s.IsBuiltin, &s.CreatedAt, &s.UpdatedAt, &s.Settings,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrEntityNotFound
		}
		return nil, fmt.Errorf("failed to get published entity by slug: %w", err)
	}

	e.Schema = &s
	return &e, nil
}

// GetPublishedByID retrieves a published entity by ID.
func (r *PostgresPublicRepository) GetPublishedByID(ctx context.Context, entityID uuid.UUID) (*Entity, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire connection: %w", err)
	}
	defer conn.Release()

	query := `
		SELECT
			e.id, e.schema_id, e.name, e.slug, e.locale, e.status, e.attributes, e.metadata,
			e.version, e.created_by, e.updated_by, e.created_at, e.updated_at, e.published_at,
			COALESCE(e.editorial, '{}'::jsonb),
			e.parent_id,
			s.id, s.name, s.slug, s.description, s.schema, s.is_builtin, s.created_at, s.updated_at, s.settings
		FROM hatamoto_entities e
		JOIN hatamoto_entity_schemas s ON e.schema_id = s.id
		WHERE e.id = $1 AND e.status = 'published' AND ` + HiddenPersonFor("$2") + `
	`

	var e Entity
	var s EntitySchema

	err = conn.QueryRow(ctx, query, entityID, VisibleLocales(ctx)).Scan(
		&e.ID, &e.SchemaID, &e.Name, &e.Slug, &e.Locale, &e.Status, &e.Attributes, &e.Metadata,
		&e.Version, &e.CreatedBy, &e.UpdatedBy, &e.CreatedAt, &e.UpdatedAt, &e.PublishedAt,
		&e.Editorial,
		&e.ParentID,
		&s.ID, &s.Name, &s.Slug, &s.Description, &s.Schema, &s.IsBuiltin, &s.CreatedAt, &s.UpdatedAt, &s.Settings,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrEntityNotFound
		}
		return nil, fmt.Errorf("failed to get published entity by ID: %w", err)
	}

	e.Schema = &s
	return &e, nil
}

// GetPublishedByIDs retrieves multiple published entities by their IDs.
func (r *PostgresPublicRepository) GetPublishedByIDs(ctx context.Context, ids []uuid.UUID) ([]*Entity, error) {
	if len(ids) == 0 {
		return []*Entity{}, nil
	}

	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire connection: %w", err)
	}
	defer conn.Release()

	query := `
		SELECT
			e.id, e.schema_id, e.name, e.slug, e.locale, e.status, e.attributes, e.metadata,
			e.version, e.created_by, e.updated_by, e.created_at, e.updated_at, e.published_at,
			COALESCE(e.editorial, '{}'::jsonb),
			e.parent_id,
			s.id, s.name, s.slug, s.description, s.schema, s.is_builtin, s.created_at, s.updated_at, s.settings
		FROM hatamoto_entities e
		JOIN hatamoto_entity_schemas s ON e.schema_id = s.id
		WHERE e.id = ANY($1) AND e.status = 'published' AND ` + HiddenPersonFor("$2") + `
	`

	rows, err := conn.Query(ctx, query, ids, VisibleLocales(ctx))
	if err != nil {
		return nil, fmt.Errorf("failed to get published entities by IDs: %w", err)
	}
	defer rows.Close()

	var entities []*Entity
	for rows.Next() {
		var e Entity
		var s EntitySchema

		if err := rows.Scan(
			&e.ID, &e.SchemaID, &e.Name, &e.Slug, &e.Locale, &e.Status, &e.Attributes, &e.Metadata,
			&e.Version, &e.CreatedBy, &e.UpdatedBy, &e.CreatedAt, &e.UpdatedAt, &e.PublishedAt,
			&e.Editorial,
			&e.ParentID,
			&s.ID, &s.Name, &s.Slug, &s.Description, &s.Schema, &s.IsBuiltin, &s.CreatedAt, &s.UpdatedAt, &s.Settings,
		); err != nil {
			return nil, fmt.Errorf("failed to scan entity: %w", err)
		}
		e.Schema = &s
		entities = append(entities, &e)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	if entities == nil {
		entities = []*Entity{}
	}

	return entities, nil
}

// ListPublished retrieves paginated published base entities by schema.
func (r *PostgresPublicRepository) ListPublished(ctx context.Context, schemaSlug string, page, perPage int) (*PublicPagedResult, error) {
	return r.ListPublishedBySchemas(ctx, []string{schemaSlug}, page, perPage)
}

// ListPublishedBySchemas retrieves paginated published base entities (variant
// rows are plugin-owned and never listed) whose schema slug is in schemaSlugs.
func (r *PostgresPublicRepository) ListPublishedBySchemas(ctx context.Context, schemaSlugs []string, page, perPage int) (*PublicPagedResult, error) {
	return r.listPublishedBySchemas(ctx, schemaSlugs, page, perPage, true)
}

// ListPublishedBySchemasNoCount is the first page of ListPublishedBySchemas
// without the COUNT(*) query, for callers that only need the rows.
func (r *PostgresPublicRepository) ListPublishedBySchemasNoCount(ctx context.Context, schemaSlugs []string, limit int) ([]*Entity, error) {
	res, err := r.listPublishedBySchemas(ctx, schemaSlugs, 1, limit, false)
	if err != nil {
		return nil, err
	}
	return res.Items, nil
}

func (r *PostgresPublicRepository) listPublishedBySchemas(ctx context.Context, schemaSlugs []string, page, perPage int, withCount bool) (*PublicPagedResult, error) {
	return r.listPublishedFiltered(ctx, schemaSlugs, "", nil, page, perPage, withCount)
}

// listPublishedFiltered is listPublishedBySchemas with an extra SQL condition.
// cond is a fixed fragment (never user text) using $2.. for condArgs; $1 is the
// schema slug list. An empty cond adds nothing.
func (r *PostgresPublicRepository) listPublishedFiltered(ctx context.Context, schemaSlugs []string, cond string, condArgs []any, page, perPage int, withCount bool) (*PublicPagedResult, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire connection: %w", err)
	}
	defer conn.Release()

	// Validate pagination
	if page < 1 {
		page = 1
	}
	if perPage < 1 || perPage > 100 {
		perPage = 10
	}
	offset := (page - 1) * perPage

	// Count total published entities for this schema
	countQuery := `
		SELECT COUNT(*)
		FROM hatamoto_entities e
		JOIN hatamoto_entity_schemas s ON e.schema_id = s.id
		WHERE s.slug = ANY($1) AND e.status = 'published' AND e.variant_key IS NULL AND ` + HiddenPersonFor("$"+strconv.Itoa(len(condArgs)+2)) + cond + `
	`
	var total int
	if withCount {
		if err := conn.QueryRow(ctx, countQuery, append(append([]any{schemaSlugs}, condArgs...), VisibleLocales(ctx))...).Scan(&total); err != nil {
			return nil, fmt.Errorf("failed to count published entities: %w", err)
		}
	}

	// Fetch paginated entities
	query := `
		SELECT
			e.id, e.schema_id, e.name, e.slug, e.locale, e.status, e.attributes, e.metadata,
			e.version, e.created_by, e.updated_by, e.created_at, e.updated_at, e.published_at,
			COALESCE(e.editorial, '{}'::jsonb),
			e.parent_id,
			s.id, s.name, s.slug, s.description, s.schema, s.is_builtin, s.created_at, s.updated_at, s.settings
		FROM hatamoto_entities e
		JOIN hatamoto_entity_schemas s ON e.schema_id = s.id
		WHERE s.slug = ANY($1) AND e.status = 'published' AND e.variant_key IS NULL AND ` + HiddenPersonFor("$"+strconv.Itoa(len(condArgs)+4)) + cond + `
		ORDER BY e.published_at DESC NULLS LAST, e.created_at DESC, e.id
		LIMIT $` + strconv.Itoa(len(condArgs)+2) + ` OFFSET $` + strconv.Itoa(len(condArgs)+3) + `
	`

	rows, err := conn.Query(ctx, query, append(append([]any{schemaSlugs}, condArgs...), perPage, offset, VisibleLocales(ctx))...)
	if err != nil {
		return nil, fmt.Errorf("failed to list published entities: %w", err)
	}
	defer rows.Close()

	var entities []*Entity
	for rows.Next() {
		var e Entity
		var s EntitySchema

		if err := rows.Scan(
			&e.ID, &e.SchemaID, &e.Name, &e.Slug, &e.Locale, &e.Status, &e.Attributes, &e.Metadata,
			&e.Version, &e.CreatedBy, &e.UpdatedBy, &e.CreatedAt, &e.UpdatedAt, &e.PublishedAt,
			&e.Editorial,
			&e.ParentID,
			&s.ID, &s.Name, &s.Slug, &s.Description, &s.Schema, &s.IsBuiltin, &s.CreatedAt, &s.UpdatedAt, &s.Settings,
		); err != nil {
			return nil, fmt.Errorf("failed to scan entity: %w", err)
		}
		e.Schema = &s
		entities = append(entities, &e)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Ensure empty array instead of null
	if entities == nil {
		entities = []*Entity{}
	}

	totalPages := max((total+perPage-1)/perPage, 1)

	return &PublicPagedResult{
		Items:       entities,
		Total:       total,
		Page:        page,
		PerPage:     perPage,
		TotalPages:  totalPages,
		HasNext:     page < totalPages,
		HasPrevious: page > 1,
	}, nil
}

// SeriesRow is one post of an editorial series, identity and timing only.
type SeriesRow struct {
	ID, Name, Slug string
	Status         EntityStatus
	Attributes     []byte
	PublishedAt    *time.Time
}

// ListSeriesRows returns the published and scheduled posts of schemaSlugs with
// attributes.series_slug = seriesSlug, oldest first. Scheduled parts are included
// so a series index can show what is coming; callers must not link them.
func (r *PostgresPublicRepository) ListSeriesRows(ctx context.Context, schemaSlugs []string, seriesSlug string) ([]SeriesRow, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire connection: %w", err)
	}
	defer conn.Release()
	rows, err := conn.Query(ctx, `
		SELECT e.id::text, e.name, e.slug, e.status, e.attributes, e.published_at
		  FROM hatamoto_entities e
		  JOIN hatamoto_entity_schemas s ON s.id = e.schema_id
		 WHERE e.attributes->>'series_slug' = $1
		   AND s.slug = ANY($2)
		   AND e.status IN ('published', 'scheduled')
		   AND e.variant_key IS NULL
		   AND `+HiddenPersonFor("$3")+`
		 ORDER BY e.published_at, e.id
	`, seriesSlug, schemaSlugs, VisibleLocales(ctx))
	if err != nil {
		return nil, fmt.Errorf("failed to list series parts: %w", err)
	}
	defer rows.Close()
	var out []SeriesRow
	for rows.Next() {
		var sr SeriesRow
		if err := rows.Scan(&sr.ID, &sr.Name, &sr.Slug, &sr.Status, &sr.Attributes, &sr.PublishedAt); err != nil {
			return nil, fmt.Errorf("failed to scan series part: %w", err)
		}
		out = append(out, sr)
	}
	return out, rows.Err()
}

// ListPublishedByAuthor lists published base entities of schemaSlugs written by
// a team member, newest first, with the total. The match:
// author_entity_slug equals the slug; else an author object whose slug or name matches; else (no author
// object) an author string or author_name equal to the member's name.
func (r *PostgresPublicRepository) ListPublishedByAuthor(ctx context.Context, schemaSlugs []string, memberSlug, memberName string, page, perPage int) (*PublicPagedResult, error) {
	const cond = ` AND (
		e.attributes->>'author_entity_slug' = $2
		OR (jsonb_typeof(e.attributes->'author') = 'object' AND (e.attributes->'author'->>'slug' = $2 OR e.attributes->'author'->>'name' = $3))
		OR (COALESCE(jsonb_typeof(e.attributes->'author'), 'null') <> 'object' AND (
			(jsonb_typeof(e.attributes->'author') = 'string' AND e.attributes->>'author' = $3)
			OR e.attributes->>'author_name' = $3)))`
	return r.listPublishedFiltered(ctx, schemaSlugs, cond, []any{memberSlug, memberName}, page, perPage, true)
}

// ListAllPublished retrieves all published entities for sitemap generation.
func (r *PostgresPublicRepository) ListAllPublished(ctx context.Context) ([]*SitemapEntry, error) {
	return r.ListAllPublishedWithAttrs(ctx, nil)
}

// ListAllPublishedWithAttrs implements PublicRepository.
func (r *PostgresPublicRepository) ListAllPublishedWithAttrs(ctx context.Context, attrKeys []string) ([]*SitemapEntry, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire connection: %w", err)
	}
	defer conn.Release()

	// Check both metadata->>'no_index' (legacy) and attributes->'seo'->>'robots'
	// containing 'noindex' (set by the SEO Schema Tab) for the most restrictive check.
	// include_in_llms_txt defaults to true when unset — matches the admin SEO tab default.
	//
	// City slug is always empty (no hatamoto_geo_locations table).
	query := `
		SELECT e.id, e.slug, s.slug as schema_slug, e.updated_at,
			` + NoIndexSQL + `,
			COALESCE(NULLIF(e.attributes->'seo'->>'include_in_llms_txt', '')::boolean, true),
			COALESCE(e.attributes->>'format', '') AS entity_format,
			COALESCE(p.slug, '') AS parent_slug,
			COALESCE(anc.path, '') AS ancestor_path,
			COALESCE(e.published_at, e.created_at) AS dated_at,
			COALESCE((SELECT jsonb_object_agg(k, e.attributes->k) FROM unnest($1::text[]) k WHERE e.attributes ? k), '{}'::jsonb) AS attrs
		FROM hatamoto_entities e
		JOIN hatamoto_entity_schemas s ON e.schema_id = s.id
		LEFT JOIN hatamoto_entities p ON p.id = e.parent_id
		LEFT JOIN LATERAL (
			WITH RECURSIVE a(slug, parent_id, depth) AS (
				SELECT p.slug, p.parent_id, 1
				UNION ALL
				SELECT q.slug, q.parent_id, a.depth + 1
				FROM hatamoto_entities q JOIN a ON q.id = a.parent_id
				WHERE a.depth < ` + maxAncestorDepthSQL + `
			)
			SELECT string_agg(slug, '/' ORDER BY depth DESC) AS path FROM a
		) anc ON e.parent_id IS NOT NULL
		WHERE e.status = 'published' AND e.variant_key IS NULL AND ` + HiddenPersonFor("$2") + `
		ORDER BY e.updated_at DESC, e.id
	`

	if attrKeys == nil {
		attrKeys = []string{}
	}
	rows, err := conn.Query(ctx, query, attrKeys, VisibleLocales(ctx))
	if err != nil {
		return nil, fmt.Errorf("failed to list published entities for sitemap: %w", err)
	}
	defer rows.Close()

	var entries []*SitemapEntry
	for rows.Next() {
		var e SitemapEntry
		var updatedAt time.Time
		var attrsJSON []byte
		if err := rows.Scan(&e.ID, &e.Slug, &e.SchemaSlug, &updatedAt, &e.NoIndex, &e.IncludeInLLMs, &e.EntityFormat, &e.ParentSlug, &e.AncestorPath, &e.PublishedAt, &attrsJSON); err != nil {
			return nil, fmt.Errorf("failed to scan sitemap entry: %w", err)
		}
		if len(attrKeys) > 0 {
			e.Attrs = FlattenAttributes(attrsJSON)
		}
		e.UpdatedAt = updatedAt.Format("2006-01-02")
		// Set priority and change frequency based on schema type
		switch e.SchemaSlug {
		case "homepage":
			e.Priority = 1.0
			e.ChangeFreq = "daily"
		case "page":
			e.Priority = 0.8
			e.ChangeFreq = "monthly"
		case "blog", "post", "article":
			e.Priority = 0.6
			e.ChangeFreq = "weekly"
		default:
			e.Priority = 0.5
			e.ChangeFreq = "monthly"
		}

		entries = append(entries, &e)
	}

	if entries == nil {
		entries = []*SitemapEntry{}
	}

	return entries, rows.Err()
}

// GetHomepage retrieves the homepage entity.
// Resolution order:
//  1. Explicit homepage_entity_id from tenant settings (if set and entity exists/published)
//  2. Slug convention fallback: "home" → "homepage" → first published page
func (r *PostgresPublicRepository) GetHomepage(ctx context.Context) (*Entity, error) {
	// 1. Check tenant settings for an explicit homepage entity ID
	ent, err := r.getHomepageFromSettings(ctx)
	if err == nil && ent != nil {
		return ent, nil
	}

	// 2. Fall back to slug convention: "home" → "homepage" → first published page
	ent, err = r.GetPublishedBySlug(ctx, "home")
	if err != nil {
		ent, err = r.GetPublishedBySlug(ctx, "homepage")
		if err != nil {
			ent, err = r.getFirstPublishedPage(ctx)
			if err != nil {
				return nil, err
			}
		}
	}

	return ent, nil
}

// getHomepageFromSettings queries the singleton site config for an explicit
// homepage_entity_id and returns the published entity if found.
//
// Reads hatamoto_site_config.homepage_entity_id.
func (r *PostgresPublicRepository) getHomepageFromSettings(ctx context.Context) (*Entity, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire connection: %w", err)
	}
	defer conn.Release()

	var homepageID *uuid.UUID
	query := `SELECT homepage_entity_id FROM hatamoto_site_config WHERE id = TRUE`
	err = conn.QueryRow(ctx, query).Scan(&homepageID)
	if err != nil || homepageID == nil {
		return nil, ErrEntityNotFound
	}

	return r.GetPublishedByID(ctx, *homepageID)
}

// getFirstPublishedPage retrieves the first published page entity for a tenant.
func (r *PostgresPublicRepository) getFirstPublishedPage(ctx context.Context) (*Entity, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire connection: %w", err)
	}
	defer conn.Release()

	query := `
		SELECT
			e.id, e.schema_id, e.name, e.slug, e.locale, e.status, e.attributes, e.metadata,
			e.version, e.created_by, e.updated_by, e.created_at, e.updated_at, e.published_at,
			COALESCE(e.editorial, '{}'::jsonb),
			e.parent_id,
			s.id, s.name, s.slug, s.description, s.schema, s.is_builtin, s.created_at, s.updated_at, s.settings
		FROM hatamoto_entities e
		JOIN hatamoto_entity_schemas s ON e.schema_id = s.id
		WHERE e.status = 'published' AND s.slug = 'page' AND ` + visibleLocaleSQL("$1") + `
		ORDER BY e.created_at ASC
		LIMIT 1
	`

	var e Entity
	var s EntitySchema

	err = conn.QueryRow(ctx, query, VisibleLocales(ctx)).Scan(
		&e.ID, &e.SchemaID, &e.Name, &e.Slug, &e.Locale, &e.Status, &e.Attributes, &e.Metadata,
		&e.Version, &e.CreatedBy, &e.UpdatedBy, &e.CreatedAt, &e.UpdatedAt, &e.PublishedAt,
		&e.Editorial,
		&e.ParentID,
		&s.ID, &s.Name, &s.Slug, &s.Description, &s.Schema, &s.IsBuiltin, &s.CreatedAt, &s.UpdatedAt, &s.Settings,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrEntityNotFound
		}
		return nil, fmt.Errorf("failed to get homepage: %w", err)
	}

	e.Schema = &s
	return &e, nil
}

// maxAncestorDepthSQL bounds every ancestor walk: a parent cycle (which the
// service refuses to write, but SQL can) ends here instead of looping.
const maxAncestorDepthSQL = "16"

// GetAncestorSlugs returns, for each of ids that has a parent, the slugs of
// its ancestors, root first (self excluded), in one recursive query. Ids
// without a parent are absent from the map. The walk is bounded like
// GetEntityPath.
func (r *PostgresPublicRepository) GetAncestorSlugs(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID][]string, error) {
	out := map[uuid.UUID][]string{}
	if len(ids) == 0 {
		return out, nil
	}
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire connection: %w", err)
	}
	defer conn.Release()

	rows, err := conn.Query(ctx, `
		WITH RECURSIVE ancestors AS (
			SELECT id AS origin, parent_id AS anc, 1 AS depth
			FROM hatamoto_entities
			WHERE id = ANY($1) AND parent_id IS NOT NULL
			UNION ALL
			SELECT a.origin, p.parent_id, a.depth + 1
			FROM ancestors a
			JOIN hatamoto_entities p ON p.id = a.anc
			WHERE p.parent_id IS NOT NULL AND a.depth < `+maxAncestorDepthSQL+`
		)
		SELECT a.origin, p.slug
		FROM ancestors a JOIN hatamoto_entities p ON p.id = a.anc
		ORDER BY a.origin, a.depth DESC`, ids)
	if err != nil {
		return nil, fmt.Errorf("failed to get ancestors: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var slug string
		if err := rows.Scan(&id, &slug); err != nil {
			return nil, fmt.Errorf("failed to scan ancestor: %w", err)
		}
		out[id] = append(out[id], slug)
	}
	return out, rows.Err()
}

// GetEntityPath returns the ancestor chain for an entity using a recursive CTE.
// The result is ordered from root to the entity itself (e.g., [Home, Services, SEO, Enterprise SEO]).
func (r *PostgresPublicRepository) GetEntityPath(ctx context.Context, entityID uuid.UUID) ([]BreadcrumbPathItem, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire connection: %w", err)
	}
	defer conn.Release()

	query := `
		WITH RECURSIVE ancestors AS (
			SELECT id, name, slug, parent_id, 0 AS depth
			FROM hatamoto_entities
			WHERE id = $1
			UNION ALL
			SELECT e.id, e.name, e.slug, e.parent_id, a.depth + 1
			FROM hatamoto_entities e
			JOIN ancestors a ON a.parent_id = e.id
			WHERE a.depth < ` + maxAncestorDepthSQL + `
		)
		SELECT id, name, slug FROM ancestors
		ORDER BY depth DESC
	`

	rows, err := conn.Query(ctx, query, entityID)
	if err != nil {
		return nil, fmt.Errorf("failed to get entity path: %w", err)
	}
	defer rows.Close()

	var path []BreadcrumbPathItem
	for rows.Next() {
		var item BreadcrumbPathItem
		if err := rows.Scan(&item.ID, &item.Name, &item.Slug); err != nil {
			return nil, fmt.Errorf("failed to scan breadcrumb item: %w", err)
		}
		path = append(path, item)
	}

	if path == nil {
		path = []BreadcrumbPathItem{}
	}

	return path, rows.Err()
}

// GetPublishedByPath retrieves the published base entity of a schema in
// schemaSlugs that sits at the end of a parent chain: segments are slugs, root
// first, the last being the entity's own. Ancestors may be of any schema and
// status (a draft region page still gives its children their URL).
// Slugs are only unique among siblings, so the chain is walked from the root.
func (r *PostgresPublicRepository) GetPublishedByPath(ctx context.Context, segments []string, schemaSlugs []string) (*Entity, error) {
	if len(segments) == 0 || len(segments) > urlpattern.MaxPathDepth {
		return nil, ErrEntityNotFound
	}
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire connection: %w", err)
	}
	defer conn.Release()

	var id uuid.UUID
	err = conn.QueryRow(ctx, `
		WITH RECURSIVE chain(id, depth) AS (
			SELECT e.id, 1 FROM hatamoto_entities e
			WHERE e.parent_id IS NULL AND e.variant_key IS NULL AND e.slug = ($1::text[])[1]
			UNION ALL
			SELECT e.id, c.depth + 1 FROM hatamoto_entities e JOIN chain c ON e.parent_id = c.id
			WHERE c.depth < cardinality($1::text[]) AND e.variant_key IS NULL AND e.slug = ($1::text[])[c.depth + 1]
		)
		SELECT e.id FROM chain c
		JOIN hatamoto_entities e ON e.id = c.id
		JOIN hatamoto_entity_schemas s ON s.id = e.schema_id
		WHERE c.depth = cardinality($1::text[]) AND e.status = 'published' AND s.slug = ANY($2) AND `+HiddenPersonFor("$3")+`
		ORDER BY `+scopedLocaleFirstSQL("$3")+`, e.created_at, e.id LIMIT 1`, segments, schemaSlugs, VisibleLocales(ctx)).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrEntityNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to resolve entity path: %w", err)
	}
	return r.GetPublishedByID(ctx, id)
}

// GetPublishedBySlugAndSchema retrieves a published base entity by slug, restricted to a specific schema slug.
func (r *PostgresPublicRepository) GetPublishedBySlugAndSchema(ctx context.Context, slug string, schemaSlug string) (*Entity, error) {
	return r.GetPublishedBySlugAndSchemas(ctx, slug, []string{schemaSlug})
}

// GetPublishedBySlugAndSchemas retrieves a published base entity by slug whose
// schema slug is in schemaSlugs. Deterministic when several rows share a slug.
func (r *PostgresPublicRepository) GetPublishedBySlugAndSchemas(ctx context.Context, slug string, schemaSlugs []string) (*Entity, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire connection: %w", err)
	}
	defer conn.Release()

	query := `
		SELECT
			e.id, e.schema_id, e.name, e.slug, e.locale, e.status, e.attributes, e.metadata,
			e.version, e.created_by, e.updated_by, e.created_at, e.updated_at, e.published_at,
			COALESCE(e.editorial, '{}'::jsonb),
			e.parent_id,
			s.id, s.name, s.slug, s.description, s.schema, s.is_builtin, s.created_at, s.updated_at, s.settings
		FROM hatamoto_entities e
		JOIN hatamoto_entity_schemas s ON e.schema_id = s.id
		WHERE e.slug = $1 AND s.slug = ANY($2) AND e.status = 'published' AND e.variant_key IS NULL AND ` + HiddenPersonFor("$3") + `
		ORDER BY ` + scopedLocaleFirstSQL("$3") + `, e.created_at, e.id
		LIMIT 1
	`

	var e Entity
	var s EntitySchema

	err = conn.QueryRow(ctx, query, slug, schemaSlugs, VisibleLocales(ctx)).Scan(
		&e.ID, &e.SchemaID, &e.Name, &e.Slug, &e.Locale, &e.Status, &e.Attributes, &e.Metadata,
		&e.Version, &e.CreatedBy, &e.UpdatedBy, &e.CreatedAt, &e.UpdatedAt, &e.PublishedAt,
		&e.Editorial,
		&e.ParentID,
		&s.ID, &s.Name, &s.Slug, &s.Description, &s.Schema, &s.IsBuiltin, &s.CreatedAt, &s.UpdatedAt, &s.Settings,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrEntityNotFound
		}
		return nil, fmt.Errorf("failed to get published entity by slug and schema: %w", err)
	}

	e.Schema = &s
	return &e, nil
}

// allowedAttributeKeys defines the set of JSONB attribute keys that may be
// queried via ListPublishedByAttribute. This prevents callers from probing
// arbitrary attributes through the public API.
var allowedAttributeKeys = map[string]bool{
	"author_entity_id": true,
}

// ListPublishedByAttribute retrieves published entities where a JSONB attribute key matches the given value.
// Entities are filtered to the given schema slugs, ordered by published_at DESC, and limited to `limit` results.
// Only attribute keys in allowedAttributeKeys are permitted for security.
func (r *PostgresPublicRepository) ListPublishedByAttribute(ctx context.Context, schemaSlugs []string, attrKey string, attrValue string, limit int, offset int) ([]*Entity, int, error) {
	if !allowedAttributeKeys[attrKey] {
		return nil, 0, fmt.Errorf("attribute key %q is not allowed for public queries", attrKey)
	}
	if len(schemaSlugs) == 0 {
		return []*Entity{}, 0, nil
	}
	if limit < 1 || limit > 500 {
		limit = 100
	}

	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to acquire connection: %w", err)
	}
	defer conn.Release()

	// Count total matching entities
	countQuery := `
		SELECT COUNT(*)
		FROM hatamoto_entities e
		JOIN hatamoto_entity_schemas s ON e.schema_id = s.id
		WHERE s.slug = ANY($1) AND e.status = 'published' AND e.attributes->>$2 = $3 AND ` + HiddenPersonFor("$4") + `
	`
	var total int
	if err := conn.QueryRow(ctx, countQuery, schemaSlugs, attrKey, attrValue, VisibleLocales(ctx)).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("failed to count entities by attribute: %w", err)
	}

	// Fetch matching entities
	query := `
		SELECT
			e.id, e.schema_id, e.name, e.slug, e.locale, e.status, e.attributes, e.metadata,
			e.version, e.created_by, e.updated_by, e.created_at, e.updated_at, e.published_at,
			COALESCE(e.editorial, '{}'::jsonb),
			e.parent_id,
			s.id, s.name, s.slug, s.description, s.schema, s.is_builtin, s.created_at, s.updated_at, s.settings
		FROM hatamoto_entities e
		JOIN hatamoto_entity_schemas s ON e.schema_id = s.id
		WHERE s.slug = ANY($1) AND e.status = 'published' AND e.attributes->>$2 = $3 AND ` + HiddenPersonFor("$6") + `
		ORDER BY e.published_at DESC NULLS LAST, e.created_at DESC
		LIMIT $4 OFFSET $5
	`

	if offset < 0 {
		offset = 0
	}

	rows, err := conn.Query(ctx, query, schemaSlugs, attrKey, attrValue, limit, offset, VisibleLocales(ctx))
	if err != nil {
		return nil, 0, fmt.Errorf("failed to list entities by attribute: %w", err)
	}
	defer rows.Close()

	var entities []*Entity
	for rows.Next() {
		var e Entity
		var s EntitySchema

		if err := rows.Scan(
			&e.ID, &e.SchemaID, &e.Name, &e.Slug, &e.Locale, &e.Status, &e.Attributes, &e.Metadata,
			&e.Version, &e.CreatedBy, &e.UpdatedBy, &e.CreatedAt, &e.UpdatedAt, &e.PublishedAt,
			&e.Editorial,
			&e.ParentID,
			&s.ID, &s.Name, &s.Slug, &s.Description, &s.Schema, &s.IsBuiltin, &s.CreatedAt, &s.UpdatedAt, &s.Settings,
		); err != nil {
			return nil, 0, fmt.Errorf("failed to scan entity: %w", err)
		}
		e.Schema = &s
		entities = append(entities, &e)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	if entities == nil {
		entities = []*Entity{}
	}

	return entities, total, nil
}

// GenerateJSONLD generates JSON-LD structured data for an entity.
// It first attempts the schema-driven generator (using x-schema-org-* annotations).
// If the schema lacks annotations, it falls back to the legacy switch-based generator.
// Supports: WebPage, Article, LocalBusiness, FAQPage, Service, Person, Organization, Product.
func GenerateJSONLD(entity *Entity, baseURL string) (map[string]any, error) {
	doc, err := generateBaseJSONLD(entity, baseURL)
	if err == nil && doc != nil {
		// Once per call, success path only: plugins see the final document.
		if enrich := currentJSONLDEnricher(); enrich != nil {
			enrich(entity, baseURL, doc)
		}
	}
	return doc, err
}

func generateBaseJSONLD(entity *Entity, baseURL string) (map[string]any, error) {
	if entity == nil || entity.Schema == nil {
		return nil, errors.New("entity or schema is nil")
	}

	// Precedence: plugin builder > x-schema-org annotations >
	// settings.schema_org_type > legacy switch (Thing for unknown slugs).
	// A person always uses the built-in person mapping (public_email, bio
	// description, sameAs), never the annotation-driven one.
	if _, hasBuilder := LookupJSONLDBuilder(entity.Schema.Slug); !hasBuilder && entity.Schema.Slug != personSchemaSlug {
		if hasSchemaOrgAnnotations(entity.Schema.Schema) {
			if result := GenerateSchemaDrivernJSONLD(entity, baseURL); result != nil {
				// Enrich with editorial provenance if present
				EnrichJSONLDWithEditorial(result, entity.Editorial)
				return result, nil
			}
		}
		if st, err := entity.Schema.URLSettings(); err == nil && IsAllowedSchemaOrgType(st.SchemaOrgType) {
			result := buildSettingsTypedJSONLD(entity, st.SchemaOrgType, baseURL)
			EnrichJSONLDWithEditorial(result, entity.Editorial)
			return result, nil
		}
	}

	// Fallback: legacy switch-based generator for schemas without annotations
	result, err := generateLegacyJSONLD(entity, baseURL)
	if err == nil && result != nil {
		EnrichJSONLDWithEditorial(result, entity.Editorial)
	}
	return result, err
}

// generateLegacyJSONLD is the original hardcoded switch-based JSON-LD generator.
// It is used as a fallback for schemas that lack x-schema-org-* annotations.
func generateLegacyJSONLD(entity *Entity, baseURL string) (map[string]any, error) {
	// Parse attributes
	var attrs map[string]any
	if err := json.Unmarshal(entity.Attributes, &attrs); err != nil {
		attrs = make(map[string]any)
	}

	// Base JSON-LD structure
	jsonLD := map[string]any{
		"@context": "https://schema.org",
	}

	// A registered plugin builder wins over the built-in mappings.
	schemaSlug := entity.Schema.Slug
	if fn, ok := LookupJSONLDBuilder(schemaSlug); ok {
		fn(jsonLD, entity, attrs, baseURL)
		return jsonLD, nil
	}

	// Map schema slug to Schema.org types
	switch schemaSlug {
	case "page":
		buildWebPageJSONLD(jsonLD, entity, attrs, baseURL)

	case "blog", "post", "article", "blog_post":
		buildArticleJSONLD(jsonLD, entity, attrs, baseURL)

	case "team_member", "person":
		buildPersonJSONLD(jsonLD, entity, attrs, baseURL)

	case "organization", "company":
		buildOrganizationJSONLD(jsonLD, entity, attrs, baseURL)

	case "product":
		buildProductJSONLD(jsonLD, entity, attrs, baseURL)

	default:
		// Generic Thing type
		jsonLD["@type"] = "Thing"
		jsonLD["name"] = entity.Name
		jsonLD["url"] = BuildEntityURLForJSONLD(entity, baseURL)
		if description, ok := attrs["description"].(string); ok {
			jsonLD["description"] = description
		}
	}

	return jsonLD, nil
}

// buildWebPageJSONLD builds JSON-LD for WebPage schema type.
func buildWebPageJSONLD(jsonLD map[string]any, entity *Entity, attrs map[string]any, baseURL string) {
	jsonLD["@type"] = "WebPage"
	jsonLD["name"] = entity.Name
	// The page's own canonical URL comes from the URL registry; an arbitrary
	// "url" attribute is content, not the page's address.
	pageURL := BuildEntityURLForJSONLD(entity, baseURL)
	jsonLD["url"] = pageURL
	jsonLD["@id"] = pageURL + "#webpage"
	if description, ok := attrs["description"].(string); ok {
		jsonLD["description"] = description
	}
	if metaDescription, ok := attrs["meta_description"].(string); ok {
		jsonLD["description"] = metaDescription
	}
}

// buildArticleJSONLD builds JSON-LD for Article schema type.
//
// Posts render without a structured author byline on the plugin-free base
// (decisions.md (g3) -- the team-member entity type this used to resolve
// against is stripped). Falls back to a plain string Person when the entity
// has an `author` attribute set.
func buildArticleJSONLD(jsonLD map[string]any, entity *Entity, attrs map[string]any, baseURL string) {
	articleURL := BuildEntityURLForJSONLD(entity, baseURL)

	jsonLD["@type"] = "Article"
	jsonLD["headline"] = entity.Name
	jsonLD["url"] = articleURL
	jsonLD["@id"] = articleURL + "#article"

	// mainEntityOfPage reference
	jsonLD["mainEntityOfPage"] = map[string]any{
		"@type": "WebPage",
		"@id":   articleURL + "#webpage",
	}

	// Publisher reference to organization
	jsonLD["publisher"] = map[string]any{
		"@id": baseURL + "#organization",
	}

	// isPartOf reference to website
	jsonLD["isPartOf"] = map[string]any{
		"@id": baseURL + "#website",
	}

	if description, ok := attrs["excerpt"].(string); ok {
		jsonLD["description"] = description
	} else if description, ok := attrs["meta_description"].(string); ok {
		jsonLD["description"] = description
	}

	if publishedAt := entity.PublishedAt; publishedAt != nil {
		jsonLD["datePublished"] = publishedAt.Format("2006-01-02T15:04:05Z07:00")
	}
	jsonLD["dateModified"] = entity.UpdatedAt.Format("2006-01-02T15:04:05Z07:00")

	// Plain string author only -- no structured author-entity byline on
	// the plugin-free base (decisions.md (g3)). Fall back to author_name
	// (the plain Person name the TeamMemberAuthorPicker writes alongside
	// author_entity_id/author_entity_slug) when author is empty, so posts
	// keep an author in JSON-LD even though the entity-linked byline was
	// dropped from the render.
	authorName, ok := attrs["author"].(string)
	if !ok || authorName == "" {
		authorName, ok = attrs["author_name"].(string)
	}
	if ok && authorName != "" {
		jsonLD["author"] = map[string]any{
			"@type": "Person",
			"name":  authorName,
		}
	}

	if featuredImage, ok := attrs["featured_image"].(string); ok && featuredImage != "" {
		jsonLD["image"] = featuredImage
	} else if image, ok := attrs["image"].(string); ok && image != "" {
		jsonLD["image"] = image
	}

	// Add article body if available
	if wordCount, ok := attrs["word_count"].(float64); ok && wordCount > 0 {
		jsonLD["wordCount"] = int(wordCount)
	}

	// Add keywords/tags if available
	if tags, ok := attrs["tags"].([]any); ok && len(tags) > 0 {
		keywords := make([]string, 0, len(tags))
		for _, tag := range tags {
			if tagStr, ok := tag.(string); ok {
				keywords = append(keywords, tagStr)
			} else if tagMap, ok := tag.(map[string]any); ok {
				if name, ok := tagMap["name"].(string); ok {
					keywords = append(keywords, name)
				}
			}
		}
		if len(keywords) > 0 {
			jsonLD["keywords"] = keywords
		}
	}
}

// buildPersonJSONLD builds JSON-LD for Person schema type.
func buildPersonJSONLD(jsonLD map[string]any, entity *Entity, attrs map[string]any, baseURL string) {
	jsonLD["@type"] = "Person"
	jsonLD["name"] = entity.Name

	// URL comes from the schema URL pattern.
	personURL := buildEntityURLForJSONLD(entity, baseURL)
	jsonLD["url"] = personURL
	jsonLD["@id"] = personURL + "#person"

	// Job title
	if jobTitle, ok := attrs["job_title"].(string); ok && jobTitle != "" {
		jsonLD["jobTitle"] = jobTitle
	} else if title, ok := attrs["title"].(string); ok && title != "" {
		jsonLD["jobTitle"] = title
	} else if role, ok := attrs["role"].(string); ok && role != "" {
		jsonLD["jobTitle"] = role
	}

	// Description/Bio. A person gets plain text (short_bio, else the bio with
	// its tags stripped, at most 300 runes); other types keep the raw value.
	if entity.Schema != nil && entity.Schema.Slug == personSchemaSlug {
		shortBio, _ := attrs["short_bio"].(string)
		bio, _ := attrs["bio"].(string)
		if d := PersonDescription(shortBio, bio); d != "" {
			jsonLD["description"] = d
		} else if description, ok := attrs["description"].(string); ok && description != "" {
			jsonLD["description"] = PlainText(description, personDescriptionMax)
		}
	} else if bio, ok := attrs["bio"].(string); ok && bio != "" {
		jsonLD["description"] = bio
	} else if description, ok := attrs["description"].(string); ok {
		jsonLD["description"] = description
	}

	// Image/Photo
	if photo, ok := attrs["photo"].(string); ok && photo != "" {
		jsonLD["image"] = photo
	} else if image, ok := attrs["image"].(string); ok && image != "" {
		jsonLD["image"] = image
	} else if headshot, ok := attrs["headshot"].(string); ok && headshot != "" {
		jsonLD["image"] = headshot
	}

	// Email. A person publishes only the address they chose to make public;
	// the other types keep their email attribute.
	emailKey := "email"
	if entity.Schema != nil && entity.Schema.Slug == personSchemaSlug {
		emailKey = "public_email"
	}
	if email, ok := attrs[emailKey].(string); ok && email != "" {
		jsonLD["email"] = email
	}

	// Phone
	if phone, ok := attrs["phone"].(string); ok && phone != "" {
		jsonLD["telephone"] = phone
	}

	// Social profiles (sameAs) - collect from multiple possible field locations
	sameAs := []string{}
	// Top-level social fields
	socialFields := []string{"linkedin", "twitter", "x", "github", "facebook", "instagram", "website"}
	for _, field := range socialFields {
		if url, ok := attrs[field].(string); ok && url != "" {
			sameAs = append(sameAs, url)
		}
	}
	// Dedicated URL fields (linkedin_url, twitter_url, github_url)
	urlFields := []string{"linkedin_url", "twitter_url", "github_url"}
	for _, field := range urlFields {
		if url, ok := attrs[field].(string); ok && url != "" {
			// Avoid duplicates
			isDuplicate := slices.Contains(sameAs, url)
			if !isDuplicate {
				sameAs = append(sameAs, url)
			}
		}
	}
	// Nested contact object fields (contact.linkedin, contact.twitter)
	if contact, ok := attrs["contact"].(map[string]any); ok {
		contactSocialFields := []string{"linkedin", "twitter"}
		for _, field := range contactSocialFields {
			if url, ok := contact[field].(string); ok && url != "" {
				isDuplicate := slices.Contains(sameAs, url)
				if !isDuplicate {
					sameAs = append(sameAs, url)
				}
			}
		}
	}
	if len(sameAs) > 0 {
		jsonLD["sameAs"] = sameAs
	}

	// Personal website / portfolio URL — overrides the entity URL when set,
	// since the entity URL is the team page on this site. The personal site
	// is a stronger sameAs/canonical signal.
	if personalURL, ok := attrs["url"].(string); ok && personalURL != "" {
		// Add to sameAs instead of overriding the canonical /team/{slug} URL
		isDuplicate := slices.Contains(sameAs, personalURL)
		if !isDuplicate {
			sameAs = append(sameAs, personalURL)
			jsonLD["sameAs"] = sameAs
		}
	}

	// Works for organization. If the person has an explicit `company`
	// attribute, surface it as a named Organization (third-party employer
	// data — e.g. a guest author's day-job). Otherwise reference the site
	// organization via @id (the default — team members work for us).
	if company, ok := attrs["company"].(string); ok && company != "" {
		jsonLD["worksFor"] = map[string]any{
			"@type": "Organization",
			"name":  company,
		}
	} else {
		jsonLD["worksFor"] = map[string]any{
			"@type": "Organization",
			"@id":   baseURL + "#organization",
		}
	}

	// Areas of expertise (knowsAbout)
	if skills, ok := attrs["skills"].([]any); ok && len(skills) > 0 {
		skillStrings := make([]string, 0, len(skills))
		for _, skill := range skills {
			if s, ok := skill.(string); ok {
				skillStrings = append(skillStrings, s)
			}
		}
		if len(skillStrings) > 0 {
			jsonLD["knowsAbout"] = skillStrings
		}
	} else if expertise, ok := attrs["expertise"].([]any); ok && len(expertise) > 0 {
		expertiseStrings := make([]string, 0, len(expertise))
		for _, e := range expertise {
			if s, ok := e.(string); ok {
				expertiseStrings = append(expertiseStrings, s)
			}
		}
		if len(expertiseStrings) > 0 {
			jsonLD["knowsAbout"] = expertiseStrings
		}
	}

	// subjectOf: aggregate press mentions, media appearances, books, publications
	// Each becomes a CreativeWork or subtype that has the person as subject.
	subjectOf := []map[string]any{}

	if pressMentions, ok := attrs["press_mentions"].([]any); ok {
		for _, m := range pressMentions {
			mention, ok := m.(map[string]any)
			if !ok {
				continue
			}
			outlet, _ := mention["outlet"].(string)
			url, _ := mention["url"].(string)
			if outlet == "" || url == "" {
				continue
			}
			item := map[string]any{
				"@type": "NewsArticle",
				"url":   url,
				"publisher": map[string]any{
					"@type": "Organization",
					"name":  outlet,
				},
			}
			if title, ok := mention["title"].(string); ok && title != "" {
				item["name"] = title
				item["headline"] = title
			}
			if date, ok := mention["date"].(string); ok && date != "" {
				item["datePublished"] = date
			}
			subjectOf = append(subjectOf, item)
		}
	}

	if mediaAppearances, ok := attrs["media_appearances"].([]any); ok {
		for _, m := range mediaAppearances {
			appearance, ok := m.(map[string]any)
			if !ok {
				continue
			}
			outlet, _ := appearance["outlet"].(string)
			url, _ := appearance["url"].(string)
			if outlet == "" || url == "" {
				continue
			}
			mediaType := "CreativeWork"
			if t, ok := appearance["type"].(string); ok {
				switch t {
				case "podcast":
					mediaType = "PodcastEpisode"
				case "video":
					mediaType = "VideoObject"
				case "tv", "radio":
					mediaType = "BroadcastEvent"
				case "interview":
					mediaType = "Article"
				}
			}
			item := map[string]any{
				"@type": mediaType,
				"url":   url,
				"publisher": map[string]any{
					"@type": "Organization",
					"name":  outlet,
				},
			}
			if title, ok := appearance["title"].(string); ok && title != "" {
				item["name"] = title
			}
			if date, ok := appearance["date"].(string); ok && date != "" {
				item["datePublished"] = date
			}
			subjectOf = append(subjectOf, item)
		}
	}

	if books, ok := attrs["books"].([]any); ok {
		for _, b := range books {
			book, ok := b.(map[string]any)
			if !ok {
				continue
			}
			title, _ := book["title"].(string)
			if title == "" {
				continue
			}
			item := map[string]any{
				"@type": "Book",
				"name":  title,
			}
			if publisher, ok := book["publisher"].(string); ok && publisher != "" {
				item["publisher"] = map[string]any{
					"@type": "Organization",
					"name":  publisher,
				}
			}
			if year, ok := book["year"].(float64); ok {
				item["datePublished"] = fmt.Sprintf("%d", int(year))
			}
			if isbn, ok := book["isbn"].(string); ok && isbn != "" {
				item["isbn"] = isbn
			}
			if url, ok := book["url"].(string); ok && url != "" {
				item["url"] = url
			}
			subjectOf = append(subjectOf, item)
		}
	}

	if publications, ok := attrs["publications"].([]any); ok {
		for _, p := range publications {
			pub, ok := p.(map[string]any)
			if !ok {
				continue
			}
			title, _ := pub["title"].(string)
			url, _ := pub["url"].(string)
			if title == "" || url == "" {
				continue
			}
			item := map[string]any{
				"@type":    "Article",
				"name":     title,
				"headline": title,
				"url":      url,
			}
			if outlet, ok := pub["publication"].(string); ok && outlet != "" {
				item["publisher"] = map[string]any{
					"@type": "Organization",
					"name":  outlet,
				}
			}
			if date, ok := pub["date"].(string); ok && date != "" {
				item["datePublished"] = date
			}
			subjectOf = append(subjectOf, item)
		}
	}

	if len(subjectOf) > 0 {
		jsonLD["subjectOf"] = subjectOf
	}

	// performerIn: speaking engagements
	if speaking, ok := attrs["speaking_engagements"].([]any); ok {
		performerIn := []map[string]any{}
		for _, s := range speaking {
			engagement, ok := s.(map[string]any)
			if !ok {
				continue
			}
			event, _ := engagement["event"].(string)
			if event == "" {
				continue
			}
			item := map[string]any{
				"@type": "Event",
				"name":  event,
			}
			if title, ok := engagement["title"].(string); ok && title != "" {
				item["description"] = title
			}
			if url, ok := engagement["url"].(string); ok && url != "" {
				item["url"] = url
			}
			if date, ok := engagement["date"].(string); ok && date != "" {
				item["startDate"] = date
			}
			if location, ok := engagement["location"].(string); ok && location != "" {
				item["location"] = map[string]any{
					"@type": "Place",
					"name":  location,
				}
			}
			performerIn = append(performerIn, item)
		}
		if len(performerIn) > 0 {
			jsonLD["performerIn"] = performerIn
		}
	}

	// award: simple string array of award names
	if awards, ok := attrs["awards"].([]any); ok {
		awardNames := []string{}
		for _, a := range awards {
			award, ok := a.(map[string]any)
			if !ok {
				continue
			}
			if name, ok := award["name"].(string); ok && name != "" {
				if issuer, ok := award["issuer"].(string); ok && issuer != "" {
					awardNames = append(awardNames, fmt.Sprintf("%s — %s", name, issuer))
				} else {
					awardNames = append(awardNames, name)
				}
			}
		}
		if len(awardNames) > 0 {
			jsonLD["award"] = awardNames
		}
	}

	// alumniOf: education / alma mater
	if education, ok := attrs["alma_mater"].([]any); ok {
		alumniOf := []map[string]any{}
		for _, e := range education {
			edu, ok := e.(map[string]any)
			if !ok {
				continue
			}
			school, _ := edu["school"].(string)
			if school == "" {
				continue
			}
			item := map[string]any{
				"@type": "EducationalOrganization",
				"name":  school,
			}
			if url, ok := edu["url"].(string); ok && url != "" {
				item["url"] = url
			}
			alumniOf = append(alumniOf, item)
		}
		if len(alumniOf) > 0 {
			jsonLD["alumniOf"] = alumniOf
		}
	}
}

// buildOrganizationJSONLD builds JSON-LD for Organization schema type.
func buildOrganizationJSONLD(jsonLD map[string]any, entity *Entity, attrs map[string]any, baseURL string) {
	jsonLD["@type"] = "Organization"
	jsonLD["name"] = entity.Name

	if logo, ok := attrs["logo"].(string); ok && logo != "" {
		jsonLD["logo"] = logo
	}
	if url, ok := attrs["website"].(string); ok && url != "" {
		jsonLD["url"] = url
	} else {
		jsonLD["url"] = fmt.Sprintf("%s/%s", baseURL, entity.Slug)
	}
	if description, ok := attrs["description"].(string); ok {
		jsonLD["description"] = description
	}

	// Address
	address := buildPostalAddress(attrs)
	if len(address) > 1 {
		jsonLD["address"] = address
	}

	// Contact
	if phone, ok := attrs["phone"].(string); ok && phone != "" {
		jsonLD["telephone"] = phone
	}
	if email, ok := attrs["email"].(string); ok && email != "" {
		jsonLD["email"] = email
	}

	// Social profiles
	sameAs := []string{}
	socialFields := []string{"linkedin", "twitter", "facebook", "instagram", "youtube"}
	for _, field := range socialFields {
		if url, ok := attrs[field].(string); ok && url != "" {
			sameAs = append(sameAs, url)
		}
	}
	if len(sameAs) > 0 {
		jsonLD["sameAs"] = sameAs
	}
}

// buildProductJSONLD builds JSON-LD for Product schema type.
func buildProductJSONLD(jsonLD map[string]any, entity *Entity, attrs map[string]any, baseURL string) {
	jsonLD["@type"] = "Product"
	jsonLD["name"] = entity.Name
	jsonLD["url"] = fmt.Sprintf("%s/%s", baseURL, entity.Slug)

	if description, ok := attrs["description"].(string); ok {
		jsonLD["description"] = description
	}
	if image, ok := attrs["image"].(string); ok && image != "" {
		jsonLD["image"] = image
	}

	// Build offers
	if price, ok := attrs["price"].(float64); ok {
		offers := map[string]any{
			"@type": "Offer",
			"price": price,
		}
		if currency, ok := attrs["currency"].(string); ok {
			offers["priceCurrency"] = currency
		} else {
			offers["priceCurrency"] = "AUD" // Default to AUD
		}
		if availability, ok := attrs["availability"].(string); ok {
			offers["availability"] = availability
		}
		jsonLD["offers"] = offers
	}

	// Brand
	if brand, ok := attrs["brand"].(string); ok && brand != "" {
		jsonLD["brand"] = map[string]any{
			"@type": "Brand",
			"name":  brand,
		}
	}

	// SKU
	if sku, ok := attrs["sku"].(string); ok && sku != "" {
		jsonLD["sku"] = sku
	}

	// Aggregate rating
	if rating := buildAggregateRating(attrs); rating != nil {
		jsonLD["aggregateRating"] = rating
	}
}

// buildPostalAddress extracts address fields and builds a PostalAddress object.
func buildPostalAddress(attrs map[string]any) map[string]any {
	address := map[string]any{
		"@type": "PostalAddress",
	}

	// Handle nested address object
	if addrObj, ok := attrs["address"].(map[string]any); ok {
		if street, ok := addrObj["street"].(string); ok && street != "" {
			address["streetAddress"] = street
		} else if street, ok := addrObj["street_address"].(string); ok && street != "" {
			address["streetAddress"] = street
		}
		if city, ok := addrObj["city"].(string); ok && city != "" {
			address["addressLocality"] = city
		}
		if state, ok := addrObj["state"].(string); ok && state != "" {
			address["addressRegion"] = state
		}
		if postal, ok := addrObj["postal_code"].(string); ok && postal != "" {
			address["postalCode"] = postal
		} else if zip, ok := addrObj["zip"].(string); ok && zip != "" {
			address["postalCode"] = zip
		}
		if country, ok := addrObj["country"].(string); ok && country != "" {
			address["addressCountry"] = country
		}
		return address
	}

	// Handle flat address fields
	if street, ok := attrs["street_address"].(string); ok && street != "" {
		address["streetAddress"] = street
	} else if street, ok := attrs["street"].(string); ok && street != "" {
		address["streetAddress"] = street
	}

	if city, ok := attrs["city"].(string); ok && city != "" {
		address["addressLocality"] = city
	}

	if state, ok := attrs["state"].(string); ok && state != "" {
		address["addressRegion"] = state
	}

	if postal, ok := attrs["postal_code"].(string); ok && postal != "" {
		address["postalCode"] = postal
	} else if zip, ok := attrs["zip"].(string); ok && zip != "" {
		address["postalCode"] = zip
	}

	if country, ok := attrs["country"].(string); ok && country != "" {
		address["addressCountry"] = country
	}

	return address
}

// buildAggregateRating extracts rating data and builds an AggregateRating object.
func buildAggregateRating(attrs map[string]any) map[string]any {
	// Check for aggregate_rating or rating attributes
	var ratingData map[string]any
	if r, ok := attrs["aggregate_rating"].(map[string]any); ok {
		ratingData = r
	} else if r, ok := attrs["rating"].(map[string]any); ok {
		ratingData = r
	}

	if ratingData != nil {
		rating := map[string]any{
			"@type": "AggregateRating",
		}

		if value, ok := ratingData["value"].(float64); ok {
			rating["ratingValue"] = value
		} else if value, ok := ratingData["rating_value"].(float64); ok {
			rating["ratingValue"] = value
		}

		if count, ok := ratingData["count"].(float64); ok {
			rating["ratingCount"] = int(count)
		} else if count, ok := ratingData["review_count"].(float64); ok {
			rating["ratingCount"] = int(count)
		}

		if best, ok := ratingData["best_rating"].(float64); ok {
			rating["bestRating"] = best
		}

		if _, hasValue := rating["ratingValue"]; hasValue {
			return rating
		}
	}

	// Check for flat rating fields
	if ratingValue, ok := attrs["rating_value"].(float64); ok {
		rating := map[string]any{
			"@type":       "AggregateRating",
			"ratingValue": ratingValue,
		}
		if count, ok := attrs["rating_count"].(float64); ok {
			rating["ratingCount"] = int(count)
		} else if count, ok := attrs["review_count"].(float64); ok {
			rating["ratingCount"] = int(count)
		}
		return rating
	}

	return nil
}

// buildSettingsTypedJSONLD emits the generic document for a schema whose
// settings.schema_org_type names the @type.
func buildSettingsTypedJSONLD(ent *Entity, typ, baseURL string) map[string]any {
	var attrs map[string]any
	if err := json.Unmarshal(ent.Attributes, &attrs); err != nil {
		attrs = map[string]any{}
	}
	entityURL := BuildEntityURLForJSONLD(ent, baseURL)
	doc := map[string]any{
		"@context": "https://schema.org",
		"@type":    typ,
		"name":     ent.Name,
		"url":      entityURL,
		"@id":      entityURL + "#" + strings.ToLower(typ),
	}
	switch typ {
	case "Article", "BlogPosting", "NewsArticle":
		doc["headline"] = ent.Name
	}
	for _, key := range []string{"description", "meta_description", "excerpt"} {
		if d, ok := attrs[key].(string); ok && d != "" {
			doc["description"] = d
			break
		}
	}
	for _, key := range []string{"featured_image", "image", "og_image"} {
		if img, ok := attrs[key].(string); ok && img != "" {
			doc["image"] = img
			break
		}
	}
	if ent.PublishedAt != nil {
		doc["datePublished"] = ent.PublishedAt.Format(time.RFC3339)
	}
	if !ent.UpdatedAt.IsZero() {
		doc["dateModified"] = ent.UpdatedAt.Format(time.RFC3339)
	}
	return doc
}
