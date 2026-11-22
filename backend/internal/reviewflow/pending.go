package reviewflow

import (
	"context"

	"hatamoto.dev/core/internal/changeset"
	"hatamoto.dev/core/internal/reviewstate"
)

// PendingReader is the engine read behind review.Pending.
type PendingReader interface {
	PendingStages(ctx context.Context, limit int) ([]changeset.PendingStageRow, error)
}

// PendingSource maps the engine's pending stages to the public rows, for reviewstate.SetPendingSource.
func PendingSource(r PendingReader) reviewstate.PendingSource {
	return func(ctx context.Context, limit int) ([]reviewstate.PendingStage, error) {
		rows, err := r.PendingStages(ctx, limit)
		if err != nil {
			return nil, err
		}
		out := make([]reviewstate.PendingStage, 0, len(rows))
		for _, row := range rows {
			if row.Approvals >= len(row.Stages) { // defensive: the query already excludes these
				continue
			}
			st := row.Stages[row.Approvals]
			entered := row.CreatedAt
			if row.Approvals > 0 && row.LastApproval != nil {
				entered = *row.LastApproval
			}
			out = append(out, reviewstate.PendingStage{
				ChangesetID: row.ID,
				EntityID:    row.TargetID,
				EntityName:  row.Name,
				Schema:      row.Schema,
				Requester:   row.Requester,
				Stage:       reviewstate.Stage{Name: st.Name, Group: st.Group},
				Index:       row.Approvals,
				Total:       len(row.Stages),
				RequestedAt: row.CreatedAt.UTC(),
				EnteredAt:   entered.UTC(),
			})
		}
		return out, nil
	}
}
