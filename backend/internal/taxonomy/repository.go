package taxonomy

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/entity"
)

// ErrTermNotFound is returned when a lookup misses.
var ErrTermNotFound = errors.New("taxonomy: term not found")

// ErrTermSlugConflict is returned when an insert or slug-update would
// violate the (kind, slug) uniqueness constraint.
var ErrTermSlugConflict = errors.New("taxonomy: term slug already exists for this kind")

// Validation errors of term writes. Their messages are the codes the REST API
// answers with.
var (
	ErrInvalidKind        = errors.New("invalid_kind")
	ErrInvalidName        = errors.New("invalid_name")
	ErrInvalidSlug        = errors.New("invalid_slug")
	ErrInvalidDescription = errors.New("invalid_description")
)

// ErrTermParentNotFound is returned when the parent term does not exist.
var ErrTermParentNotFound = errors.New("taxonomy: parent term not found")

// ErrTermParentInvalid is returned for a parent of another kind, a parent
// cycle, or nesting deeper than MaxDepth.
var ErrTermParentInvalid = errors.New("taxonomy: invalid parent term")

// Repository is the storage contract.
type Repository interface {
	// CreateTerm inserts a row. On (kind, slug) conflict returns
	// ErrTermSlugConflict.
	CreateTerm(ctx context.Context, t *Term) error

	// UpdateTerm overwrites name + slug. On conflict returns
	// ErrTermSlugConflict. On miss returns ErrTermNotFound.
	UpdateTerm(ctx context.Context, t *Term) error

	// DeleteTerm hard-deletes a term. FK CASCADE wipes the join rows.
	DeleteTerm(ctx context.Context, id uuid.UUID) error

	// GetTerm returns one term by ID.
	GetTerm(ctx context.Context, id uuid.UUID) (*Term, error)

	// GetTermBySlug returns one term by (kind, slug).
	GetTermBySlug(ctx context.Context, kind, slug string) (*Term, error)

	// ListTerms returns a paginated list, optionally filtered by kind
	// and a case-insensitive name substring match.
	ListTerms(ctx context.Context, kind, search string, page, perPage int) (*TermListResult, error)

	// ListTermsByIDs returns a slice in arbitrary order for the supplied
	// IDs. Missing IDs are silently skipped.
	ListTermsByIDs(ctx context.Context, ids []uuid.UUID) ([]*Term, error)

	// ListTermsForEntity returns every term attached to an entity,
	// ordered by kind then name. Used by the admin editor sidebar to
	// hydrate the picker on load.
	ListTermsForEntity(ctx context.Context, entityID uuid.UUID) ([]*Term, error)

	// ReplaceEntityTerms is the idempotent set-replace operation behind
	// the PUT /entities/{id}/terms endpoint. termIDs may be empty to
	// detach all terms. Runs in a single transaction so partial state
	// never leaks.
	ReplaceEntityTerms(ctx context.Context, entityID uuid.UUID, termIDs []uuid.UUID) error

	// ListEntitiesForTerm returns published entities attached to the
	// given term, paginated, newest-first.
	ListEntitiesForTerm(ctx context.Context, termID uuid.UUID, page, perPage int) ([]*EntityRef, int, error)

	// Chains returns, for each id, the terms from the root down to it
	// (itself last), at most MaxDepth long.
	Chains(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID][]*Term, error)

	// KindExists reports whether any term of kind exists.
	KindExists(ctx context.Context, kind string) (bool, error)
}

// PostgresRepository is the pgx-backed implementation.
type PostgresRepository struct {
	db *database.DB
}

// NewPostgresRepository constructs a repository.
func NewPostgresRepository(db *database.DB) *PostgresRepository {
	return &PostgresRepository{db: db}
}

// uniqueViolation tests whether err is a Postgres unique_violation
// (SQLSTATE 23505). Used to map a DB-level conflict to ErrTermSlugConflict.
func uniqueViolation(err error) bool {
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
		return pgErr.Code == "23505"
	}
	return false
}

