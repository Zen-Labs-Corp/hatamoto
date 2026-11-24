package comment

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// RowsQuerier is the read side of a pool or transaction (pgx.Tx and *pgxpool.Pool satisfy it).
type RowsQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// CollectForEntity reads the comments an entity delete will cascade (drafts
// excluded: they were never public). Call announce after the delete committed:
// it fires OnDeleted for each collected comment with a batch context.
func (s *Service) CollectForEntity(ctx context.Context, q RowsQuerier, entityID uuid.UUID) (announce func(context.Context), err error) {
	rows, err := q.Query(ctx, `SELECT `+commentColumns+` FROM hatamoto_comments
		WHERE entity_id = $1 AND status <> 'draft' ORDER BY path`, entityID)
	if err != nil {
		return nil, err
	}
	cs, err := collectComments(rows)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context) { s.announceDeleted(ctx, cs) }, nil
}
