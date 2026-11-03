package entity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"hatamoto.dev/core/internal/urlpattern"
)

// ErrConflict reports that the live row moved since the changeset was staged
// (its version or updated_at no longer matches the base token).
var ErrConflict = errors.New("entity changed since the changeset was staged")

// maxSlugLen is the width of the slug column.
const maxSlugLen = 255

// maxSlugAttempts bounds the savepoint retries after a racing insert took the slug.
const maxSlugAttempts = 20

// ChangesetSource is the Source of the batch event AfterChangeset emits.
const ChangesetSource = "changeset"

// TxOp is one entity operation of a changeset, applied inside the caller's
// transaction. A nil ID creates; an ID updates (empty fields keep the stored
// value) or, with Delete, removes.
type TxOp struct {
	Schema     string // schema slug (create only)
	ID         *uuid.UUID
	Name       string
	Slug       string // empty derives it from Name on create
	Locale     string
	Attributes json.RawMessage
	ParentID   *uuid.UUID
	Status     EntityStatus // empty keeps the stored status (draft on create)
	Origin     string       // recorded on create; empty keeps the stored origin on update
	Delete     bool

	// BaseVersion and BaseUpdatedAt are the base token: when set they must
	// equal the live row's, else the op returns ErrConflict.
	BaseVersion   *int
	BaseUpdatedAt *time.Time
}

// TxResult is what ApplyChangesetTx did. Before is the pre-image for
// RestoreChangesetTx (nil for a created row).
type TxResult struct {
	ID       uuid.UUID
	Slug     string
	Before   json.RawMessage
	Conflict bool
	Created  bool
	Deleted  bool
}

// entitySnapshot is the stored pre-image of an entity row.
type entitySnapshot struct {
	ID             uuid.UUID       `json:"id"`
	SchemaID       uuid.UUID       `json:"schema_id"`
	Name           string          `json:"name"`
	Slug           string          `json:"slug"`
	Locale         string          `json:"locale"`
	Status         string          `json:"status"`
	Attributes     json.RawMessage `json:"attributes"`
	Metadata       json.RawMessage `json:"metadata"`
	Version        int             `json:"version"`
	CreatedBy      *uuid.UUID      `json:"created_by,omitempty"`
	UpdatedBy      *uuid.UUID      `json:"updated_by,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	PublishedAt    *time.Time      `json:"published_at,omitempty"`
	ParentID       *uuid.UUID      `json:"parent_id,omitempty"`
	AuthorID       *uuid.UUID      `json:"author_id,omitempty"`
	BylinePersonID *uuid.UUID      `json:"byline_person_id,omitempty"`
	BylineOrg      bool            `json:"byline_org"`
	Origin         string          `json:"origin"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

const snapshotColumns = `id, schema_id, name, slug, locale, status, attributes, metadata, version,
	created_by, updated_by, created_at, published_at, parent_id, author_id, byline_person_id, byline_org, origin, updated_at`

func (e *entitySnapshot) scan(row pgx.Row) error {
	return row.Scan(&e.ID, &e.SchemaID, &e.Name, &e.Slug, &e.Locale, &e.Status, &e.Attributes, &e.Metadata, &e.Version,
		&e.CreatedBy, &e.UpdatedBy, &e.CreatedAt, &e.PublishedAt, &e.ParentID, &e.AuthorID, &e.BylinePersonID, &e.BylineOrg, &e.Origin, &e.UpdatedAt)
}

// ApplyChangesetTx applies op inside tx, in its own savepoint: on any error
// the savepoint is rolled back and tx stays usable for the next item. It
// validates and sanitises like CreateEntity/UpdateEntity but runs neither the
// before_save hook nor any observer, event or background job: call
// AfterChangeset once the transaction committed. A create whose slug is taken
// gets a numeric suffix (-2, -3, ...); a moved base token returns ErrConflict
// (and Conflict set).
func (s *Service) ApplyChangesetTx(ctx context.Context, tx pgx.Tx, op TxOp) (res TxResult, err error) {
	sp, err := tx.Begin(ctx)
	if err != nil {
		return TxResult{}, fmt.Errorf("failed to open savepoint: %w", err)
	}
	defer func() {
		if err != nil {
			_ = sp.Rollback(ctx)
			return
		}
		if err = sp.Commit(ctx); err != nil {
			res, err = TxResult{}, fmt.Errorf("failed to release savepoint: %w", err)
		}
	}()
	switch {
	case op.ID == nil && op.Delete:
		return TxResult{}, ErrEntityNotFound
	case op.ID == nil:
		return s.createTx(ctx, sp, op)
	}
	cur := &entitySnapshot{}
	if err := cur.scan(sp.QueryRow(ctx, `SELECT `+snapshotColumns+` FROM hatamoto_entities WHERE id = $1 FOR UPDATE`, *op.ID)); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return TxResult{}, ErrEntityNotFound
		}
		return TxResult{}, fmt.Errorf("failed to read entity: %w", err)
	}
	if (op.BaseVersion != nil && *op.BaseVersion != cur.Version) ||
		(op.BaseUpdatedAt != nil && !op.BaseUpdatedAt.Truncate(time.Microsecond).Equal(cur.UpdatedAt.Truncate(time.Microsecond))) {
		return TxResult{ID: cur.ID, Slug: cur.Slug, Conflict: true}, ErrConflict
	}
	before, err := json.Marshal(cur)
	if err != nil {
		return TxResult{}, err
	}
	if op.Delete {
		return deleteTx(ctx, sp, cur, before)
	}
	return s.updateTx(ctx, sp, op, cur, before)
}