// CreateTerm inserts a hatamoto_terms row. ID is generated server-side
// when zero. created_at / updated_at always default to NOW().
func (r *PostgresRepository) CreateTerm(ctx context.Context, t *Term) error {
	if t == nil {
		return errors.New("nil term")
	}
	if !ValidKind(t.Kind) {
		return fmt.Errorf("invalid kind %q", t.Kind)
	}
	if !ValidSlug(t.Slug) {
		return fmt.Errorf("invalid slug %q", t.Slug)
	}
	if strings.TrimSpace(t.Name) == "" || len(t.Name) > MaxNameLen {
		return fmt.Errorf("invalid name")
	}
	if t.ID == uuid.Nil {
		t.ID = uuid.New()
	}
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	now := time.Now()
	if _, err := conn.Exec(ctx, `
        INSERT INTO hatamoto_terms (id, kind, slug, name, description, parent_id, created_at, updated_at)
        VALUES ($1, $2, $3, $4, $5, $6, $7, $7)
    `, t.ID, t.Kind, t.Slug, t.Name, t.Description, t.ParentID, now); err != nil {
		if uniqueViolation(err) {
			return ErrTermSlugConflict
		}
		if foreignKeyViolation(err) {
			return ErrTermParentNotFound
		}
		return fmt.Errorf("create term: %w", err)
	}
	t.CreatedAt = now
	t.UpdatedAt = now
	return nil
}

// UpdateTerm updates name + slug. kind is immutable post-create — letting
// admins flip kind would silently break public URLs.
func (r *PostgresRepository) UpdateTerm(ctx context.Context, t *Term) error {
	if t == nil || t.ID == uuid.Nil {
		return errors.New("missing term id")
	}
	if !ValidSlug(t.Slug) {
		return fmt.Errorf("invalid slug %q", t.Slug)
	}
	if strings.TrimSpace(t.Name) == "" || len(t.Name) > MaxNameLen {
		return fmt.Errorf("invalid name")
	}
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	tag, err := conn.Exec(ctx, `
        UPDATE hatamoto_terms
           SET slug = $2, name = $3, description = $4, parent_id = $5, updated_at = NOW()
         WHERE id = $1
    `, t.ID, t.Slug, t.Name, t.Description, t.ParentID)
	if err != nil {
		if uniqueViolation(err) {
			return ErrTermSlugConflict
		}
		if foreignKeyViolation(err) {
			return ErrTermParentNotFound
		}
		return fmt.Errorf("update term: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrTermNotFound
	}
	return nil
}

// DeleteTerm removes a term row. FK CASCADE on hatamoto_entity_terms
// removes any attachments. Idempotent — missing ID is not an error.
func (r *PostgresRepository) DeleteTerm(ctx context.Context, id uuid.UUID) error {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	_, err = conn.Exec(ctx, `DELETE FROM hatamoto_terms WHERE id = $1`, id)
	return err
}

// GetTerm fetches by ID.
func (r *PostgresRepository) GetTerm(ctx context.Context, id uuid.UUID) (*Term, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()
	t := &Term{}
	err = conn.QueryRow(ctx, `
        SELECT id, kind, slug, name, description, parent_id, created_at, updated_at
          FROM hatamoto_terms
         WHERE id = $1
    `, id).Scan(&t.ID, &t.Kind, &t.Slug, &t.Name, &t.Description, &t.ParentID, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrTermNotFound
		}
		return nil, err
	}
	return t, nil
}

// GetTermBySlug fetches by (kind, slug).
func (r *PostgresRepository) GetTermBySlug(ctx context.Context, kind, slug string) (*Term, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()
	t := &Term{}
	err = conn.QueryRow(ctx, `
        SELECT id, kind, slug, name, description, parent_id, created_at, updated_at
          FROM hatamoto_terms
         WHERE kind = $1 AND slug = $2
    `, kind, slug).Scan(&t.ID, &t.Kind, &t.Slug, &t.Name, &t.Description, &t.ParentID, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrTermNotFound
		}
		return nil, err
	}
	return t, nil
}

