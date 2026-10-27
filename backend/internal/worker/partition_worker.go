package worker

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"time"

	"hatamoto.dev/core/internal/database"
)

// PartitionWorker keeps a monthly RANGE-partitioned table's partitions ahead of
// the write boundary and drops aged ones. The parent table carries PARTITIONED
// indexes, so CREATE TABLE ... PARTITION OF auto-creates the matching child
// indexes: no manual index DDL needed. parent is always a package constant,
// never user input; it is also what keeps the DROP guard (nameRe) honest.
type PartitionWorker struct {
	db        *database.DB
	parent    string
	nameRe    *regexp.Regexp // matches parent_YYYY_MM only, never the DEFAULT partition
	ahead     int            // how many future months to ensure exist
	retention int            // drop partitions whose month ended more than this ago
	logger    *slog.Logger
}

const (
	agentHitsParent = "hatamoto_agent_hits"
)

func newPartitionWorker(db *database.DB, parent string, nameRe *regexp.Regexp, ahead, retention int, logger *slog.Logger) *PartitionWorker {
	return &PartitionWorker{db: db, parent: parent, nameRe: nameRe, ahead: ahead, retention: retention, logger: logger}
}

// agentHitsPartitionName matches hatamoto_agent_hits_YYYY_MM (never _default).
var agentHitsPartitionName = regexp.MustCompile(`^hatamoto_agent_hits_(\d{4})_(\d{2})$`)

// NewAgentHitsPartitionWorker maintains hatamoto_agent_hits. retentionMonths=3
// keeps at least 90 days of hits (month granularity: up to ~120 days).
func NewAgentHitsPartitionWorker(db *database.DB, aheadMonths, retentionMonths int, logger *slog.Logger) *PartitionWorker {
	if aheadMonths <= 0 {
		aheadMonths = 2
	}
	if retentionMonths <= 0 {
		retentionMonths = 3
	}
	return newPartitionWorker(db, agentHitsParent, agentHitsPartitionName, aheadMonths, retentionMonths, logger)
}

