package entity

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// MoveInput says where an entity goes: under another parent (or the root) and
// at a 1-based position among its siblings. A Position below 1 means last.
type MoveInput struct {
	// ParentID re-parents the entity; ClearParent moves it to the root.
	ParentID    *uuid.UUID
	ClearParent bool
	Position    int
}

// MoveResult is where a move put the entity: Position of Total siblings.
type MoveResult struct {
	Entity   *Entity
	Position int
	Total    int
}

// OrderFunc is called after the order of siblings changed.
type OrderFunc func(ctx context.Context)

// ErrOrderUnsupported is returned when the store cannot keep a manual order.
var ErrOrderUnsupported = errors.New("manual order is not supported by this store")

// OrderStore keeps the manual order. PostgresEntityRepository implements it;
// the service uses it when its repository does.
type OrderStore interface {
	// SetPosition puts id at pos (1-based, clamped; below 1 is last) among its
	// siblings (same schema, parent and locale, no variants or redirect
	// stubs) and renumbers them. It reports the final position and the number
	// of siblings. It never touches origin, version, versions, updated_by or
	// updated_at.
	SetPosition(ctx context.Context, id uuid.UUID, pos int) (placed, total int, err error)
	// OrderRows returns every sibling-eligible row of a schema and locale,
	// ordered by parent, then manual order, created_at, id. Only the
	// identifying fields, parent_id and sort_order are loaded.
	OrderRows(ctx context.Context, schemaID uuid.UUID, locale string) ([]*Entity, error)
}

// SetOrderObserver installs the callback run after a reorder; nil disables it.
// The server wires it to the rendered-page purge. A reorder emits no entity
// event: it changes no content.
func (s *Service) SetOrderObserver(f OrderFunc) { s.orderChanged = f }

func (s *Service) fireOrder(ctx context.Context) {
	if s.orderChanged != nil {
		s.orderChanged(ctx)
	}
}

// OrderRows lists the rows an owner can order for one schema and locale.
func (s *Service) OrderRows(ctx context.Context, schemaID uuid.UUID, locale string) ([]*Entity, error) {
	os, ok := s.entityRepo.(OrderStore)
	if !ok {
		return nil, ErrOrderUnsupported
	}
	return os.OrderRows(ctx, schemaID, locale)
}

// MoveEntity re-parents (when the parent changes) and orders an entity among
// its siblings.
//
// A pure order change is ownership-free: it writes no version and leaves a
// plugin's entity with the plugin. A re-parent goes through UpdateEntity (one
// implementation of the cycle and depth checks, move redirects and link
// rewrites), so it behaves like an editor save: the URL changes and a plugin's
// entity is handed to its owner.
func (s *Service) MoveEntity(ctx context.Context, id uuid.UUID, userID *uuid.UUID, in MoveInput) (*MoveResult, error) {
	os, ok := s.entityRepo.(OrderStore)
	if !ok {
		return nil, ErrOrderUnsupported
	}
	existing, err := s.entityRepo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	var tookOwnership bool
	if reparents(existing.ParentID, in) {
		upd := &UpdateEntityInput{ParentID: in.ParentID, ClearParent: in.ClearParent}
		moved, err := s.UpdateEntity(ctx, id, userID, upd)
		if err != nil {
			return nil, err
		}
		tookOwnership = moved.TookOwnership
	}
	placed, total, err := os.SetPosition(ctx, id, in.Position)
	if err != nil {
		return nil, err
	}
	s.fireOrder(ctx)
	e, err := s.entityRepo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	e.TookOwnership = tookOwnership
	return &MoveResult{Entity: e, Position: placed, Total: total}, nil
}

// reparents reports whether in moves an entity under a parent other than cur.
func reparents(cur *uuid.UUID, in MoveInput) bool {
	if in.ClearParent {
		return cur != nil
	}
	return in.ParentID != nil && !sameUUIDPtr(cur, in.ParentID)
}

