package worker

import (
	"context"
	"fmt"
	"log/slog"

	"hatamoto.dev/core/internal/database"
)

const agentHitsTruncateBatch = 5000

// truncateAgentHitIPsSQL truncates client IPs older than 30 days to /24 (IPv4)
// or /48 (IPv6). network() is required: set_masklen alone keeps the host bits.
// Rows are selected by primary key (id, occurred_at), not ctid, because ctid is
// only unique within one partition of a partitioned table. Verification marks
// pending hits unverifiable after 24h, so nothing still needs the full IP.
const truncateAgentHitIPsSQL = `
UPDATE public.hatamoto_agent_hits
SET ip = network(set_masklen(ip, CASE family(ip) WHEN 4 THEN 24 ELSE 48 END))::inet,
    ip_truncated = true
WHERE (id, occurred_at) IN (
    SELECT id, occurred_at FROM public.hatamoto_agent_hits
    WHERE ip_truncated = false AND occurred_at < now() - interval '30 days'
    LIMIT $1)`

// AgentHitsMaintenanceWorker truncates stored client IPs after 30 days, in
// bounded batches so a backlog never holds a long lock.
type AgentHitsMaintenanceWorker struct {
	db        *database.DB
	batchSize int
	logger    *slog.Logger
}

func NewAgentHitsMaintenanceWorker(db *database.DB, batchSize int, logger *slog.Logger) *AgentHitsMaintenanceWorker {
	if batchSize <= 0 {
		batchSize = agentHitsTruncateBatch
	}
	return &AgentHitsMaintenanceWorker{db: db, batchSize: batchSize, logger: logger}
}

// Run truncates until a short batch signals the backlog is empty. A nil worker
// or database is a no-op.
func (w *AgentHitsMaintenanceWorker) Run(ctx context.Context) error {
	if w == nil || w.db == nil {
		return nil
	}
	adminCtx := database.WithSuperadmin(ctx)
	conn, err := w.db.GetConn(adminCtx)
	if err != nil {
		return fmt.Errorf("acquire conn: %w", err)
	}
	defer conn.Release()

	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		tag, err := conn.Exec(adminCtx, truncateAgentHitIPsSQL, w.batchSize)
		if err != nil {
			return fmt.Errorf("truncate agent hit ips: %w", err)
		}
		n := tag.RowsAffected()
		total += n
		if n < int64(w.batchSize) {
			break
		}
	}
	if total > 0 {
		w.logger.Info("truncated agent hit IPs", slog.Int64("rows", total))
	}
	return nil
}
