package changeset

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"
)

// Decisions a person records on an item before the changeset publishes.
const (
	DecisionApproved = "approved"
	DecisionDropped  = "dropped"
	// ResultDropped is the result of an item a reviewed publish left out.
	ResultDropped = "dropped"
	// detailTargetDropped is why a grouped redirect was dropped: its page was.
	detailTargetDropped = "target_dropped"
)

// Errors of the review flow.
var (
	// ErrUndecided means a high or critical item has no decision yet.
	ErrUndecided = errors.New("changeset has undecided risky items")
	// ErrNotDecidable means the decision would split a redirect group.
	ErrNotDecidable = errors.New("item cannot be decided that way")
)

// PublishResult is what PublishReviewed did: the Result of the applied items
// and how many items the reviewer dropped.
type PublishResult struct {
	Result
	Dropped int
}

// PublishReviewed publishes like Publish but honours the decisions. Items are
// classified first (inside the transaction, under the lock). Dropped items are
// not applied and are recorded with result "dropped"; a redirect grouped with a
// dropped page is dropped too. A high or critical item that is not approved
// refuses the whole publish with ErrUndecided and nothing is written.
func (e *Engine) PublishReviewed(ctx context.Context, csID int64) (PublishResult, error) {
	res, dropped, err := e.publish(ctx, csID, publishReviewed, nil, 0)
	return PublishResult{Result: res, Dropped: dropped}, err
}

// SetDecisions records decision (approved, dropped, or "" to clear it) on the
// items with the given seqs of a staged changeset. Redirects grouped with the
// same page are decided together: a set that holds some but not all of a group
// is refused with ErrNotDecidable.
func (e *Engine) SetDecisions(ctx context.Context, csID int64, seqs []int, decision string) error {
	if decision != "" && decision != DecisionApproved && decision != DecisionDropped {
		return fmt.Errorf("%w: decision %q", ErrInvalidItem, decision)
	}
	return e.DB.WithTx(ctx, func(tx pgx.Tx) error {
		cs, err := lockChangeset(ctx, tx, csID)
		if err != nil {
			return err
		}
		if cs.State != StateStaged {
			return ErrNotStaged
		}
		if err := e.ensureClassified(ctx, tx, csID, cs.Plugin); err != nil {
			return err
		}
		states, err := loadReviewStates(ctx, tx, csID)
		if err != nil {
			return err
		}
		picked := map[int]bool{}
		for _, s := range seqs {
			if _, ok := states[s]; !ok {
				return fmt.Errorf("%w: no item %d", ErrInvalidItem, s)
			}
			picked[s] = true
		}
		groups := map[int][]int{}
		for seq, st := range states {
			if st.reason == ReasonRedirect && st.ref > 0 {
				groups[st.ref] = append(groups[st.ref], seq)
			}
		}
		for _, g := range groups {
			n := 0
			for _, s := range g {
				if picked[s] {
					n++
				}
			}
			if n != 0 && n != len(g) {
				return fmt.Errorf("%w: redirects of one page are decided together", ErrNotDecidable)
			}
		}
		list := make([]int32, 0, len(picked))
		for s := range picked {
			list = append(list, int32(s))
		}
		slices.Sort(list)
		if _, err := tx.Exec(ctx, `UPDATE hatamoto_changeset_items SET decision = NULLIF($3, '') WHERE changeset_id = $1 AND seq = ANY($2)`,
			csID, list, decision); err != nil {
			return fmt.Errorf("failed to set decisions: %w", err)
		}
		return nil
	})
}

// reviewState is the risk and decision of one item.
type reviewState struct {
	risk     Risk
	reason   RiskReason
	ref      int
	decision string
}

func loadReviewStates(ctx context.Context, tx rowsQuerier, csID int64) (map[int]reviewState, error) {
	rows, err := tx.Query(ctx, `
		SELECT seq, COALESCE(risk, ''), COALESCE(risk_reason, ''), COALESCE(risk_ref, 0), COALESCE(decision, '')
		FROM hatamoto_changeset_items WHERE changeset_id = $1`, csID)
	if err != nil {
		return nil, fmt.Errorf("failed to read decisions: %w", err)
	}
	defer rows.Close()
	out := map[int]reviewState{}
	for rows.Next() {
		var seq int
		var st reviewState
		var risk, reason string
		if err := rows.Scan(&seq, &risk, &reason, &st.ref, &st.decision); err != nil {
			return nil, err
		}
		st.risk, st.reason = Risk(risk), RiskReason(reason)
		out[seq] = st
	}
	return out, rows.Err()
}

// dropped reports whether the item is left out of a reviewed publish, and the
// detail to record: its own drop, or the drop of the page it is grouped with.
// A zero state (publishAll) is never dropped.
func (s reviewState) dropped(all map[int]reviewState) (detail string, drop bool) {
	if s.decision == DecisionDropped {
		return "", true
	}
	if s.reason == ReasonRedirect && s.ref > 0 && all[s.ref].decision == DecisionDropped {
		return detailTargetDropped, true
	}
	return "", false
}

// checkDecided returns ErrUndecided when an item that would be applied is high
// or critical and not approved.
func checkDecided(states map[int]reviewState) error {
	for _, st := range states {
		if st.risk != RiskHigh && st.risk != RiskCritical || st.decision == DecisionApproved {
			continue
		}
		if _, drop := st.dropped(states); !drop {
			return ErrUndecided
		}
	}
	return nil
}
