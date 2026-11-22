package changeset

import (
	"context"
	"fmt"
	"time"

	"hatamoto.dev/core/internal/jsonx"
)

// PendingStageRow is one staged edit waiting at a review stage.
type PendingStageRow struct {
	ID           int64
	Requester    string
	CreatedAt    time.Time
	Stages       []StageSpec
	Approvals    int
	LastApproval *time.Time // decided_at of the newest approval; nil when none
	TargetID     string
	Name         string // the first item's payload name
	Schema       string // the first item's payload schema slug
}

// PendingStages reads the edits waiting at a stage (staged, not sent back, a
// stage left to approve), oldest first. It never writes.
func (e *Engine) PendingStages(ctx context.Context, limit int) ([]PendingStageRow, error) {
	rows, err := e.DB.Pool().Query(ctx, `
		SELECT c.id, c.requested_by, c.created_at, c.stages,
		       COALESCE(i.target_id, ''), COALESCE(i.payload->>'name', ''), COALESCE(i.payload->>'schema', ''),
		       COALESCE(d.approvals, 0), d.last_approval
		FROM hatamoto_changesets c
		LEFT JOIN LATERAL (
			SELECT target_id, payload FROM hatamoto_changeset_items WHERE changeset_id = c.id ORDER BY seq LIMIT 1
		) i ON true
		LEFT JOIN LATERAL (
			SELECT count(*) FILTER (WHERE decision = 'approved') AS approvals,
			       max(decided_at) FILTER (WHERE decision = 'approved') AS last_approval,
			       bool_or(decision = 'rejected') AS rejected
			FROM hatamoto_changeset_stage_decisions WHERE changeset_id = c.id
		) d ON true
		WHERE c.kind = 'edit' AND c.state = 'staged' AND NOT COALESCE(d.rejected, false)
		  AND COALESCE(d.approvals, 0) < COALESCE(jsonb_array_length(c.stages), 0)
		ORDER BY c.id ASC LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to list pending stages: %w", err)
	}
	defer rows.Close()
	var out []PendingStageRow
	for rows.Next() {
		var r PendingStageRow
		var stages []byte
		if err := rows.Scan(&r.ID, &r.Requester, &r.CreatedAt, &stages, &r.TargetID, &r.Name, &r.Schema, &r.Approvals, &r.LastApproval); err != nil {
			return nil, fmt.Errorf("failed to list pending stages: %w", err)
		}
		if len(stages) > 0 {
			if err := jsonx.Unmarshal(stages, &r.Stages); err != nil {
				return nil, fmt.Errorf("failed to read changeset stages: %w", err)
			}
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to list pending stages: %w", err)
	}
	return out, nil
}
