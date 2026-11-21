package worker

import (
	"context"
	"fmt"
	"log/slog"
	"slices"

	"github.com/jackc/pgx/v5/pgxpool"
	"hatamoto.dev/core/internal/auditlog"
	"hatamoto.dev/core/internal/database"
)

// PruneWorker performs batched DELETEs on high-churn tables so they don't grow
// unbounded. Each delete is done in bounded LIMIT batches (via ctid) so a large
// backlog never takes a long lock.
type PruneWorker struct {
	db        *database.DB
	batchSize int
	logger    *slog.Logger

	// retention404 reports the 404 log retention in days; nil keeps the default.
	retention404 func(context.Context) int
	// auditRetention reports the admin audit and agent call retention in days;
	// nil keeps the defaults.
	auditRetention func(context.Context) (admin, agent int, err error)
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

// retention404Where holds the only WHERE clauses the 404 log prune may use,
// keyed by retention in days. The text is interpolated into SQL, so it is never
// built from a stored value.
var retention404Where = map[int]string{
	30:  "last_seen_at < NOW() - INTERVAL '30 days'",
	90:  "last_seen_at < NOW() - INTERVAL '90 days'",
	180: "last_seen_at < NOW() - INTERVAL '180 days'",
	365: "last_seen_at < NOW() - INTERVAL '365 days'",
}

// auditAdminWhere and auditAgentWhere hold the only WHERE clauses the audit
// prunes may use, keyed by retention in days. The text is interpolated into
// SQL, so it is never built from a stored value. The defaults (365 and 90)
// are the clauses the jobs always had.
var auditAdminWhere = map[int]string{
	90:   "at < NOW() - INTERVAL '90 days'",
	180:  "at < NOW() - INTERVAL '180 days'",
	365:  "at < NOW() - INTERVAL '1 year'",
	730:  "at < NOW() - INTERVAL '2 years'",
	1095: "at < NOW() - INTERVAL '3 years'",
}

var auditAgentWhere = map[int]string{
	90:   "at < NOW() - INTERVAL '90 days'",
	180:  "at < NOW() - INTERVAL '180 days'",
	365:  "at < NOW() - INTERVAL '1 year'",
	730:  "at < NOW() - INTERVAL '2 years'",
	1095: "at < NOW() - INTERVAL '3 years'",
}

// defaultRetention404 applies when the configured value is missing or unknown,
// so a failed read never shortens retention.
const defaultRetention404 = 90

// SetRetention404 makes each Run read the 404 log retention (days) from fn.
// Values outside retention404Where fall back to 90 days.
func (w *PruneWorker) SetRetention404(fn func(context.Context) int) { w.retention404 = fn }

// SetAuditRetention makes each Run read the admin audit and agent call
// retention (days) from fn. Unknown values fall back to the defaults; a failed
// read skips both audit prunes for that run, so it never shortens retention.
func (w *PruneWorker) SetAuditRetention(fn func(context.Context) (admin, agent int, err error)) {
	w.auditRetention = fn
}

// jobs returns retentionJobs with the where-clauses for this run.
func (w *PruneWorker) jobs(ctx context.Context) []pruneJob {
	jobs := slices.Clone(retentionJobs)
	if w.retention404 != nil {
		where, ok := retention404Where[w.retention404(ctx)]
		if !ok {
			where = retention404Where[defaultRetention404]
		}
		for i := range jobs {
			if jobs[i].name == "404-log" {
				jobs[i].where = where
			}
		}
	}
	if w.auditRetention == nil {
		return jobs
	}
	admin, agent, err := w.auditRetention(ctx)
	if err != nil {
		if w.logger != nil {
			w.logger.Warn("audit retention unavailable, skipping audit prune", slog.Any("error", err))
		}
		return slices.DeleteFunc(jobs, func(j pruneJob) bool { return j.name == "admin-audit" || j.name == "mcp-audit" })
	}
	adminWhere, ok := auditAdminWhere[admin]
	if !ok {
		adminWhere = auditAdminWhere[auditlog.DefaultRetentionDays]
	}
	agentWhere, ok := auditAgentWhere[agent]
	if !ok {
		agentWhere = auditAgentWhere[auditlog.DefaultAgentRetentionDays]
	}
	for i := range jobs {
		switch jobs[i].name {
		case "admin-audit":
			jobs[i].where = adminWhere
		case "mcp-audit":
			jobs[i].where = agentWhere
		}
	}
	return jobs
}

// retentionJobs are the batched prunes. The 404 log is aged by last_seen_at
// (90 days unless SetRetention404 says otherwise).
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

	for _, j := range w.jobs(ctx) {
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
