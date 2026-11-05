package standing

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"fmt"
)

// SchemaLookup returns the current JSON Schema of an entity schema slug, or
// ok=false when the schema no longer exists.
type SchemaLookup func(ctx context.Context, slug string) (raw jsontext.Value, ok bool, err error)

// VoidChangedSchemas ends every active approval that names a schema whose
// current hash differs from the one approved, or that no longer exists, and
// returns how many it ended. Approvals naming only unchanged schemas stay. A
// schema that cannot be read or hashed counts as changed (fail closed); the
// first lookup error is returned after every approval was handled.
//
// The entity schema callback carries no slug, so this compares every named
// schema instead of trusting the caller to say which one changed. The hash
// compare in the publish gate stays the hard guarantee.
func (s *Store) VoidChangedSchemas(ctx context.Context, lookup SchemaLookup) (int64, error) {
	rows, err := s.DB.Pool().Query(ctx,
		`SELECT `+approvalColumns+` FROM hatamoto_standing_approvals WHERE voided_at IS NULL`)
	if err != nil {
		return 0, fmt.Errorf("failed to list approvals: %w", err)
	}
	var active []Approval
	for rows.Next() {
		a, err := scanApproval(rows)
		if err != nil {
			rows.Close()
			return 0, err
		}
		active = append(active, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("failed to list approvals: %w", err)
	}

	var voided int64
	var firstErr error
	for _, a := range active {
		if len(a.Policy.Schemas) == 0 {
			continue
		}
		changed := false
		for _, slug := range a.Policy.Schemas {
			raw, ok, err := lookup(ctx, slug)
			if err != nil && firstErr == nil {
				firstErr = fmt.Errorf("failed to read schema %q: %w", slug, err)
			}
			hash := ""
			if err == nil && ok {
				if h, herr := SchemaHash(raw); herr == nil {
					hash = h
				}
			}
			if hash == "" || hash != a.FP.SchemaHashes[slug] {
				changed = true
				break
			}
		}
		if !changed {
			continue
		}
		tag, err := s.DB.Pool().Exec(ctx,
			`UPDATE hatamoto_standing_approvals SET voided_at = $2, void_reason = 'schema changed'
			 WHERE id = $1 AND voided_at IS NULL`, a.ID, s.now())
		if err != nil {
			return voided, errors.Join(firstErr, fmt.Errorf("failed to void approval: %w", err))
		}
		voided += tag.RowsAffected()
	}
	return voided, firstErr
}
