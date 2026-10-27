package taxonomy

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// Merge errors.
var (
	ErrMergeSameTerm  = errors.New("taxonomy: cannot merge a term into itself")
	ErrMergeKind      = errors.New("taxonomy: terms of different kinds cannot be merged")
	ErrMergeHasChilds = errors.New("taxonomy: the term has child terms: move or delete them first")
)

// merger is implemented by repositories that can merge two terms.
type merger interface {
	MergeTerms(ctx context.Context, from, into uuid.UUID) error
}

// MergeTerms moves every entity tagged with from onto into, then deletes from.
// Both must be of one kind and from must have no children. The archive path
// of from is reported to the move observer so it redirects to into.
func (s *Service) MergeTerms(ctx context.Context, from, into uuid.UUID) error {
	if from == into {
		return ErrMergeSameTerm
	}
	m, ok := s.repo.(merger)
	if !ok {
		return errors.New("taxonomy: merging is not supported by this store")
	}
	a, err := s.repo.GetTerm(ctx, from)
	if err != nil {
		return err
	}
	b, err := s.repo.GetTerm(ctx, into)
	if err != nil {
		return err
	}
	if a.Kind != b.Kind {
		return ErrMergeKind
	}
	var paths map[uuid.UUID]string
	if s.moved != nil {
		paths, _ = s.archivePaths(ctx, a, b)
	}
	if err := m.MergeTerms(ctx, from, into); err != nil {
		return err
	}
	if s.moved != nil && paths != nil {
		s.moved(ctx, []PathMove{{From: paths[from], To: paths[into]}})
	}
	s.notifyChanged(ctx)
	return nil
}

// MergeTerms runs the merge in one transaction.
func (r *PostgresRepository) MergeTerms(ctx context.Context, from, into uuid.UUID) error {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var children int
	if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM hatamoto_terms WHERE parent_id = $1`, from).Scan(&children); err != nil {
		return err
	}
	if children > 0 {
		return ErrMergeHasChilds
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO hatamoto_entity_terms (entity_id, term_id)
		SELECT entity_id, $2 FROM hatamoto_entity_terms WHERE term_id = $1
		ON CONFLICT DO NOTHING`, from, into); err != nil {
		return fmt.Errorf("merge terms: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM hatamoto_terms WHERE id = $1`, from); err != nil {
		return fmt.Errorf("merge terms: %w", err)
	}
	return tx.Commit(ctx)
}
