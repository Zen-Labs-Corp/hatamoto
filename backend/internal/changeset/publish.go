package changeset

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Publish applies every item of a staged changeset in one transaction, each in
// its own savepoint. An item whose live object moved since it was staged is
// skipped and listed in Result.Conflicts; any other item error rolls the whole
// publish back and leaves the changeset staged. Events, observers and file
// clean-up run only after the commit. A changeset past its expiry is closed
// and refused, so installs without the sweeper never publish stale work.
func (e *Engine) Publish(ctx context.Context, csID int64) (Result, error) {
	res, _, err := e.publish(ctx, csID, publishAll, nil, 0)
	return res, err
}

// publishCommitTimeout bounds the COMMIT of a publish, not the work before it.
var publishCommitTimeout = 30 * time.Second

// publishMode says which items a publish applies.
type publishMode int

const (
	// publishAll applies every item, whatever its risk or decision.
	publishAll publishMode = iota
	// publishReviewed honours the decisions: dropped items are skipped, and a
	// high or critical item that is not approved refuses the publish.
	publishReviewed
)

// publishGate, when given, runs inside the publish transaction after the
// changeset is locked and found staged; an error from it rolls the publish
// back. A gate marks the published changeset automatic with approvalID.
type publishGate func(tx pgx.Tx, cs changesetRow) error

func (e *Engine) publish(ctx context.Context, csID int64, mode publishMode, gate publishGate, approvalID int64) (Result, int, error) {
	var (
		res     Result
		keys    []string
		undos   []func(context.Context)
		stale   bool
		dropped int
	)
	// Acquire, begin and the work run on the request's ctx, so a gone client
	// still stops them. Only COMMIT runs on a context the client cannot cancel:
	// a COMMIT cut off mid-flight may still have committed, and the undo below
	// would then delete files of rows that exist.
	err := e.DB.WithTxDetachedCommit(ctx, publishCommitTimeout, func(tx pgx.Tx) error {
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
		if gate != nil {
			if err := gate(tx, cs); err != nil {
				return err
			}
		}
		items, err := loadItems(ctx, tx, csID, false)
		if err != nil {
			return err
		}
		var review map[int]reviewState
		if mode == publishReviewed {
			if err := e.ensureClassified(ctx, tx, csID, cs.Plugin); err != nil {
				return err
			}
			if review, err = loadReviewStates(ctx, tx, csID); err != nil {
				return err
			}
			if err := checkDecided(review); err != nil {
				return err
			}
		}
		var applied []appliedRef
		for _, it := range items {
			keys = append(keys, it.BlobKeys...)
			if detail, drop := review[it.Seq].dropped(review); drop {
				dropped++
				if err := recordResult(ctx, tx, it.ID, ResultDropped, detail, it.TargetID, nil, ""); err != nil {
					return err
				}
				continue
			}
			n := len(res.Items)
			if err := e.publishItem(ctx, tx, cs.Plugin, it, &res, &undos); err != nil {
				return fmt.Errorf("item %d (%s): %w", it.Seq, it.Kind, err)
			}
			if len(res.Items) > n {
				applied = append(applied, appliedRef{it.ID, it.Kind, res.Items[n].TargetID})
			}
		}
		if err := recordAfterTokens(ctx, tx, applied); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE hatamoto_changesets SET state = 'published', published_at = $2 WHERE id = $1`, csID, e.now()); err != nil {
			return fmt.Errorf("failed to mark changeset published: %w", err)
		}
		if gate != nil {
			if _, err := tx.Exec(ctx, `UPDATE hatamoto_changesets SET auto_published = true, approval_id = $2 WHERE id = $1`, csID, approvalID); err != nil {
				return fmt.Errorf("failed to mark changeset automatic: %w", err)
			}
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
		return Result{}, 0, err
	}
	e.afterCommit(ctx, res.Items)
	e.deleteBlobs(ctx, keys)
	e.logger().Info("changeset published: " + res.summary())
	detail := map[string]any{"applied": res.Applied, "skipped": res.Skipped}
	if mode == publishReviewed {
		detail["dropped"] = dropped
	}
	e.audit(ctx, "changeset.publish", csID, detail)
	return res, dropped, nil
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
	if err := e.checkOwnership(ctx, tx, ownerPlugin(plugin, it.Item), it.Kind, target); errors.Is(err, ErrNotOwned) {
		res.Skipped++
		res.Conflicts = append(res.Conflicts, ConflictItem{Seq: it.Seq, Kind: it.Kind, SourceKey: it.SourceKey, TargetID: target, Reason: ReasonNotOwned})
		return recordResult(ctx, tx, it.ID, ResultSkippedOther, ReasonNotOwned, target, nil, "")
	} else if err != nil {
		return err
	}
	origin := Origin(plugin)
	beforeTok := ""
	if target != "" {
		var err error
		if beforeTok, err = liveToken(ctx, tx, it.Kind, target); err != nil {
			return err
		}
	}
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
		return recordResult(ctx, tx, it.ID, ResultSkippedConflict, out.Detail, target, nil, "")
	}
	if err := recordResult(ctx, tx, it.ID, ResultApplied, out.Detail, out.TargetID, out.Before, beforeTok); err != nil {
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
	res.Items = append(res.Items, AppliedItem{Kind: it.Kind, TargetID: out.TargetID, Created: out.Created, Deleted: out.Deleted, Before: out.Before, PathMoved: out.PathMoved})
	return nil
}

func recordResult(ctx context.Context, tx pgx.Tx, itemID int64, result, detail, target string, before []byte, beforeToken string) error {
	_, err := tx.Exec(ctx, `
		UPDATE hatamoto_changeset_items
		SET result = $2, result_detail = NULLIF($3, ''), target_id = NULLIF($4, ''), before = $5, before_token = NULLIF($6, '')
		WHERE id = $1`, itemID, result, detail, target, before, beforeToken)
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