func deleteTx(ctx context.Context, tx pgx.Tx, cur *entitySnapshot, before json.RawMessage) (TxResult, error) {
	var kids bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM hatamoto_entities WHERE parent_id = $1)`, cur.ID).Scan(&kids); err != nil {
		return TxResult{}, fmt.Errorf("failed to check children: %w", err)
	}
	if kids {
		return TxResult{}, ErrHasChildren
	}
	if _, err := tx.Exec(ctx, `DELETE FROM hatamoto_entities WHERE id = $1`, cur.ID); err != nil {
		return TxResult{}, fmt.Errorf("failed to delete entity: %w", err)
	}
	return TxResult{ID: cur.ID, Slug: cur.Slug, Before: before, Deleted: true}, nil
}

func (s *Service) createTx(ctx context.Context, tx pgx.Tx, op TxOp) (TxResult, error) {
	schema, err := schemaBySlugTx(ctx, tx, op.Schema)
	if err != nil {
		return TxResult{}, err
	}
	locale := op.Locale
	if locale != "" {
		if locale, err = NormalizeLocale(locale); err != nil {
			return TxResult{}, err
		}
	}
	if err := s.validator.ValidateAttributes(schema.Schema, op.Attributes); err != nil {
		return TxResult{}, err
	}
	attrs, err := sanitizeAttributes(op.Attributes)
	if err != nil {
		return TxResult{}, err
	}
	if attrs, err = normalizeDocRaw(docWrite, nil, attrs, EligibleForPageDoc(schema)); err != nil {
		return TxResult{}, err
	}
	slug := normalizeSlug(op.Slug)
	if op.Slug == "" {
		slug = generateSlug(op.Name)
	}
	if op.ParentID != nil {
		if err := checkParentTx(ctx, tx, nil, *op.ParentID); err != nil {
			return TxResult{}, err
		}
	}
	status, err := txStatus(schema, attrs, op.Status, StatusDraft)
	if err != nil {
		return TxResult{}, err
	}
	origin := op.Origin
	if origin == "" {
		origin = "owner"
	}

	id := uuid.New()
	for attempt := 0; attempt < maxSlugAttempts; attempt++ {
		cand, err := freeSlugTx(ctx, tx, schema.ID, op.ParentID, nil, slug, attempt)
		if err != nil {
			return TxResult{}, err
		}
		sp, err := tx.Begin(ctx)
		if err != nil {
			return TxResult{}, fmt.Errorf("failed to open savepoint: %w", err)
		}
		_, err = sp.Exec(ctx, `
			INSERT INTO hatamoto_entities (id, schema_id, name, slug, locale, status, attributes, metadata, version,
				parent_id, origin, published_at)
			VALUES ($1, $2, $3, $4, COALESCE(NULLIF($5::text, ''), public.hatamoto_site_language()), $6::text, $7, '{}', 1,
				$8, $9, CASE WHEN $6::text = 'published' THEN now() END)`,
			id, schema.ID, op.Name, cand, locale, string(status), attrs, op.ParentID, origin)
		if err == nil {
			if err = sp.Commit(ctx); err != nil {
				return TxResult{}, fmt.Errorf("failed to release savepoint: %w", err)
			}
			return TxResult{ID: id, Slug: cand, Created: true}, nil
		}
		_ = sp.Rollback(ctx)
		switch {
		case isPgUniqueViolation(err):
			continue // a concurrent writer took the slug: probe again
		case isPgForeignKeyViolation(err):
			return TxResult{}, fkViolation(err)
		}
		return TxResult{}, fmt.Errorf("failed to create entity: %w", err)
	}
	return TxResult{}, ErrSlugAlreadyExists
}

func (s *Service) updateTx(ctx context.Context, tx pgx.Tx, op TxOp, cur *entitySnapshot, before json.RawMessage) (TxResult, error) {
	schema, err := getSchemaByID(ctx, tx, cur.SchemaID, "")
	if err != nil {
		return TxResult{}, err
	}
	locale := cur.Locale
	if op.Locale != "" {
		if locale, err = NormalizeLocale(op.Locale); err != nil {
			return TxResult{}, err
		}
	}
	attrs, changed := cur.Attributes, false
	if op.Attributes != nil {
		if err := s.validator.ValidateAttributes(schema.Schema, op.Attributes); err != nil {
			return TxResult{}, err
		}
		cleaned, err := sanitizeAttributesAgainst(op.Attributes, cur.Attributes)
		if err != nil {
			return TxResult{}, err
		}
		if attrs, err = normalizeDocRaw(docWrite, cur.Attributes, cleaned, EligibleForPageDoc(schema)); err != nil {
			return TxResult{}, err
		}
		changed = !jsonRawEqual(attrs, cur.Attributes)
	}
	parent := cur.ParentID
	if op.ParentID != nil {
		if err := checkParentTx(ctx, tx, &cur.ID, *op.ParentID); err != nil {
			return TxResult{}, err
		}
		parent = op.ParentID
	}
	status, err := txStatus(schema, attrs, op.Status, EntityStatus(cur.Status))
	if err != nil {
		return TxResult{}, err
	}
	name := cur.Name
	if op.Name != "" {
		name = op.Name
	}
	slug := cur.Slug
	if op.Slug != "" {
		slug = normalizeSlug(op.Slug)
	}
	origin := cur.Origin
	if op.Origin != "" {
		origin = op.Origin
	}

	if changed {
		if _, err := tx.Exec(ctx, `INSERT INTO hatamoto_entity_versions (entity_id, version, attributes) VALUES ($1, $2, $3)`,
			cur.ID, cur.Version, cur.Attributes); err != nil {
			return TxResult{}, fmt.Errorf("failed to create version snapshot: %w", err)
		}
	}
	for attempt := 0; attempt < maxSlugAttempts; attempt++ {
		cand := slug
		if slug != cur.Slug || parent != cur.ParentID {
			if cand, err = freeSlugTx(ctx, tx, schema.ID, parent, &cur.ID, slug, attempt); err != nil {
				return TxResult{}, err
			}
		}
		sp, err := tx.Begin(ctx)
		if err != nil {
			return TxResult{}, fmt.Errorf("failed to open savepoint: %w", err)
		}
		_, err = sp.Exec(ctx, `
			UPDATE hatamoto_entities
			SET name = $2, slug = $3, locale = $4, attributes = $5, parent_id = $6, status = $7::text, origin = $8,
			    published_at = CASE WHEN $7::text = 'published' AND published_at IS NULL THEN now() ELSE published_at END,
			    version = CASE WHEN $9 THEN version + 1 ELSE version END
			WHERE id = $1`,
			cur.ID, name, cand, locale, attrs, parent, string(status), origin, changed)
		if err == nil {
			if err = sp.Commit(ctx); err != nil {
				return TxResult{}, fmt.Errorf("failed to release savepoint: %w", err)
			}
			return TxResult{ID: cur.ID, Slug: cand, Before: before}, nil
		}
		_ = sp.Rollback(ctx)
		switch {
		case isPgUniqueViolation(err):
			continue
		case isPgForeignKeyViolation(err):
			return TxResult{}, fkViolation(err)
		}
		return TxResult{}, fmt.Errorf("failed to update entity: %w", err)
	}
	return TxResult{}, ErrSlugAlreadyExists
}

// RestoreChangesetTx reverts one applied op inside tx: an empty before (the
// row was created) deletes the row; otherwise the row is re-created or
// overwritten from the pre-image ApplyChangesetTx returned.
func (s *Service) RestoreChangesetTx(ctx context.Context, tx pgx.Tx, id uuid.UUID, before json.RawMessage) (err error) {
	sp, err := tx.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to open savepoint: %w", err)
	}
	defer func() {
		if err != nil {
			_ = sp.Rollback(ctx)
			return
		}
		err = sp.Commit(ctx)
	}()
	if len(before) == 0 {
		if _, err := sp.Exec(ctx, `DELETE FROM hatamoto_entities WHERE id = $1`, id); err != nil {
			if isPgUniqueViolation(err) {
				return ErrHasChildren
			}
			return fmt.Errorf("failed to delete entity: %w", err)
		}
		return nil
	}
	var snap entitySnapshot
	if err := json.Unmarshal(before, &snap); err != nil {
		return fmt.Errorf("invalid pre-image: %w", err)
	}
	if snap.ID != id {
		return fmt.Errorf("pre-image is for entity %s, not %s", snap.ID, id)
	}
	// The restore is a new write: the version never rewinds (history keeps a
	// unique row per version). When the attributes differ, the live ones are
	// snapshotted and the version moves on.
	var curVersion int
	var curAttrs json.RawMessage
	switch err := sp.QueryRow(ctx, `SELECT version, attributes FROM hatamoto_entities WHERE id = $1 FOR UPDATE`, id).Scan(&curVersion, &curAttrs); {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return fmt.Errorf("failed to read entity: %w", err)
	default:
		changed := !jsonRawEqual(curAttrs, snap.Attributes)
		if changed {
			if _, err := sp.Exec(ctx, `INSERT INTO hatamoto_entity_versions (entity_id, version, attributes) VALUES ($1, $2, $3)`,
				id, curVersion, curAttrs); err != nil {
				return fmt.Errorf("failed to create version snapshot: %w", err)
			}
		}
		_, err := sp.Exec(ctx, `
			UPDATE hatamoto_entities
			SET name = $2, slug = $3, locale = $4, status = $5, attributes = $6, metadata = $7,
			    parent_id = $8, author_id = $9, byline_person_id = $10, byline_org = $11, published_at = $12, origin = $13,
			    version = CASE WHEN $14 THEN version + 1 ELSE version END
			WHERE id = $1`,
			id, snap.Name, snap.Slug, snap.Locale, snap.Status, snap.Attributes, snap.Metadata,
			snap.ParentID, snap.AuthorID, snap.BylinePersonID, snap.BylineOrg, snap.PublishedAt, snap.Origin, changed)
		return restoreErr(err)
	}
	_, err = sp.Exec(ctx, `
		INSERT INTO hatamoto_entities (id, schema_id, name, slug, locale, status, attributes, metadata, version,
			created_by, updated_by, created_at, parent_id, author_id, byline_person_id, byline_org, published_at, origin)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)`,
		id, snap.SchemaID, snap.Name, snap.Slug, snap.Locale, snap.Status, snap.Attributes, snap.Metadata, snap.Version,
		snap.CreatedBy, snap.UpdatedBy, snap.CreatedAt, snap.ParentID, snap.AuthorID, snap.BylinePersonID, snap.BylineOrg,
		snap.PublishedAt, snap.Origin)
	return restoreErr(err)
}

func restoreErr(err error) error {
	switch {
	case err == nil:
		return nil
	case isPgUniqueViolation(err):
		return slugConflict(err)
	case isPgForeignKeyViolation(err):
		return fkViolation(err)
	}
	return fmt.Errorf("failed to restore entity: %w", err)
}

// AfterChangeset runs the side effects of a committed changeset: link-index
// refresh, the live observer and ONE batch event (import.completed with
// source ChangesetSource, never a per-entity event). Call it only after the
// transaction committed; on rollback call nothing.
func (s *Service) AfterChangeset(ctx context.Context, created, updated, deleted []uuid.UUID) {
	stored := append(append([]uuid.UUID{}, created...), updated...)
	s.reindexLinks(stored)
	s.fireLive(ctx, stored...)
	if s.events != nil && len(stored)+len(deleted) > 0 {
		s.events.Emit(ctx, Event{Kind: EventImportCompleted, Import: &ImportSummary{
			Source: ChangesetSource, Created: len(created), Updated: len(updated), IDs: stored,
		}})
	}
}

func schemaBySlugTx(ctx context.Context, tx pgx.Tx, slug string) (*EntitySchema, error) {
	var sc EntitySchema
	err := tx.QueryRow(ctx, `SELECT `+schemaColumns+` FROM hatamoto_entity_schemas WHERE slug = $1`, slug).Scan(
		&sc.ID, &sc.Name, &sc.Slug, &sc.Description, &sc.Schema, &sc.IsBuiltin, &sc.CreatedAt, &sc.UpdatedAt, &sc.Settings)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrSchemaNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get entity schema: %w", err)
	}
	return &sc, nil
}

// txStatus is the status a changeset write stores: the schema's locked status,
// else the requested one, else the fallback.
func txStatus(sc *EntitySchema, attrs json.RawMessage, want, fallback EntityStatus) (EntityStatus, error) {
	if st := lockedStatus(sc, attrs); st != nil {
		return *st, nil
	}
	if want == "" {
		return fallback, nil
	}
	if !ValidStatus(want) {
		return "", ErrInvalidStatus
	}
	return want, nil
}

// checkParentTx is checkParent on tx: the parent exists, self (nil for a new
// row) is not among its ancestors, and the nesting stays within MaxPathDepth.
func checkParentTx(ctx context.Context, tx pgx.Tx, self *uuid.UUID, parent uuid.UUID) error {
	if self != nil && *self == parent {
		return ErrParentCycle
	}
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM hatamoto_entities WHERE id = $1)`, parent).Scan(&exists); err != nil {
		return fmt.Errorf("failed to read parent: %w", err)
	}
	if !exists {
		return ErrParentNotFound
	}
	rows, err := tx.Query(ctx, `
		WITH RECURSIVE up AS (
			SELECT id, parent_id, 1 AS n FROM hatamoto_entities WHERE id = $1
			UNION ALL
			SELECT e.id, e.parent_id, up.n + 1 FROM hatamoto_entities e JOIN up ON e.id = up.parent_id WHERE up.n < $2
		) SELECT id FROM up`, parent, urlpattern.MaxPathDepth+1)
	if err != nil {
		return fmt.Errorf("failed to read ancestors: %w", err)
	}
	defer rows.Close()
	chain := 0 // the parent and its ancestors
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return err
		}
		if self != nil && id == *self {
			return ErrParentCycle
		}
		chain++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()
	below := 0
	if self != nil {
		if err := tx.QueryRow(ctx, `
			WITH RECURSIVE down AS (
				SELECT id, 0 AS n FROM hatamoto_entities WHERE id = $1
				UNION ALL
				SELECT e.id, down.n + 1 FROM hatamoto_entities e JOIN down ON e.parent_id = down.id WHERE down.n < $2
			) SELECT max(n) FROM down`, *self, urlpattern.MaxPathDepth).Scan(&below); err != nil {
			return fmt.Errorf("failed to read subtree: %w", err)
		}
	}
	if chain+1+below > urlpattern.MaxPathDepth {
		return ErrParentTooDeep
	}
	return nil
}