// firstOfMonthUTC returns midnight UTC on the first day of t's month.
func firstOfMonthUTC(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

func (w *PartitionWorker) ddl(month time.Time) (name, ddl string) {
	return partitionDDLFor(w.parent, month)
}

func partitionDDLFor(parent string, month time.Time) (name, ddl string) {
	lower := firstOfMonthUTC(month)
	upper := lower.AddDate(0, 1, 0)
	name = fmt.Sprintf("%s_%04d_%02d", parent, lower.Year(), int(lower.Month()))
	ddl = fmt.Sprintf(
		`CREATE TABLE IF NOT EXISTS public.%s PARTITION OF public.%s `+
			`FOR VALUES FROM ('%s') TO ('%s')`,
		name,
		parent,
		lower.Format("2006-01-02 15:04:05-07"),
		upper.Format("2006-01-02 15:04:05-07"),
	)
	return name, ddl
}

// ensureStatements returns the idempotent CREATE statements for the month of
// now and the following `ahead` months, in order.
func (w *PartitionWorker) ensureStatements(now time.Time, ahead int) (names, ddls []string) {
	base := firstOfMonthUTC(now)
	for i := 0; i <= ahead; i++ {
		name, ddl := w.ddl(base.AddDate(0, i, 0))
		names = append(names, name)
		ddls = append(ddls, ddl)
	}
	return names, ddls
}

// EnsureCurrentAndNext synchronously creates the current and next month's
// partitions and warns when the DEFAULT partition holds rows. It is meant for
// server startup, independent of the scheduler (HATAMOTO_WORKER=off is the
// default), so hits never land in DEFAULT where retention cannot drop them.
// Errors are logged, never returned: startup must not depend on it.
func (w *PartitionWorker) EnsureCurrentAndNext(ctx context.Context) {
	adminCtx := database.WithSuperadmin(ctx)
	conn, err := w.db.GetConn(adminCtx)
	if err != nil {
		w.logger.Error("ensure partitions: acquire conn failed", slog.String("parent", w.parent), slog.Any("error", err))
		return
	}
	defer conn.Release()
	defer func() { _, _ = conn.Exec(context.Background(), `RESET lock_timeout`) }()
	if _, err := conn.Exec(adminCtx, `SET lock_timeout = '5s'`); err != nil {
		w.logger.Error("ensure partitions: set lock_timeout failed", slog.String("parent", w.parent), slog.Any("error", err))
		return
	}

	names, ddls := w.ensureStatements(time.Now(), 1)
	for i, ddl := range ddls {
		if _, err := conn.Exec(adminCtx, ddl); err != nil {
			w.logger.Error("create partition failed", slog.String("parent", w.parent), slog.String("partition", names[i]), slog.Any("error", err))
		}
	}

	var hasRows bool
	q := fmt.Sprintf(`SELECT EXISTS (SELECT 1 FROM public.%s_default)`, w.parent)
	if err := conn.QueryRow(adminCtx, q).Scan(&hasRows); err != nil {
		w.logger.Error("check default partition failed", slog.String("parent", w.parent), slog.Any("error", err))
		return
	}
	if hasRows {
		w.logger.Warn("DEFAULT partition holds rows: they are never retention-dropped or IP-truncated; move them into monthly partitions",
			slog.String("parent", w.parent), slog.String("partition", w.parent+"_default"))
	}
}

// dropCutoff is the month boundary before which whole partitions are dropped.
func (w *PartitionWorker) dropCutoff(now time.Time) time.Time {
	return firstOfMonthUTC(now).AddDate(0, -w.retention, 0)
}

// expired reports whether the named partition's month ended at or before cutoff.
// Names that do not match the parent_YYYY_MM convention (DEFAULT, strays) never
// expire.
func (w *PartitionWorker) expired(name string, cutoff time.Time) bool {
	m := w.nameRe.FindStringSubmatch(name)
	if m == nil {
		return false
	}
	year, _ := strconv.Atoi(m[1])
	mon, _ := strconv.Atoi(m[2])
	monthEnd := time.Date(year, time.Month(mon), 1, 0, 0, 0, 0, time.UTC).AddDate(0, 1, 0)
	return !monthEnd.After(cutoff)
}

// Run ensures the current + next `ahead` partitions exist, then drops any
// YYYY_MM partition whose month ended more than `retention` months ago.
func (w *PartitionWorker) Run(ctx context.Context) error {
	adminCtx := database.WithSuperadmin(ctx)
	conn, err := w.db.GetConn(adminCtx)
	if err != nil {
		return fmt.Errorf("acquire conn: %w", err)
	}
	defer conn.Release()
	// Reset the session-level lock_timeout before the conn returns to the pool
	// (defers run LIFO, so this fires before Release). Otherwise the 5s timeout
	// would leak to the next request that reuses this pooled connection.
	defer func() { _, _ = conn.Exec(context.Background(), `RESET lock_timeout`) }()

	// Bound how long any single DDL will wait for its lock so partition
	// maintenance can never block event writes/reads for minutes.
	if _, err := conn.Exec(adminCtx, `SET lock_timeout = '5s'`); err != nil {
		return fmt.Errorf("set lock_timeout: %w", err)
	}

	base := firstOfMonthUTC(time.Now())
	names, ddls := w.ensureStatements(base, w.ahead)
	for i, ddl := range ddls {
		name := names[i]
		if _, err := conn.Exec(adminCtx, ddl); err != nil {
			// Don't abort the whole run: one month's failure (e.g. the DEFAULT
			// partition already holds rows for it, or a lock timeout) must not
			// stop later months from being created or old ones from being dropped.
			w.logger.Error("create partition failed", slog.String("parent", w.parent), slog.String("partition", name), slog.Any("error", err))
		}
	}

	// Drop partitions whose month is older than the retention cutoff.
	cutoff := w.dropCutoff(base)
	rows, err := conn.Query(adminCtx, `
		SELECT c.relname
		FROM pg_inherits i
		JOIN pg_class c ON c.oid = i.inhrelid
		JOIN pg_class p ON p.oid = i.inhparent
		WHERE p.relname = $1`, w.parent)
	if err != nil {
		return fmt.Errorf("list partitions: %w", err)
	}
	var drop []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		if w.expired(name, cutoff) {
			drop = append(drop, name)
		}
	}
	rows.Close()

	for _, name := range drop {
		if _, err := conn.Exec(adminCtx, fmt.Sprintf(`DROP TABLE IF EXISTS public.%s`, name)); err != nil {
			w.logger.Error("drop aged partition failed", slog.String("parent", w.parent), slog.String("partition", name), slog.Any("error", err))
			continue
		}
		w.logger.Info("dropped aged partition", slog.String("parent", w.parent), slog.String("partition", name))
	}
	return nil
}