// SetPosition implements OrderStore.
func (r *PostgresEntityRepository) SetPosition(ctx context.Context, id uuid.UUID, pos int) (int, int, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return 0, 0, err
	}
	defer conn.Release()
	tx, err := conn.Begin(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx)

	var schemaID uuid.UUID
	var parent *uuid.UUID
	var locale string
	err = tx.QueryRow(ctx, `SELECT schema_id, parent_id, locale FROM hatamoto_entities
		WHERE id = $1 AND variant_key IS NULL AND status <> 'redirected'`, id).Scan(&schemaID, &parent, &locale)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, 0, ErrEntityNotFound
	}
	if err != nil {
		return 0, 0, fmt.Errorf("read entity: %w", err)
	}

	// Lock the whole sibling set, in id order so two editors queue up instead of
	// deadlocking, then read it in the manual order.
	if _, err := tx.Exec(ctx, `SELECT 1 FROM hatamoto_entities
		WHERE schema_id = $1 AND parent_id IS NOT DISTINCT FROM $2::uuid AND locale = $3
		  AND variant_key IS NULL AND status <> 'redirected'
		ORDER BY id FOR UPDATE`, schemaID, parent, locale); err != nil {
		return 0, 0, fmt.Errorf("lock siblings: %w", err)
	}
	rows, err := tx.Query(ctx, `SELECT id FROM hatamoto_entities
		WHERE schema_id = $1 AND parent_id IS NOT DISTINCT FROM $2::uuid AND locale = $3
		  AND variant_key IS NULL AND status <> 'redirected'
		ORDER BY sort_order, created_at, id`, schemaID, parent, locale)
	if err != nil {
		return 0, 0, fmt.Errorf("read siblings: %w", err)
	}
	var ids []uuid.UUID
	for rows.Next() {
		var sib uuid.UUID
		if err := rows.Scan(&sib); err != nil {
			rows.Close()
			return 0, 0, fmt.Errorf("scan sibling: %w", err)
		}
		if sib != id {
			ids = append(ids, sib)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, 0, fmt.Errorf("read siblings: %w", err)
	}

	total := len(ids) + 1
	if pos < 1 || pos > total {
		pos = total
	}
	ordered := make([]uuid.UUID, 0, total)
	ordered = append(ordered, ids[:pos-1]...)
	ordered = append(ordered, id)
	ordered = append(ordered, ids[pos-1:]...)
	nums := make([]int32, total)
	for i := range nums {
		nums[i] = int32(i + 1)
	}

	// hatamoto.import keeps updated_at (sitemap lastmod, dateModified) where it
	// is; unchanged rows are skipped, so one move writes only the rows between.
	if _, err := tx.Exec(ctx, `SELECT set_config('hatamoto.import', 'on', true)`); err != nil {
		return 0, 0, fmt.Errorf("keep updated_at: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE hatamoto_entities e SET sort_order = v.n
		FROM unnest($1::uuid[], $2::int[]) AS v(id, n)
		WHERE e.id = v.id AND e.sort_order <> v.n`, ordered, nums); err != nil {
		return 0, 0, fmt.Errorf("renumber siblings: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, 0, fmt.Errorf("commit: %w", err)
	}
	return pos, total, nil
}

// OrderRows implements OrderStore.
func (r *PostgresEntityRepository) OrderRows(ctx context.Context, schemaID uuid.UUID, locale string) ([]*Entity, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()
	rows, err := conn.Query(ctx, `SELECT id, schema_id, name, slug, locale, status, parent_id, sort_order, created_at
		FROM hatamoto_entities
		WHERE schema_id = $1 AND locale = $2 AND variant_key IS NULL AND status <> 'redirected'
		ORDER BY parent_id NULLS FIRST, sort_order, created_at, id`, schemaID, locale)
	if err != nil {
		return nil, fmt.Errorf("list rows to order: %w", err)
	}
	defer rows.Close()
	var out []*Entity
	for rows.Next() {
		var e Entity
		if err := rows.Scan(&e.ID, &e.SchemaID, &e.Name, &e.Slug, &e.Locale, &e.Status, &e.ParentID, &e.SortOrder, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan row to order: %w", err)
		}
		out = append(out, &e)
	}
	return out, rows.Err()
}
