package standing

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/jsonx"
)

// Store reads and writes approvals.
type Store struct {
	DB  *database.DB
	Now func() time.Time // clock seam; nil is time.Now
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

const approvalColumns = `id, plugin, run, policy, fingerprint, approved_by, approved_at`

func scanApproval(row pgx.Row) (Approval, error) {
	var a Approval
	var policy, fp []byte
	if err := row.Scan(&a.ID, &a.Plugin, &a.Run, &policy, &fp, &a.ApprovedBy, &a.ApprovedAt); err != nil {
		return a, err
	}
	if err := jsonx.Unmarshal(policy, &a.Policy, jsonx.StoredLoose); err != nil {
		return a, fmt.Errorf("failed to decode approval policy: %w", err)
	}
	if err := jsonx.Unmarshal(fp, &a.FP, jsonx.StoredLoose); err != nil {
		return a, fmt.Errorf("failed to decode approval fingerprint: %w", err)
	}
	return a, nil
}

// Active returns the plugin's active approval for the run, or ErrNoApproval.
func (s *Store) Active(ctx context.Context, plugin, run string) (*Approval, error) {
	a, err := scanApproval(s.DB.Pool().QueryRow(ctx,
		`SELECT `+approvalColumns+` FROM hatamoto_standing_approvals
		 WHERE plugin = $1 AND run = $2 AND voided_at IS NULL`, plugin, run))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoApproval
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read approval: %w", err)
	}
	return &a, nil
}

// Approve records an approval and voids the run's previous active one in the
// same transaction, so at most one is ever active.
func (s *Store) Approve(ctx context.Context, plugin, run string, p Policy, fp Fingerprint, by string) (Approval, error) {
	policy, err := jsonx.Marshal(p)
	if err != nil {
		return Approval{}, fmt.Errorf("failed to encode policy: %w", err)
	}
	fpJSON, err := jsonx.Marshal(fp)
	if err != nil {
		return Approval{}, fmt.Errorf("failed to encode fingerprint: %w", err)
	}
	now := s.now()
	var a Approval
	err = pgx.BeginFunc(ctx, s.DB.Pool(), func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`UPDATE hatamoto_standing_approvals SET voided_at = $3, void_reason = 'replaced'
			 WHERE plugin = $1 AND run = $2 AND voided_at IS NULL`, plugin, run, now); err != nil {
			return fmt.Errorf("failed to void the previous approval: %w", err)
		}
		a, err = scanApproval(tx.QueryRow(ctx,
			`INSERT INTO hatamoto_standing_approvals (plugin, run, policy, fingerprint, approved_by, approved_at)
			 VALUES ($1, $2, $3, $4, $5, $6) RETURNING `+approvalColumns,
			plugin, run, policy, fpJSON, by, now))
		return err
	})
	if err != nil {
		return Approval{}, fmt.Errorf("failed to record approval: %w", err)
	}
	return a, nil
}

// Void ends the plugin's active approvals with a reason the Automatic updates
// tab shows. It returns how many approvals it ended (0 when none was active).
func (s *Store) Void(ctx context.Context, plugin, reason string) (n int64, err error) {
	err = s.DB.WithTx(ctx, func(tx pgx.Tx) error {
		n, err = VoidTx(ctx, tx, plugin, reason)
		return err
	})
	return n, err
}

// VoidTx voids every active approval of the plugin inside the caller's
// transaction and returns how many it ended. Calling it again returns 0.
func VoidTx(ctx context.Context, tx pgx.Tx, plugin, reason string) (int64, error) {
	tag, err := tx.Exec(ctx,
		`UPDATE hatamoto_standing_approvals SET voided_at = now(), void_reason = $2
		 WHERE plugin = $1 AND voided_at IS NULL`, plugin, reason)
	if err != nil {
		return 0, fmt.Errorf("failed to void approvals: %w", err)
	}
	return tag.RowsAffected(), nil
}
