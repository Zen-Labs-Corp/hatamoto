package agents

import (
	"context"
	"fmt"
	"time"

	"hatamoto.dev/core/internal/database"
)

const (
	// attributionPathBatch is how many distinct unattributed paths one page of
	// the listing holds, and how many paths one update statement covers.
	attributionPathBatch = 500
	// attributionUpdatePaths bounds the paths one daily transaction locks.
	attributionUpdatePaths = 50
	// attributionHitBatch bounds the raw hits one UPDATE statement rewrites.
	attributionHitBatch = 5000
	// attributionLookup bounds one resolver call.
	attributionLookup = 3 * time.Second
	// attributionMissTTL is how long a path without a page is left alone. A
	// little under the daily schedule, so the daily run looks again (a page
	// may have been published since) while restarts do not.
	attributionMissTTL = 20 * time.Hour
	// attributionMissKeep is how long a miss is remembered at all.
	attributionMissKeep = 7 * 24 * time.Hour
	// AttributionRetention is how far back raw hits are attributed (the raw
	// hit retention; older rows are gone or truncated anyway).
	AttributionRetention = 90 * 24 * time.Hour
)

// attributionLockSQL keeps one backfill running at a time, across replicas and
// between the start-up run and the scheduled one.
const attributionLockSQL = `SELECT pg_try_advisory_lock(hashtext('hatamoto_agent_attribution_backfill'))`
const attributionUnlockSQL = `SELECT pg_advisory_unlock(hashtext('hatamoto_agent_attribution_backfill'))`

// unattributedPathsSQL lists the paths still to attribute, from the rollup and
// from the retained raw hits (a hit skipped as locked in an earlier run shows
// up here again), without the paths recently found to have no page. Both
// sides are served by the partial idx_agent_*_unattributed indexes.
// $1 resume after, $2 limit, $3 raw hit retention, $4 miss TTL.
const unattributedPathsSQL = `
SELECT path FROM (
    SELECT path FROM hatamoto_agent_daily WHERE entity_id IS NULL AND status < 400
    UNION
    SELECT path FROM hatamoto_agent_hits
    WHERE entity_id IS NULL AND COALESCE(status, 0) < 400 AND occurred_at > now() - $3::interval
) p
WHERE path > $1
  AND NOT EXISTS (SELECT 1 FROM hatamoto_agent_attribution_misses m
                  WHERE m.path = p.path AND m.checked_at > now() - $4::interval)
ORDER BY path
LIMIT $2`

const recordMissesSQL = `
INSERT INTO hatamoto_agent_attribution_misses (path, checked_at)
SELECT unnest($1::text[]), now()
ON CONFLICT (path) DO UPDATE SET checked_at = EXCLUDED.checked_at`

const pruneMissesSQL = `DELETE FROM hatamoto_agent_attribution_misses WHERE checked_at < now() - $1::interval`

// attributeDailySQL links the rollup rows of the mapped paths. The rows are
// locked first, in the writer's order (day, agent_id, path, status; text
// bytewise, hence COLLATE "C"), so there is no wait cycle with the writer or
// verify. Only rows without an entity are touched. $1 paths, $2 entity ids.
const attributeDailySQL = `
WITH t AS (
    SELECT d.day, d.agent_id, d.path, d.status, m.id
    FROM hatamoto_agent_daily d
    JOIN unnest($1::text[], $2::uuid[]) AS m(path, id) ON m.path = d.path
    WHERE d.entity_id IS NULL AND d.status < 400
    ORDER BY d.day, d.agent_id COLLATE "C", d.path COLLATE "C", d.status
    FOR UPDATE OF d
)
UPDATE hatamoto_agent_daily d SET entity_id = t.id
FROM t
WHERE d.day = t.day AND d.agent_id = t.agent_id AND d.path = t.path AND d.status = t.status`

// attributeHitsSQL runs in its own statement with no daily row held, and never
// waits on a raw hit: one a verify run holds is skipped (the next run picks it
// up). At most $4 hits per call. $1 paths, $2 entity ids, $3 raw hit retention.
const attributeHitsSQL = `
UPDATE hatamoto_agent_hits h SET entity_id = t.eid
FROM (
    SELECT h.id, h.occurred_at, m.id AS eid
    FROM hatamoto_agent_hits h
    JOIN unnest($1::text[], $2::uuid[]) AS m(path, id) ON m.path = h.path
    WHERE h.entity_id IS NULL AND COALESCE(h.status, 0) < 400
      AND h.occurred_at > now() - $3::interval
    LIMIT $4
    FOR UPDATE OF h SKIP LOCKED
) t
WHERE h.id = t.id AND h.occurred_at = t.occurred_at`

// AttributionBackfill links crawler hits recorded without a page to the page
// they fetched, by path, as the resolver sees it today. Only entity_id is
// written: counters are untouched, so a run is idempotent (a second run finds
// nothing left to attribute) and safe beside the writer and the verify worker.
//
// A run lists the candidate paths and resolves them with no lock held, then
// updates in bounded batches joined on the path -> page mapping.
type AttributionBackfill struct {
	db        *database.DB
	resolve   ResolveFunc
	retention time.Duration
	hitBatch  int
}

