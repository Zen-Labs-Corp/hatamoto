// Package kit holds what the site records about applied site kits.
package kit

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotApplied is returned by Get when the kit was never applied.
var ErrNotApplied = errors.New("kit: not applied")

// Applied is one row of hatamoto_applied_kits.
type Applied struct {
	Name      string
	Version   string
	AppliedBy string
	AppliedAt time.Time
	Summary   jsontext.Value
}

// AppliedStore reads and writes hatamoto_applied_kits.
type AppliedStore struct{ Pool *pgxpool.Pool }

const appliedCols = `name, version, applied_by, applied_at, summary`

func scanApplied(r pgx.Row) (Applied, error) {
	var a Applied
	var sum []byte
	if err := r.Scan(&a.Name, &a.Version, &a.AppliedBy, &a.AppliedAt, &sum); err != nil {
		return Applied{}, err
	}
	a.AppliedAt = a.AppliedAt.UTC()
	a.Summary = jsontext.Value(sum)
	return a, nil
}

// Get returns the applied record for name, or ErrNotApplied.
func (s AppliedStore) Get(ctx context.Context, name string) (*Applied, error) {
	a, err := scanApplied(s.Pool.QueryRow(ctx, `SELECT `+appliedCols+` FROM hatamoto_applied_kits WHERE name = $1`, name))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotApplied
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// List returns every applied kit, most recently applied first.
func (s AppliedStore) List(ctx context.Context) ([]Applied, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+appliedCols+` FROM hatamoto_applied_kits ORDER BY applied_at DESC, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Applied
	for rows.Next() {
		a, err := scanApplied(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Record upserts the row for a.Name; applied_at is set to now (UTC).
func (s AppliedStore) Record(ctx context.Context, a Applied) error {
	sum := []byte(a.Summary)
	if len(sum) == 0 {
		sum = []byte("{}")
	}
	_, err := s.Pool.Exec(ctx, `INSERT INTO hatamoto_applied_kits (name, version, applied_by, applied_at, summary)
		VALUES ($1, $2, $3, now(), $4)
		ON CONFLICT (name) DO UPDATE SET version = EXCLUDED.version, applied_by = EXCLUDED.applied_by,
			applied_at = EXCLUDED.applied_at, summary = EXCLUDED.summary`, a.Name, a.Version, a.AppliedBy, sum)
	return err
}
