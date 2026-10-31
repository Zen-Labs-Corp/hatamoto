package agents

import (
	"context"
	"fmt"
	"time"

	"hatamoto.dev/core/internal/database"
)

const (
	// attributionPathBatch is how many distinct unattributed paths one page of
	// the scan holds.
	attributionPathBatch = 500
	// attributionLookup bounds one resolver call.
	attributionLookup = 3 * time.Second
	// AttributionRetention is how far back raw hits are attributed (the raw
	// hit retention; older rows are gone or truncated anyway).
	AttributionRetention = 90 * 24 * time.Hour
)

// unattributedPathsSQL lists the paths still to attribute, from the rollup and
// from the retained raw hits (a hit skipped as locked in an earlier run shows
// up here again). $1 resume after, $2 limit, $3 raw hit retention.
const unattributedPathsSQL = `
SELECT path FROM (
    SELECT path FROM hatamoto_agent_daily WHERE entity_id IS NULL AND status < 400
    UNION
    SELECT path FROM hatamoto_agent_hits
    WHERE entity_id IS NULL AND COALESCE(status, 0) < 400 AND occurred_at > now() - $3::interval
) p
WHERE path > $1
ORDER BY path
LIMIT $2`

// lockAttributionSQL row-locks every daily row of the path that can be
// attributed (not only the ones still without an entity: the raw hits updated
// next belong to them), in the writer's order (day, agent_id, path, status;
// text bytewise, hence COLLATE "C"), so no wait cycle with the writer or verify.
const lockAttributionSQL = `
SELECT 1 FROM hatamoto_agent_daily
WHERE path = $1 AND status < 400
ORDER BY day, agent_id COLLATE "C", path COLLATE "C", status
FOR UPDATE`

const attributeDailySQL = `
UPDATE hatamoto_agent_daily SET entity_id = $2
WHERE path = $1 AND entity_id IS NULL AND status < 400`

// attributeHitsSQL never waits on a raw hit: one a verify run holds is skipped
// (the next run picks it up), because waiting on a hit while holding daily
// rows could close a wait cycle with the verify worker, which holds hits and
// then wants daily rows.
const attributeHitsSQL = `
UPDATE hatamoto_agent_hits h SET entity_id = $2
FROM (
    SELECT id, occurred_at FROM hatamoto_agent_hits
    WHERE path = $1 AND entity_id IS NULL AND COALESCE(status, 0) < 400
      AND occurred_at > now() - $3::interval
    FOR UPDATE SKIP LOCKED
) t
WHERE h.id = t.id AND h.occurred_at = t.occurred_at`

// AttributionBackfill links crawler hits recorded without a page to the page
// they fetched, by path, as the resolver sees it today. Only entity_id is
// written: counters are untouched, so a run is idempotent (a second run finds
// nothing left to attribute) and safe beside the writer and the verify worker.
type AttributionBackfill struct {
	db        *database.DB
	resolve   ResolveFunc
	retention time.Duration
}

// NewAttributionBackfill builds the job. A zero retention means AttributionRetention.
func NewAttributionBackfill(db *database.DB, resolve ResolveFunc, retention time.Duration) *AttributionBackfill {
	if retention <= 0 {
		retention = AttributionRetention
	}
	return &AttributionBackfill{db: db, resolve: resolve, retention: retention}
}

// Run attributes every unattributed page path it can resolve and returns the
// number of daily rows updated. It ends by seeding the search first crawl
// table from the verified hits that carry a page.
func (b *AttributionBackfill) Run(ctx context.Context) (int64, error) {
	var total int64
	last := ""
	for {
		paths, err := b.nextPaths(ctx, last)
		if err != nil {
			return total, err
		}
		if len(paths) == 0 {
			// Attributed hits may now qualify as a first crawl.
			if _, err := SeedSearchFirstCrawl(ctx, b.db); err != nil {
				return total, err
			}
			return total, nil
		}
		last = paths[len(paths)-1]
		for _, p := range paths {
			if err := ctx.Err(); err != nil {
				return total, err
			}
			if IsFilePath(p) {
				continue
			}
			lctx, cancel := context.WithTimeout(ctx, attributionLookup)
			id, _ := b.resolve(lctx, p)
			cancel()
			if id == nil {
				continue
			}
			n, err := b.attributeRetry(ctx, p, id.String())
			if err != nil {
				return total, fmt.Errorf("attribute %s: %w", p, err)
			}
			total += n
		}
	}
}

func (b *AttributionBackfill) nextPaths(ctx context.Context, after string) ([]string, error) {
	rows, err := b.db.Pool().Query(ctx, unattributedPathsSQL, after, attributionPathBatch, b.interval())
	if err != nil {
		return nil, fmt.Errorf("list paths: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (b *AttributionBackfill) interval() string {
	return fmt.Sprintf("%d seconds", int64(b.retention.Seconds()))
}

// attributeRetry reruns a path once or twice when the database aborts the
// transaction (deadlock or serialization failure): nothing was committed.
func (b *AttributionBackfill) attributeRetry(ctx context.Context, path, entityID string) (n int64, err error) {
	for try := 0; try < 3; try++ {
		if n, err = b.attribute(ctx, path, entityID); err == nil || !isTransactionRollback(err) {
			return n, err
		}
	}
	return n, err
}

// attribute runs one path in its own short transaction.
func (b *AttributionBackfill) attribute(ctx context.Context, path, entityID string) (int64, error) {
	conn, err := b.db.GetConn(ctx)
	if err != nil {
		return 0, fmt.Errorf("get connection: %w", err)
	}
	defer conn.Release()
	tx, err := conn.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, lockAttributionSQL, path); err != nil {
		return 0, fmt.Errorf("lock daily rows: %w", err)
	}
	tag, err := tx.Exec(ctx, attributeDailySQL, path, entityID)
	if err != nil {
		return 0, fmt.Errorf("attribute daily: %w", err)
	}
	if _, err := tx.Exec(ctx, attributeHitsSQL, path, entityID, b.interval()); err != nil {
		return 0, fmt.Errorf("attribute hits: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}
	return tag.RowsAffected(), nil
}
