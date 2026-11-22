package changeset

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"hatamoto.dev/core/internal/jsonx"
)

// EditFilter selects edit changesets.
type EditFilter struct {
	Pending   bool   // staged and not rejected (the review queue)
	Requester string // "" = anyone
	TargetID  string // "" = any
}

// EditRow is one edit changeset with its first item's target and name.
type EditRow struct {
	ID        int64
	Requester string
	CreatedAt time.Time
	Stages    []StageSpec
	Approvals int
	Rejected  bool
	State     string
	TargetID  string
	Name      string // the payload's name
}

const editRowSelect = `
	SELECT c.id, c.requested_by, c.created_at, c.stages, c.state,
	       COALESCE(i.target_id, ''), COALESCE(i.payload->>'name', ''),
	       COALESCE(d.approvals, 0), COALESCE(d.rejected, false)
	FROM hatamoto_changesets c
	LEFT JOIN LATERAL (
		SELECT target_id, payload FROM hatamoto_changeset_items WHERE changeset_id = c.id ORDER BY seq LIMIT 1
	) i ON true
	LEFT JOIN LATERAL (
		SELECT count(*) FILTER (WHERE decision = 'approved') AS approvals, bool_or(decision = 'rejected') AS rejected
		FROM hatamoto_changeset_stage_decisions WHERE changeset_id = c.id
	) d ON true
	WHERE c.kind = 'edit'
	  AND (NOT $1 OR (c.state = 'staged' AND NOT COALESCE(d.rejected, false)))
	  AND ($2 = '' OR c.requested_by = $2)
	  AND ($3 = '' OR i.target_id = $3)`

func scanEditRow(row pgx.Row) (EditRow, error) {
	var r EditRow
	var stages []byte
	if err := row.Scan(&r.ID, &r.Requester, &r.CreatedAt, &stages, &r.State, &r.TargetID, &r.Name, &r.Approvals, &r.Rejected); err != nil {
		return EditRow{}, err
	}
	if len(stages) > 0 {
		if err := jsonx.Unmarshal(stages, &r.Stages); err != nil {
			return EditRow{}, fmt.Errorf("failed to read changeset stages: %w", err)
		}
	}
	return r, nil
}

// ListEdits reads edit changesets, oldest first for the pending queue and
// newest first otherwise. It never writes.
func (e *Engine) ListEdits(ctx context.Context, f EditFilter, limit, offset int) ([]EditRow, error) {
	rows, err := e.DB.Pool().Query(ctx, editRowSelect+`
		ORDER BY CASE WHEN $1 THEN c.id END ASC, c.id DESC LIMIT $4 OFFSET $5`,
		f.Pending, f.Requester, f.TargetID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to list edits: %w", err)
	}
	defer rows.Close()
	var out []EditRow
	for rows.Next() {
		r, err := scanEditRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CountPendingEdits counts the edits in the review queue (staged, not rejected).
func (e *Engine) CountPendingEdits(ctx context.Context) (int, error) {
	var n int
	err := e.DB.Pool().QueryRow(ctx, `
		SELECT count(*) FROM hatamoto_changesets c
		WHERE c.kind = 'edit' AND c.state = 'staged'
		  AND NOT EXISTS (SELECT 1 FROM hatamoto_changeset_stage_decisions d WHERE d.changeset_id = c.id AND d.decision = 'rejected')`).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("failed to count pending edits: %w", err)
	}
	return n, nil
}

// PendingEdit is the staged edit of target, rejected or not, if any: the
// requester's editor shows it over the live row.
func (e *Engine) PendingEdit(ctx context.Context, targetID string) (EditRow, bool, error) {
	if targetID == "" {
		return EditRow{}, false, nil
	}
	r, err := scanEditRow(e.DB.Pool().QueryRow(ctx, editRowSelect+`
		  AND c.state = 'staged' ORDER BY c.id DESC LIMIT 1`, false, "", targetID))
	if errors.Is(err, pgx.ErrNoRows) {
		return EditRow{}, false, nil
	}
	if err != nil {
		return EditRow{}, false, fmt.Errorf("failed to read the pending edit: %w", err)
	}
	return r, true, nil
}
