package changeset

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"hatamoto.dev/core/internal/jsonx"
)

// ErrEditPending is returned when another person's edit of the same object is waiting for review.
var ErrEditPending = errors.New("another edit of this object is waiting for review")

// SubmitEdit stages one item as a human edit in one transaction. It takes an
// advisory lock on the item's target so two submissions cannot both find no
// pending edit, closes the requester's own staged edit of the target as
// discarded (a resubmission supersedes it), refuses another requester's, and
// records changeset.edit_submit through AuditTx inside the transaction. It
// returns the new changeset's id.
func (e *Engine) SubmitEdit(ctx context.Context, o EditOptions, it Item) (int64, error) {
	if strings.TrimSpace(o.Requester) == "" {
		return 0, fmt.Errorf("%w: no requester", ErrInvalidEdit)
	}
	stages := o.Stages
	if stages == nil {
		stages = []StageSpec{}
	}
	for _, s := range stages {
		if strings.TrimSpace(s.Name) == "" || strings.TrimSpace(s.Group) == "" {
			return 0, fmt.Errorf("%w: stage without name or group", ErrInvalidEdit)
		}
	}
	rawStages, err := jsonx.Marshal(stages)
	if err != nil {
		return 0, fmt.Errorf("failed to encode stages: %w", err)
	}
	if it.TargetID == "" {
		return 0, fmt.Errorf("%w: an edit needs a target", ErrInvalidItem)
	}
	if len(it.BlobKeys) > 0 {
		return 0, fmt.Errorf("%w: an edit carries no files", ErrInvalidItem)
	}
	payload, keys, err := e.checkItem(it)
	if err != nil {
		return 0, err
	}
	if int64(len(payload)) > maxStagedBytes {
		return 0, fmt.Errorf("%w: at most %d MiB", ErrQuotaExceeded, maxStagedBytes>>20)
	}

	var id, superseded int64
	var oldKeys []string
	err = e.DB.WithTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('hatamoto:edit:' || $1, 0))`, it.TargetID); err != nil {
			return fmt.Errorf("failed to lock the edit target: %w", err)
		}
		var oldID int64
		var oldBy string
		err := tx.QueryRow(ctx, `
			SELECT c.id, c.requested_by FROM hatamoto_changesets c
			JOIN hatamoto_changeset_items i ON i.changeset_id = c.id
			WHERE c.kind = 'edit' AND c.state = 'staged' AND i.target_id = $1
			ORDER BY c.id LIMIT 1 FOR UPDATE OF c`, it.TargetID).Scan(&oldID, &oldBy)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			return fmt.Errorf("failed to look up the pending edit: %w", err)
		case oldBy != o.Requester:
			return ErrEditPending
		default:
			if oldKeys, err = closeStaged(ctx, tx, oldID, StateDiscarded); err != nil {
				return err
			}
			superseded = oldID
		}
		runID := uuid.New()
		if _, err := tx.Exec(ctx, `INSERT INTO hatamoto_runs (id, plugin, state) VALUES ($1, '', 'finished')`, runID); err != nil {
			return fmt.Errorf("failed to create run: %w", err)
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO hatamoto_changesets (run_id, plugin, expires_at, kind, requested_by, stages)
			VALUES ($1, '', $2, 'edit', $3, $4) RETURNING id`,
			runID, NoExpiry, o.Requester, rawStages).Scan(&id); err != nil {
			return fmt.Errorf("failed to create the edit changeset: %w", err)
		}
		if err := insertItem(ctx, tx, id, it, payload, keys); err != nil {
			return err
		}
		if e.AuditTx != nil {
			return e.AuditTx(ctx, tx, "changeset.edit_submit", fmt.Sprintf("changeset:%d", id),
				map[string]any{"target": it.TargetID, "stages": len(stages), "superseded": superseded})
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	e.deleteBlobs(ctx, oldKeys)
	if e.AuditTx == nil {
		e.audit(ctx, "changeset.edit_submit", id,
			map[string]any{"target": it.TargetID, "stages": len(stages), "superseded": superseded})
	}
	return id, nil
}
