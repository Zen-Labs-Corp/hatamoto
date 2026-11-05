package taxonomy

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// MoveInput says where a term goes: under another parent (or the root) and at
// a 1-based position among its siblings (same kind and parent). A Position
// below 1 means last.
type MoveInput struct {
	ParentID    *uuid.UUID
	ClearParent bool
	Position    int
}

// MoveResult is where a move put the term: Position of Total siblings.
type MoveResult struct {
	Term     *Term
	Position int
	Total    int
}

// ErrOrderUnsupported is returned when the store cannot keep a manual order.
var ErrOrderUnsupported = errors.New("taxonomy: manual order is not supported by this store")

// OrderStore keeps the manual order. PostgresRepository implements it; the
// service uses it when its repository does.
type OrderStore interface {
	// SetPosition puts id at pos (1-based, clamped; below 1 is last) among the
	// terms of its kind and parent and renumbers them. It reports the final
	// position and the number of siblings. It leaves updated_at alone: a
	// reorder is not a content change.
	SetPosition(ctx context.Context, id uuid.UUID, pos int) (placed, total int, err error)
}

// MoveTerm re-parents (when the parent changes) and orders a term among its
// siblings. A re-parent goes through UpdateTerm: one implementation of the
// kind, cycle and depth checks, of the archive redirects and of the purge.
func (s *Service) MoveTerm(ctx context.Context, id uuid.UUID, in MoveInput) (*MoveResult, error) {
	os, ok := s.repo.(OrderStore)
	if !ok {
		return nil, ErrOrderUnsupported
	}
	existing, err := s.repo.GetTerm(ctx, id)
	if err != nil {
		return nil, err
	}
	if in.ClearParent && existing.ParentID != nil || !in.ClearParent && in.ParentID != nil && !sameParent(existing.ParentID, in.ParentID) {
		// UpdateTerm re-derives an empty slug from the name: pass both as stored.
		if _, err := s.UpdateTerm(ctx, UpdateTermInput{
			ID: id, Name: existing.Name, Slug: existing.Slug, ParentID: in.ParentID, ClearParent: in.ClearParent,
		}); err != nil {
			return nil, err
		}
	}
	placed, total, err := os.SetPosition(ctx, id, in.Position)
	if err != nil {
		return nil, err
	}
	s.notifyChanged(ctx)
	t, err := s.repo.GetTerm(ctx, id)
	if err != nil {
		return nil, err
	}
	return &MoveResult{Term: t, Position: placed, Total: total}, nil
}

// SetPosition implements OrderStore.
func (r *PostgresRepository) SetPosition(ctx context.Context, id uuid.UUID, pos int) (int, int, error) {
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

	var kind string
	var parent *uuid.UUID
	err = tx.QueryRow(ctx, `SELECT kind, parent_id FROM hatamoto_terms WHERE id = $1`, id).Scan(&kind, &parent)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, 0, ErrTermNotFound
	}
	if err != nil {
		return 0, 0, fmt.Errorf("read term: %w", err)
	}
	// Lock the sibling set in id order (no deadlock between two editors), then
	// read it in the manual order.
	if _, err := tx.Exec(ctx, `SELECT 1 FROM hatamoto_terms
		WHERE kind = $1 AND parent_id IS NOT DISTINCT FROM $2::uuid ORDER BY id FOR UPDATE`, kind, parent); err != nil {
		return 0, 0, fmt.Errorf("lock siblings: %w", err)
	}
	rows, err := tx.Query(ctx, `SELECT id FROM hatamoto_terms
		WHERE kind = $1 AND parent_id IS NOT DISTINCT FROM $2::uuid ORDER BY sort_order, name, id`, kind, parent)
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
	if _, err := tx.Exec(ctx, `UPDATE hatamoto_terms t SET sort_order = v.n
		FROM unnest($1::uuid[], $2::int[]) AS v(id, n)
		WHERE t.id = v.id AND t.sort_order <> v.n`, ordered, nums); err != nil {
		return 0, 0, fmt.Errorf("renumber siblings: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, 0, fmt.Errorf("commit: %w", err)
	}
	return pos, total, nil
}
