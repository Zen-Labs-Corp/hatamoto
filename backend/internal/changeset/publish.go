package changeset

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Publish applies every item of a staged changeset in one transaction, each in
// its own savepoint. An item whose live object moved since it was staged is
// skipped and listed in Result.Conflicts; any other item error rolls the whole
// publish back and leaves the changeset staged. Events, observers and file
// clean-up run only after the commit. A changeset past its expiry is closed
// and refused, so installs without the sweeper never publish stale work.
func (e *Engine) Publish(ctx context.Context, csID int64) (Result, error) {
	var (
		res   Result
		keys  []string
		undos []func(context.Context)
		stale bool
	)
	err := e.DB.WithTx(ctx, func(tx pgx.Tx) error {
		cs, err := lockChangeset(ctx, tx, csID)
		if err != nil {
			return err
		}
		if cs.State != StateStaged {
			return ErrNotStaged
		}
		if !e.now().Before(cs.ExpiresAt) {
			stale = true
			return ErrExpired
		}
		items, err := loadItems(ctx, tx, csID, false)
		if err != nil {
			return err
		}
		for _, it := range items {
			keys = append(keys, it.BlobKeys...)
			if err := e.publishItem(ctx, tx, cs.Plugin, it, &res, &undos); err != nil {
				return fmt.Errorf("item %d (%s): %w", it.Seq, it.Kind, err)
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE hatamoto_changesets SET state = 'published', published_at = $2 WHERE id = $1`, csID, e.now()); err != nil {
			return fmt.Errorf("failed to mark changeset published: %w", err)
		}
		return nil
	})
	if err != nil {
		for _, undo := range undos {
			undo(ctx)
		}
		if stale {
			if _, cerr := e.finishStaged(ctx, csID, StateExpired); cerr != nil && !errors.Is(cerr, ErrNotStaged) {
				e.logger().Warn("changeset: failed to expire", "changeset", csID, "error", cerr.Error())
			}
		}
		return Result{}, err
	}
	e.afterCommit(ctx, res.Items)
	e.deleteBlobs(ctx, keys)
	e.logger().Info("changeset published: " + res.summary())
	e.audit(ctx, "changeset.publish", csID, map[string]any{"applied": res.Applied, "skipped": res.Skipped})
	return res, nil
}

func (e *Engine) publishItem(ctx context.Context, tx pgx.Tx, plugin string, it StoredItem, res *Result, undos *[]func(context.Context)) error {
	w, ok := e.Writers[it.Kind]
	if !ok {
		return fmt.Errorf("%w: %q", ErrNoWriter, it.Kind)
	}
	target := it.TargetID
	if target == "" {
		var err error
		if target, err = mappedTarget(ctx, tx, Origin(plugin), it.Kind, it.SourceKey); err != nil {
			return err
		}
	}
	// The object may have changed hands since the item was staged.
	if err := e.checkOwnership(ctx, tx, plugin, it.Kind, target); errors.Is(err, ErrNotOwned) {
		res.Skipped++
		res.Conflicts = append(res.Conflicts, ConflictItem{Seq: it.Seq, Kind: it.Kind, SourceKey: it.SourceKey, TargetID: target, Reason: ReasonNotOwned})
		return recordResult(ctx, tx, it.ID, ResultSkippedOther, ReasonNotOwned, target, nil)
	} else if err != nil {
		return err
	}
	origin := Origin(plugin)
	out, err := w.Apply(ctx, tx, origin, it.Item, target)
	if out.Undo != nil {
		*undos = append(*undos, out.Undo)
	}
	if err != nil {
		return err
	}
	if out.Conflict {
		res.Skipped++
		res.Conflicts = append(res.Conflicts, ConflictItem{Seq: it.Seq, Kind: it.Kind, SourceKey: it.SourceKey, TargetID: target, Reason: out.Detail})
		return recordResult(ctx, tx, it.ID, ResultSkippedConflict, out.Detail, target, nil)
	}
	if err := recordResult(ctx, tx, it.ID, ResultApplied, out.Detail, out.TargetID, out.Before); err != nil {
		return err
	}
	if it.SourceKey != "" {
		if out.Deleted {
			err = dropMapping(ctx, tx, origin, it.Kind, it.SourceKey)
		} else {
			err = saveMapping(ctx, tx, origin, it.Kind, it.SourceKey, out.TargetID)
		}
		if err != nil {
			return err
		}
	}
	res.Applied++
	res.Items = append(res.Items, AppliedItem{Kind: it.Kind, TargetID: out.TargetID, Created: out.Created, Deleted: out.Deleted, Before: out.Before})
	return nil
}

func recordResult(ctx context.Context, tx pgx.Tx, itemID int64, result, detail, target string, before []byte) error {
	_, err := tx.Exec(ctx, `
		UPDATE hatamoto_changeset_items
		SET result = $2, result_detail = NULLIF($3, ''), target_id = NULLIF($4, ''), before = $5
		WHERE id = $1`, itemID, result, detail, target, before)
	if err != nil {
		return fmt.Errorf("failed to record result: %w", err)
	}
	return nil
}

// afterCommit calls each writer's After hook with the items of its kind.
func (e *Engine) afterCommit(ctx context.Context, items []AppliedItem) {
	byKind := map[Kind][]AppliedItem{}
	for _, it := range items {
		byKind[it.Kind] = append(byKind[it.Kind], it)
	}
	for k, list := range byKind {
		if w, ok := e.Writers[k]; ok {
			w.After(ctx, list)
		}
	}
}

// summary is the one-line result: "N applied, M skipped (conflict: ...)".
func (r Result) summary() string {
	s := fmt.Sprintf("%d applied, %d skipped", r.Applied, r.Skipped)
	if len(r.Conflicts) > 0 {
		parts := make([]string, len(r.Conflicts))
		for i, c := range r.Conflicts {
			parts[i] = fmt.Sprintf("%s %s: %s", c.Kind, c.TargetID, c.Reason)
		}
		s += " (conflict: " + strings.Join(parts, "; ") + ")"
	}
	return s
}
