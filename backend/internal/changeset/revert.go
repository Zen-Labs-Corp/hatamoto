package changeset

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Revert undoes a published changeset in one transaction, last item first:
// created objects are removed, updated and deleted ones are restored from the
// pre-image stored at publish. Any item error rolls the whole revert back.
func (e *Engine) Revert(ctx context.Context, csID int64) (Result, error) {
	var (
		res   Result
		posts []func(context.Context)
	)
	err := e.DB.WithTx(ctx, func(tx pgx.Tx) error {
		cs, err := lockChangeset(ctx, tx, csID)
		if err != nil {
			return err
		}
		if cs.State != StatePublished {
			return ErrNotPublished
		}
		items, err := loadItems(ctx, tx, csID, true)
		if err != nil {
			return err
		}
		origin := Origin(cs.Plugin)
		for _, it := range items {
			if it.Result != ResultApplied {
				continue
			}
			w, ok := e.Writers[it.Kind]
			if !ok {
				return fmt.Errorf("item %d: %w: %q", it.Seq, ErrNoWriter, it.Kind)
			}
			post, err := w.Restore(ctx, tx, it)
			if errors.Is(err, ErrRevertConflict) {
				res.Skipped++
				res.Conflicts = append(res.Conflicts, ConflictItem{Seq: it.Seq, Kind: it.Kind, SourceKey: it.SourceKey, TargetID: it.TargetID, Reason: reasonRevertMoved})
				continue
			}
			if err != nil {
				return fmt.Errorf("item %d (%s): %w", it.Seq, it.Kind, err)
			}
			if post != nil {
				posts = append(posts, post)
			}
			created := it.Before == nil
			if it.SourceKey != "" {
				if created {
					err = dropMapping(ctx, tx, origin, it.Kind, it.SourceKey)
				} else {
					err = saveMapping(ctx, tx, origin, it.Kind, it.SourceKey, it.TargetID)
				}
				if err != nil {
					return err
				}
			}
			res.Applied++
			// The reverse of a create is a delete; of a delete, a create.
			res.Items = append(res.Items, AppliedItem{Kind: it.Kind, TargetID: it.TargetID, Created: !created && it.Op == OpDelete, Deleted: created})
		}
		if _, err := tx.Exec(ctx, `UPDATE hatamoto_changesets SET state = 'reverted', reverted_at = $2 WHERE id = $1`, csID, e.now()); err != nil {
			return fmt.Errorf("failed to mark changeset reverted: %w", err)
		}
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	e.afterCommit(ctx, res.Items)
	for _, post := range posts {
		post(ctx)
	}
	e.logger().Info(fmt.Sprintf("changeset reverted: %d restored", res.Applied))
	e.audit(ctx, "changeset.revert", csID, map[string]any{"restored": res.Applied})
	return res, nil
}
