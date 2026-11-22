package changeset

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Stage decisions of an edit changeset. They are separate from the item level
// risk decisions (DecisionApproved, DecisionDropped).
const (
	StageApproved = "approved"
	StageRejected = "rejected"
)

// Errors of the stage decisions.
var (
	ErrNotEdit       = errors.New("changeset is not an edit")
	ErrStageOrder    = errors.New("stage decided out of order")
	ErrStageRejected = errors.New("edit was rejected at an earlier stage")
	ErrStagesPending = errors.New("edit has stages without approval")
	// ErrSelfDecision means the requester tried to decide a stage of their own edit.
	ErrSelfDecision = errors.New("the requester cannot decide their own edit")
)

// StageDecision is one reviewer's answer for one stage of an edit changeset.
type StageDecision struct {
	Stage    int
	Reviewer string
	Decision string
	Comment  string
	At       time.Time
}

// DecideStage records d on a staged edit changeset. Stages are decided in
// order: d.Stage must be the number of approvals so far and below the number
// of stages, and nothing is accepted after a rejection. That the reviewer
// belongs to the stage's group is the caller's check; that the reviewer is not
// the requester is checked here (ErrSelfDecision). With AuditTx set the audit
// row is written in the transaction and an error from it rolls the decision
// back; otherwise it is recorded after the commit.
func (e *Engine) DecideStage(ctx context.Context, csID int64, d StageDecision) error {
	if strings.TrimSpace(d.Reviewer) == "" || (d.Decision != StageApproved && d.Decision != StageRejected) {
		return fmt.Errorf("%w: a stage decision needs a reviewer and approved or rejected", ErrInvalidEdit)
	}
	detail := map[string]any{"stage": d.Stage, "decision": d.Decision, "has_comment": strings.TrimSpace(d.Comment) != ""}
	err := e.DB.WithTx(ctx, func(tx pgx.Tx) error {
		cs, err := lockChangeset(ctx, tx, csID)
		if err != nil {
			return err
		}
		if cs.State != StateStaged {
			return ErrNotStaged
		}
		if cs.Kind != ChangesetEdit {
			return ErrNotEdit
		}
		if d.Reviewer == cs.Requester {
			return ErrSelfDecision
		}
		approvals, rejected, err := countStages(ctx, tx, csID)
		if err != nil {
			return err
		}
		if rejected {
			return ErrStageRejected
		}
		if d.Stage != approvals || d.Stage >= len(cs.Stages) {
			return fmt.Errorf("%w: stage %d, %d approved of %d", ErrStageOrder, d.Stage, approvals, len(cs.Stages))
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO hatamoto_changeset_stage_decisions (changeset_id, stage, reviewer, decision, comment, decided_at)
			VALUES ($1, $2, $3, $4, $5, $6)`, csID, d.Stage, d.Reviewer, d.Decision, d.Comment, e.now()); err != nil {
			return fmt.Errorf("failed to record the stage decision: %w", err)
		}
		if e.AuditTx != nil {
			return e.AuditTx(ctx, tx, "changeset.stage_decision", fmt.Sprintf("changeset:%d", csID), detail)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if e.AuditTx == nil {
		e.audit(ctx, "changeset.stage_decision", csID, detail)
	}
	return nil
}

// StageDecisions reads the decisions of a changeset in stage order. It never
// writes.
func (e *Engine) StageDecisions(ctx context.Context, csID int64) ([]StageDecision, error) {
	rows, err := e.DB.Pool().Query(ctx, `
		SELECT stage, reviewer, decision, comment, decided_at
		FROM hatamoto_changeset_stage_decisions WHERE changeset_id = $1 ORDER BY stage`, csID)
	if err != nil {
		return nil, fmt.Errorf("failed to read stage decisions: %w", err)
	}
	defer rows.Close()
	var out []StageDecision
	for rows.Next() {
		var d StageDecision
		if err := rows.Scan(&d.Stage, &d.Reviewer, &d.Decision, &d.Comment, &d.At); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func countStages(ctx context.Context, tx pgx.Tx, csID int64) (approvals int, rejected bool, err error) {
	err = tx.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE decision = 'approved'), COALESCE(bool_or(decision = 'rejected'), false)
		FROM hatamoto_changeset_stage_decisions WHERE changeset_id = $1`, csID).Scan(&approvals, &rejected)
	if err != nil {
		return 0, false, fmt.Errorf("failed to count stage decisions: %w", err)
	}
	return approvals, rejected, nil
}

// stagesApproved is the publish gate of an edit changeset: every stage must
// be approved. Zero stages means it may publish at once.
func stagesApproved(ctx context.Context, tx pgx.Tx, cs changesetRow) error {
	approvals, rejected, err := countStages(ctx, tx, cs.ID)
	if err != nil {
		return err
	}
	switch {
	case rejected:
		return ErrStageRejected
	case approvals < len(cs.Stages):
		return fmt.Errorf("%w: %d approved of %d", ErrStagesPending, approvals, len(cs.Stages))
	}
	return nil
}
