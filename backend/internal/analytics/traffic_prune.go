package analytics

import (
	"context"
	"fmt"
	"time"
)

// Retention and compaction of the traffic rollup.
//
//   - channel, device and country rows are tiny and kept forever;
//   - source, campaign and landing rows are deleted after trafficRetentionMonths;
//   - for those three dims, a day older than a day keeps its top
//     trafficTopKeys rows by visits plus pageviews, the rest are folded into
//     one (other) row, so totals stay exact. This also covers the keys the
//     in-memory per-day cap let through.
const (
	trafficRetentionMonths = 13
	trafficTopKeys         = 100
)

const pruneTrafficSQL = `
WITH ranked AS (
	SELECT day, dim, k1, k2, visits, pageviews, engaged, multi, scrolled, form_seen, form_started,
		row_number() OVER (PARTITION BY day, dim ORDER BY visits + pageviews DESC, k1, k2) AS rn
	FROM hatamoto_traffic_daily
	WHERE dim IN ('source', 'campaign', 'landing') AND day < $1 AND NOT (k1 = $3 AND k2 = '')
), gone AS (
	DELETE FROM hatamoto_traffic_daily t USING ranked r
	WHERE r.rn > $2 AND t.day = r.day AND t.dim = r.dim AND t.k1 = r.k1 AND t.k2 = r.k2
	RETURNING 1
)
INSERT INTO hatamoto_traffic_daily AS t (day, dim, k1, k2, visits, pageviews, engaged, multi, scrolled, form_seen, form_started)
SELECT day, dim, $3, '', SUM(visits), SUM(pageviews), SUM(engaged), SUM(multi), SUM(scrolled),
	SUM(form_seen), SUM(form_started)
FROM ranked WHERE rn > $2 GROUP BY day, dim
ON CONFLICT (day, dim, k1, k2) DO UPDATE SET
	visits = t.visits + EXCLUDED.visits, pageviews = t.pageviews + EXCLUDED.pageviews,
	engaged = t.engaged + EXCLUDED.engaged, multi = t.multi + EXCLUDED.multi,
	scrolled = t.scrolled + EXCLUDED.scrolled, form_seen = t.form_seen + EXCLUDED.form_seen,
	form_started = t.form_started + EXCLUDED.form_started`

// PruneTraffic applies the traffic retention and compaction as of now (one
// transaction). It never deletes channel, device or country rows.
func (r *PostgresRepository) PruneTraffic(ctx context.Context, now time.Time) error {
	y, m, d := now.UTC().Date()
	today := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return fmt.Errorf("get connection: %w", err)
	}
	defer conn.Release()
	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("prune traffic: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `DELETE FROM hatamoto_traffic_daily
		WHERE dim IN ('source', 'campaign', 'landing') AND day < $1`,
		today.AddDate(0, -trafficRetentionMonths, 0)); err != nil {
		return fmt.Errorf("prune traffic: %w", err)
	}
	if _, err := tx.Exec(ctx, pruneTrafficSQL, today.AddDate(0, 0, -1), trafficTopKeys, KeyOther); err != nil {
		return fmt.Errorf("compact traffic: %w", err)
	}
	return tx.Commit(ctx)
}