// ListTerms returns the paginated admin list with attachment counts.
// The COUNT is computed in the same query via LEFT JOIN + GROUP BY so
// the round-trip stays at one.
func (r *PostgresRepository) ListTerms(ctx context.Context, kind, search string, page, perPage int) (*TermListResult, error) {
	if page < 1 {
		page = 1
	}
	if perPage < 1 || perPage > 200 {
		perPage = 50
	}
	offset := (page - 1) * perPage

	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	// Build WHERE clause + args. Bind kind / search separately so the
	// query plan stays cacheable.
	where := []string{"1=1"}
	args := []any{}
	if kind != "" {
		args = append(args, kind)
		where = append(where, fmt.Sprintf("kind = $%d", len(args)))
	}
	if search != "" {
		args = append(args, "%"+strings.ToLower(search)+"%")
		where = append(where, fmt.Sprintf("LOWER(name) LIKE $%d", len(args)))
	}
	whereSQL := strings.Join(where, " AND ")

	var total int
	countArgs := append([]any{}, args...)
	if err := conn.QueryRow(ctx,
		fmt.Sprintf(`SELECT COUNT(*) FROM hatamoto_terms WHERE %s`, whereSQL),
		countArgs...,
	).Scan(&total); err != nil {
		return nil, fmt.Errorf("count terms: %w", err)
	}

	args = append(args, perPage, offset)
	listSQL := fmt.Sprintf(`
        SELECT t.id, t.kind, t.slug, t.name, t.description, t.parent_id, t.created_at, t.updated_at,
               COALESCE(c.n, 0)
          FROM hatamoto_terms t
          LEFT JOIN (
              SELECT term_id, COUNT(*) AS n
                FROM hatamoto_entity_terms
               GROUP BY term_id
          ) c ON c.term_id = t.id
         WHERE %s
         ORDER BY t.kind, t.name
         LIMIT $%d OFFSET $%d
    `, whereSQL, len(args)-1, len(args))

	rows, err := conn.Query(ctx, listSQL, args...)
	if err != nil {
		return nil, fmt.Errorf("list terms: %w", err)
	}
	defer rows.Close()
	items := make([]*TermWithCount, 0, perPage)
	for rows.Next() {
		tc := &TermWithCount{}
		if err := rows.Scan(&tc.ID, &tc.Kind, &tc.Slug, &tc.Name, &tc.Description, &tc.ParentID, &tc.CreatedAt, &tc.UpdatedAt, &tc.EntityCount); err != nil {
			return nil, err
		}
		items = append(items, tc)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return &TermListResult{
		Items:   items,
		Total:   total,
		Page:    page,
		PerPage: perPage,
	}, nil
}

// ListTermsByIDs fetches the supplied IDs in one round-trip.
func (r *PostgresRepository) ListTermsByIDs(ctx context.Context, ids []uuid.UUID) ([]*Term, error) {
	if len(ids) == 0 {
		return []*Term{}, nil
	}
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()
	rows, err := conn.Query(ctx, `
        SELECT id, kind, slug, name, description, parent_id, created_at, updated_at
          FROM hatamoto_terms
         WHERE id = ANY($1)
    `, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*Term, 0, len(ids))
	for rows.Next() {
		t := &Term{}
		if err := rows.Scan(&t.ID, &t.Kind, &t.Slug, &t.Name, &t.Description, &t.ParentID, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ListTermsForEntity returns terms attached to an entity, sorted by
// kind then name. The editor sidebar uses this to pre-populate the
// picker on load.
func (r *PostgresRepository) ListTermsForEntity(ctx context.Context, entityID uuid.UUID) ([]*Term, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()
	rows, err := conn.Query(ctx, `
        SELECT t.id, t.kind, t.slug, t.name, t.description, t.parent_id, t.created_at, t.updated_at
          FROM hatamoto_terms t
          JOIN hatamoto_entity_terms et ON et.term_id = t.id
         WHERE et.entity_id = $1
         ORDER BY t.kind, t.name
    `, entityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Term{}
	for rows.Next() {
		t := &Term{}
		if err := rows.Scan(&t.ID, &t.Kind, &t.Slug, &t.Name, &t.Description, &t.ParentID, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ReplaceEntityTerms sets the entity's terms to exactly the supplied IDs.
// Implemented as DELETE-then-INSERT inside one tx so the join table never
// transiently shows a mixed state. Duplicate IDs in the input slice are
// idempotent (PK collision is silently ignored via ON CONFLICT).
//
// Term-existence validation happens INSIDE the transaction (Phase 2
// review item #4 — TOCTOU). The service-layer pre-fetch in
// Service.ReplaceEntityTerms is a UX optimisation only; if a term is
// deleted between that pre-fetch and reaching this tx the user would
// otherwise see a generic FK-violation 500. We hold a SHARE lock on
// each supplied term row for the duration of the join INSERTs, which
// blocks a concurrent DELETE FROM hatamoto_terms WHERE id = ... long
// enough for the join to commit. If the lookup misses a row, we return
// a typed error the handler maps to 400 unknown_term:<uuid>.
func (r *PostgresRepository) ReplaceEntityTerms(ctx context.Context, entityID uuid.UUID, termIDs []uuid.UUID) error {
	if entityID == uuid.Nil {
		return errors.New("missing entity id")
	}
	if len(termIDs) > MaxTermsPerEntity {
		return fmt.Errorf("too many terms: max %d", MaxTermsPerEntity)
	}
	// Dedupe locally — saves a per-row round-trip in the unlikely case the
	// frontend sends duplicates.
	seen := make(map[uuid.UUID]struct{}, len(termIDs))
	clean := make([]uuid.UUID, 0, len(termIDs))
	for _, id := range termIDs {
		if id == uuid.Nil {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		clean = append(clean, id)
	}
	return r.db.WithTx(ctx, func(tx pgx.Tx) error {
		// In-tx existence check with FOR SHARE so a concurrent DELETE
		// on hatamoto_terms cannot drop a row between this SELECT and
		// the INSERT batch below. ANY($1::uuid[]) keeps this one
		// round-trip regardless of len(clean).
		if len(clean) > 0 {
			rows, err := tx.Query(ctx, `
                SELECT id
                  FROM hatamoto_terms
                 WHERE id = ANY($1::uuid[])
                   FOR SHARE
            `, clean)
			if err != nil {
				return fmt.Errorf("validate terms exist: %w", err)
			}
			found := make(map[uuid.UUID]struct{}, len(clean))
			for rows.Next() {
				var id uuid.UUID
				if err := rows.Scan(&id); err != nil {
					rows.Close()
					return fmt.Errorf("scan term id: %w", err)
				}
				found[id] = struct{}{}
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return fmt.Errorf("validate terms rows: %w", err)
			}
			for _, id := range clean {
				if _, ok := found[id]; !ok {
					return fmt.Errorf("unknown_term:%s", id)
				}
			}
		}

		if _, err := tx.Exec(ctx, `DELETE FROM hatamoto_entity_terms WHERE entity_id = $1`, entityID); err != nil {
			return fmt.Errorf("clear entity terms: %w", err)
		}
		if len(clean) == 0 {
			return nil
		}
		batch := &pgx.Batch{}
		for _, tid := range clean {
			batch.Queue(`
                INSERT INTO hatamoto_entity_terms (entity_id, term_id)
                VALUES ($1, $2)
                ON CONFLICT (entity_id, term_id) DO NOTHING
            `, entityID, tid)
		}
		br := tx.SendBatch(ctx, batch)
		defer br.Close()
		for range clean {
			if _, err := br.Exec(); err != nil {
				// FK violation here is unexpected — FOR SHARE above
				// should have made this path unreachable. Surface
				// verbatim if it happens (e.g. concurrent admin race
				// against FOR SHARE on a deadlock-aborted tx).
				return fmt.Errorf("insert entity term: %w", err)
			}
		}
		return nil
	})
}

// ListEntitiesForTerm joins through the M2M and returns published
// entities, newest first.
func (r *PostgresRepository) ListEntitiesForTerm(ctx context.Context, termID uuid.UUID, page, perPage int) ([]*EntityRef, int, error) {
	if page < 1 {
		page = 1
	}
	if perPage < 1 || perPage > 100 {
		perPage = 20
	}
	offset := (page - 1) * perPage

	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, 0, err
	}
	defer conn.Release()

	args := []any{termID}

	var total int
	if err := conn.QueryRow(ctx, `
        SELECT COUNT(*)
          FROM hatamoto_entity_terms et
          JOIN hatamoto_entities e ON e.id = et.entity_id
          JOIN hatamoto_entity_schemas s ON s.id = e.schema_id
         WHERE et.term_id = $1
           AND e.status = 'published'
           AND `+entity.HiddenPersonSQL+`
    `, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count entities for term: %w", err)
	}

	// Phase 2 review item #7: schemas diverge on which JSONB key holds
	// the hero image / excerpt. Blog uses featured_image + excerpt; case
	// studies use hero_image + summary; playbooks use cover + description.
	// COALESCE the common spellings in priority order so a single query
	// works across every schema kind without per-schema branching.
	// Returns '' (the trailing fallback) when no key matches — the
	// frontend already treats empty strings as "no image / no excerpt".
	args = append(args, perPage, offset)
	rows, err := conn.Query(ctx, fmt.Sprintf(`
        SELECT e.id, e.name, e.slug,
               s.slug AS schema_slug,
               COALESCE(
                   e.attributes->>'excerpt',
                   e.attributes->>'summary',
                   e.attributes->>'description',
                   ''
               ) AS excerpt,
               COALESCE(
                   e.attributes->>'featured_image',
                   e.attributes->>'hero_image',
                   e.attributes->>'cover',
                   e.attributes->>'image',
                   e.attributes->>'thumbnail',
                   ''
               ) AS image_url,
               e.published_at
          FROM hatamoto_entity_terms et
          JOIN hatamoto_entities e ON e.id = et.entity_id
          JOIN hatamoto_entity_schemas s ON s.id = e.schema_id
         WHERE et.term_id = $1
           AND e.status = 'published'
           AND %s
         ORDER BY e.published_at DESC NULLS LAST, e.created_at DESC
         LIMIT $%d OFFSET $%d
    `, entity.HiddenPersonSQL, len(args)-1, len(args)), args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list entities for term: %w", err)
	}
	defer rows.Close()
	out := make([]*EntityRef, 0, perPage)
	for rows.Next() {
		ref := &EntityRef{}
		if err := rows.Scan(&ref.ID, &ref.Name, &ref.Slug, &ref.SchemaSlug, &ref.Excerpt, &ref.ImageURL, &ref.PublishedAt); err != nil {
			return nil, 0, err
		}
		out = append(out, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// ListPublicTerms returns the terms of a kind that have at least one published
// entity, most used first (name breaks ties), with the published count.
func (r *PostgresRepository) ListPublicTerms(ctx context.Context, kind string, limit int) ([]*TermWithCount, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()
	rows, err := conn.Query(ctx, `
        SELECT t.id, t.kind, t.slug, t.name, t.description, t.parent_id, t.created_at, t.updated_at, COUNT(e.id)
          FROM hatamoto_terms t
          JOIN hatamoto_entity_terms et ON et.term_id = t.id
          JOIN hatamoto_entities e ON e.id = et.entity_id
                                  AND e.status = 'published' AND e.variant_key IS NULL
          JOIN hatamoto_entity_schemas s ON s.id = e.schema_id AND `+entity.HiddenPersonSQL+`
         WHERE t.kind = $1
         GROUP BY t.id
         ORDER BY COUNT(e.id) DESC, t.name
         LIMIT $2
    `, kind, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*TermWithCount{}
	for rows.Next() {
		t := &TermWithCount{}
		if err := rows.Scan(&t.ID, &t.Kind, &t.Slug, &t.Name, &t.Description, &t.ParentID, &t.CreatedAt, &t.UpdatedAt, &t.EntityCount); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// foreignKeyViolation tests for SQLSTATE 23503.
func foreignKeyViolation(err error) bool {
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
		return pgErr.Code == "23503"
	}
	return false
}

// Chains implements Repository.
func (r *PostgresRepository) Chains(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID][]*Term, error) {
	out := make(map[uuid.UUID][]*Term, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()
	rows, err := conn.Query(ctx, `
        WITH RECURSIVE up(start_id, id, depth) AS (
            SELECT t.id, t.id, 1 FROM hatamoto_terms t WHERE t.id = ANY($1)
            UNION ALL
            SELECT up.start_id, p.parent_id, up.depth + 1
              FROM up JOIN hatamoto_terms p ON p.id = up.id
             WHERE p.parent_id IS NOT NULL AND up.depth < $2
        )
        SELECT up.start_id, t.id, t.kind, t.slug, t.name, t.description, t.parent_id, t.created_at, t.updated_at
          FROM up JOIN hatamoto_terms t ON t.id = up.id
         ORDER BY up.start_id, up.depth DESC
    `, ids, MaxDepth+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var start uuid.UUID
		t := &Term{}
		if err := rows.Scan(&start, &t.ID, &t.Kind, &t.Slug, &t.Name, &t.Description, &t.ParentID, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		out[start] = append(out[start], t)
	}
	return out, rows.Err()
}

// Descendants returns every term below id (children, grandchildren, ...),
// without id itself. Depth is capped at MaxDepth+1, which also ends a cycle.
func (r *PostgresRepository) Descendants(ctx context.Context, id uuid.UUID) ([]*Term, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()
	rows, err := conn.Query(ctx, `
        WITH RECURSIVE down(id, depth) AS (
            SELECT t.id, 1 FROM hatamoto_terms t WHERE t.parent_id = $1
            UNION ALL
            SELECT t.id, down.depth + 1 FROM hatamoto_terms t JOIN down ON t.parent_id = down.id WHERE down.depth < $2
        )
        SELECT DISTINCT t.id, t.kind, t.slug, t.name, t.description, t.parent_id, t.created_at, t.updated_at
          FROM down JOIN hatamoto_terms t ON t.id = down.id WHERE t.id <> $1
    `, id, MaxDepth+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Term
	for rows.Next() {
		t := &Term{}
		if err := rows.Scan(&t.ID, &t.Kind, &t.Slug, &t.Name, &t.Description, &t.ParentID, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// KindExists implements Repository.
func (r *PostgresRepository) KindExists(ctx context.Context, kind string) (bool, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return false, err
	}
	defer conn.Release()
	var ok bool
	err = conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM hatamoto_terms WHERE kind = $1)`, kind).Scan(&ok)
	return ok, err
}

// FillPaths sets Path on each term to the slugs from its root term down to
// itself. Terms whose chain cannot be loaded keep their own slug.
func FillPaths(ctx context.Context, r Repository, terms ...*Term) error {
	ids := make([]uuid.UUID, 0, len(terms))
	for _, t := range terms {
		ids = append(ids, t.ID)
	}
	chains, err := r.Chains(ctx, ids)
	if err != nil {
		for _, t := range terms {
			t.Path = t.Slug
		}
		return err
	}
	for _, t := range terms {
		slugs := make([]string, 0, 3)
		for _, c := range chains[t.ID] {
			slugs = append(slugs, c.Slug)
		}
		if len(slugs) == 0 {
			slugs = append(slugs, t.Slug)
		}
		t.Path = strings.Join(slugs, "/")
	}
	return nil
}
