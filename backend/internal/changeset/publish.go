package changeset

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"hatamoto.dev/core/internal/jsonx"
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
		kind    ChangesetKind
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
		kind = cs.Kind
		if !e.now().Before(cs.ExpiresAt) {
			stale = true
			return ErrExpired
		}
		if cs.Kind == ChangesetEdit {
			if err := stagesApproved(ctx, tx, cs); err != nil {
				return err
			}
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
			// A gate has judged every item itself (a standing policy refuses any
			// risky one, bar the link_host class it re-checks), so nobody decides.
			if gate == nil {
				if err := checkDecided(review); err != nil {
					return err
				}
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
			if err := e.publishItem(ctx, tx, cs, it, &res, &undos); err != nil {
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
		if e.AuditTx != nil {
			detail := publishAuditDetail(res, mode, dropped)
			detail["plugin"] = cs.Plugin
			if err := e.AuditTx(ctx, tx, publishAction(cs.Kind), fmt.Sprintf("changeset:%d", csID), detail); err != nil {
				return fmt.Errorf("audit: %w", err)
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
	if e.AuditTx == nil {
		e.audit(ctx, publishAction(kind), csID, publishAuditDetail(res, mode, dropped))
		if kind == ChangesetEdit && e.Audit != nil {
			for _, it := range res.Items {
				if it.Kind != KindEntity || it.Created || it.Deleted || len(it.Before) == 0 {
					continue
				}
				if from, err := takeOverOrigin(it.Before); err == nil && from != "" {
					e.Audit(ctx, "entity.take_over", it.TargetID, takeOverDetail(csID, from))
				}
			}
		}
	}
	return res, dropped, nil
}

// publishAction is the audit action of a published changeset: edits have their own.
func publishAction(k ChangesetKind) string {
	if k == ChangesetEdit {
		return "changeset.edit_publish"
	}
	return "changeset.publish"
}

func publishAuditDetail(res Result, mode publishMode, dropped int) map[string]any {
	detail := map[string]any{"applied": res.Applied, "skipped": res.Skipped}
	if mode == publishReviewed {
		detail["dropped"] = dropped
	}
	return detail
}

func (e *Engine) publishItem(ctx context.Context, tx pgx.Tx, cs changesetRow, it StoredItem, res *Result, undos *[]func(context.Context)) error {
	w, ok := e.Writers[it.Kind]
	if !ok {
		return fmt.Errorf("%w: %q", ErrNoWriter, it.Kind)
	}
	target := it.TargetID
	if target == "" {
		var err error
		if target, err = mappedTarget(ctx, tx, Origin(cs.Plugin), it.Kind, it.SourceKey); err != nil {
			return err
		}
	}
	// The object may have changed hands since the item was staged. A person
	// may edit an object of any origin, so an edit skips the check.
	if cs.Kind != ChangesetEdit {
		if err := e.checkOwnership(ctx, tx, ownerPlugin(cs.Plugin, it.Item), it.Kind, target); errors.Is(err, ErrNotOwned) {
			res.Skipped++
			res.Conflicts = append(res.Conflicts, ConflictItem{Seq: it.Seq, Kind: it.Kind, SourceKey: it.SourceKey, TargetID: target, Reason: ReasonNotOwned})
			return recordResult(ctx, tx, it.ID, ResultSkippedOther, ReasonNotOwned, target, nil, "")
		} else if err != nil {
			return err
		}
	}
	origin := Origin(cs.Plugin)
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
	if e.AuditTx != nil && cs.Kind == ChangesetEdit && it.Kind == KindEntity && it.Op == OpUpsert && len(out.Before) > 0 {
		from, err := takeOverOrigin(out.Before)
		if err != nil {
			return err
		}
		if from != "" {
			if err := e.AuditTx(ctx, tx, "entity.take_over", out.TargetID, takeOverDetail(cs.ID, from)); err != nil {
				return fmt.Errorf("audit: %w", err)
			}
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

// takeOverOrigin is the origin the pre-image of an edited entity had, when it
// was not the owner's: a person's edit takes the entity over (origin owner).
func takeOverOrigin(before jsontext.Value) (string, error) {
	var pre struct {
		Origin string `json:"origin"`
	}
	if err := jsonx.Unmarshal(before, &pre, jsonx.StoredLoose); err != nil {
		return "", fmt.Errorf("invalid pre-image: %w", err)
	}
	if pre.Origin == Origin("") {
		return "", nil
	}
	return pre.Origin, nil
}

// takeOverDetail is the entity.take_over audit detail of an applied edit item.
func takeOverDetail(cs int64, from string) map[string]any {
	return map[string]any{"from": from, "changeset": cs}
}