// freeSlugTx returns the first free slug from base, probing base, base-2, ...
// A slug is taken by any row of the schema whatever its status (draft and
// private included); for a nested schema only siblings count. attempt skips
// that many candidates (a racing insert took the previous one). The suffix
// fits the 255-char column: the base is cut, never the suffix.
func freeSlugTx(ctx context.Context, tx pgx.Tx, schemaID uuid.UUID, parent, self *uuid.UUID, base string, attempt int) (string, error) {
	for n := attempt + 1; ; n++ {
		cand := base
		if n > 1 {
			suffix := "-" + strconv.Itoa(n)
			b := base
			if len(b)+len(suffix) > maxSlugLen {
				b = b[:maxSlugLen-len(suffix)]
			}
			cand = b + suffix
		} else if len(cand) > maxSlugLen {
			cand = cand[:maxSlugLen]
		}
		var taken bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM hatamoto_entities
				WHERE schema_id = $1 AND slug = $2 AND variant_key IS NULL AND ($4::uuid IS NULL OR id <> $4)
				  AND (NOT slug_nested OR COALESCE(parent_id, '00000000-0000-0000-0000-000000000000'::uuid) = COALESCE($3::uuid, '00000000-0000-0000-0000-000000000000'::uuid)))`,
			schemaID, cand, parent, self).Scan(&taken); err != nil {
			return "", fmt.Errorf("failed to probe slug: %w", err)
		}
		if !taken {
			return cand, nil
		}
	}
}