// NewAttributionBackfill builds the job. A zero retention means AttributionRetention.
func NewAttributionBackfill(db *database.DB, resolve ResolveFunc, retention time.Duration) *AttributionBackfill {
	if retention <= 0 {
		retention = AttributionRetention
	}
	return &AttributionBackfill{db: db, resolve: resolve, retention: retention, hitBatch: attributionHitBatch}
}

// Run attributes every unattributed page path it can resolve and returns the
// number of daily rows updated. It ends by seeding the search first crawl
// table from the verified hits that carry a page. When another run holds the
// job (a replica, or the start-up run beside the scheduled one) it returns
// at once with nothing done.
func (b *AttributionBackfill) Run(ctx context.Context) (int64, error) {
	conn, err := b.db.GetConn(ctx)
	if err != nil {
		return 0, fmt.Errorf("get connection: %w", err)
	}
	defer conn.Release()
	var got bool
	if err := conn.QueryRow(ctx, attributionLockSQL).Scan(&got); err != nil {
		return 0, fmt.Errorf("job lock: %w", err)
	}
	if !got {
		return 0, nil
	}
	defer func() {
		uctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var ok bool
		if err := conn.QueryRow(uctx, attributionUnlockSQL).Scan(&ok); err != nil || !ok {
			_ = conn.Conn().Close(uctx) // never hand a session still holding the lock back to the pool
		}
	}()

	if _, err := b.db.Pool().Exec(ctx, pruneMissesSQL, seconds(attributionMissKeep)); err != nil {
		return 0, fmt.Errorf("prune misses: %w", err)
	}
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
		n, err := b.attributeBatch(ctx, paths)
		total += n
		if err != nil {
			return total, err
		}
	}
}

// attributeBatch resolves the paths (no lock held), remembers the ones with no
// page, and updates the others.
func (b *AttributionBackfill) attributeBatch(ctx context.Context, paths []string) (int64, error) {
	var mapped, ids, misses []string
	for _, p := range paths {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		if IsFilePath(p) {
			misses = append(misses, p)
			continue
		}
		lctx, cancel := context.WithTimeout(ctx, attributionLookup)
		id, _ := b.resolve(lctx, p)
		cancel()
		if id == nil {
			misses = append(misses, p)
			continue
		}
		mapped, ids = append(mapped, p), append(ids, id.String())
	}
	if len(misses) > 0 {
		if _, err := b.db.Pool().Exec(ctx, recordMissesSQL, misses); err != nil {
			return 0, fmt.Errorf("record misses: %w", err)
		}
	}
	var total int64
	for len(mapped) > 0 {
		k := min(attributionUpdatePaths, len(mapped))
		n, err := b.updateDailyRetry(ctx, mapped[:k], ids[:k])
		total += n
		if err != nil {
			return total, fmt.Errorf("attribute daily: %w", err)
		}
		if err := b.updateHits(ctx, mapped[:k], ids[:k]); err != nil {
			return total, fmt.Errorf("attribute hits: %w", err)
		}
		mapped, ids = mapped[k:], ids[k:]
	}
	return total, nil
}

func (b *AttributionBackfill) nextPaths(ctx context.Context, after string) ([]string, error) {
	rows, err := b.db.Pool().Query(ctx, unattributedPathsSQL, after, attributionPathBatch,
		seconds(b.retention), seconds(attributionMissTTL))
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

func seconds(d time.Duration) string { return fmt.Sprintf("%d seconds", int64(d.Seconds())) }

// updateDailyRetry reruns the batch once or twice when the database aborts the
// transaction (deadlock or serialization failure): nothing was committed.
func (b *AttributionBackfill) updateDailyRetry(ctx context.Context, paths, ids []string) (n int64, err error) {
	for try := 0; try < 3; try++ {
		if n, err = b.updateDaily(ctx, paths, ids); err == nil || !isTransactionRollback(err) {
			return n, err
		}
	}
	return n, err
}

// updateDaily links the daily rows of the paths in one short transaction.
func (b *AttributionBackfill) updateDaily(ctx context.Context, paths, ids []string) (int64, error) {
	tx, err := b.db.Pool().Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, attributeDailySQL, paths, ids)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}
	return tag.RowsAffected(), nil
}

// updateHits links the raw hits of the paths, in bounded statements. Hits
// skipped as locked stay for the next run.
func (b *AttributionBackfill) updateHits(ctx context.Context, paths, ids []string) error {
	aborted := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		tag, err := b.db.Pool().Exec(ctx, attributeHitsSQL, paths, ids, seconds(b.retention), b.hitBatch)
		if err != nil {
			if isTransactionRollback(err) && aborted < 2 {
				aborted++ // aborted, nothing written: run it again
				continue
			}
			return err
		}
		if tag.RowsAffected() < int64(b.hitBatch) {
			return nil
		}
	}
}
