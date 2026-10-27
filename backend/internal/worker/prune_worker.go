package worker

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"hatamoto.dev/core/internal/database"
)

// PruneWorker performs batched DELETEs on high-churn tables so they don't grow
// unbounded. Each delete is done in bounded LIMIT batches (via ctid) so a large
// backlog never takes a long lock.
type PruneWorker struct {
	db        *database.DB
	batchSize int
	logger    *slog.Logger
}

func NewPruneWorker(db *database.DB, batchSize int, logger *slog.Logger) *PruneWorker {
	if batchSize <= 0 {
		batchSize = 5000
	}
	return &PruneWorker{db: db, batchSize: batchSize, logger: logger}
}

// pruneJob is one table + WHERE clause to batch-delete.
type pruneJob struct {
	name  string
	table string
	where string // must reference only the table; no params
}

// retentionJobs are the batched prunes. The 404 log is aged by last_seen_at.
var retentionJobs = []pruneJob{
	{"404-log", "hatamoto_404_log", "last_seen_at < NOW() - INTERVAL '90 days'"},
	{"mcp-audit", "hatamoto_mcp_audit", "at < NOW() - INTERVAL '90 days'"},
	{"admin-audit", "hatamoto_admin_audit", "at < NOW() - INTERVAL '1 year'"},
	{"admin-recovery", "hatamoto_admin_recovery_copies", "saved_at < NOW() - INTERVAL '30 days'"},
}

func (w *PruneWorker) Run(ctx context.Context) error {
	adminCtx := database.WithSuperadmin(ctx)
	conn, err := w.db.GetConn(adminCtx)
	if err != nil {
		return fmt.Errorf("acquire conn: %w", err)
	}
	defer conn.Release()

	for _, j := range retentionJobs {
		removed, err := w.pruneBatched(adminCtx, conn, j)
		if err != nil {
			// Log and continue: one table's failure shouldn't block the others.
			w.logger.Error("prune failed", slog.String("job", j.name), slog.Any("error", err))
			continue
		}
		if removed > 0 {
			w.logger.Info("pruned rows", slog.String("job", j.name), slog.Int64("removed", removed))
		}
	}
	return nil
}

func (w *PruneWorker) pruneBatched(ctx context.Context, conn *pgxpool.Conn, j pruneJob) (int64, error) {
	sql := fmt.Sprintf(
		`DELETE FROM public.%s WHERE ctid IN (SELECT ctid FROM public.%s WHERE %s LIMIT $1)`,
		j.table, j.table, j.where,
	)
	var total int64
	for {
		if ctx.Err() != nil {
			return total, ctx.Err()
		}
		tag, err := conn.Exec(ctx, sql, w.batchSize)
		if err != nil {
			return total, err
		}
		n := tag.RowsAffected()
		total += n
		if n < int64(w.batchSize) {
			break
		}
	}
	return total, nil
}
