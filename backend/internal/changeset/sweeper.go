package changeset

import (
	"context"
	"fmt"
	"path"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// OrphanGrace is how old a staged file must be before the sweeper may call it
// an orphan: a plugin writes the file first and records the item after.
const OrphanGrace = time.Hour

// StagedFiles is the media side the engine and sweeper need (media.Service
// implements it).
type StagedFiles interface {
	DeleteStaged(ctx context.Context, keys []string) error
	ListStagedKeys(ctx context.Context) ([]string, error)
	StagedKeyModTime(key string) (time.Time, error)
}

// Sweep is the hourly housekeeping job. It is safe to run on several replicas
// at once: expiry skips changesets another replica holds (FOR UPDATE SKIP
// LOCKED) and every other step is an idempotent delete.
//
//  1. expire staged changesets past their TTL (items, buffer and files go);
//  2. delete runs that hold no staged or published changeset (a published
//     changeset keeps its pre-images so it stays revertible);
//  3. delete staged files nothing refers to (crash leftovers).
func (e *Engine) Sweep(ctx context.Context) error {
	if _, err := e.Expire(ctx); err != nil {
		return fmt.Errorf("expire changesets: %w", err)
	}
	if err := e.deleteFinishedRuns(ctx); err != nil {
		return err
	}
	if e.Blobs == nil {
		return nil
	}
	return e.deleteOrphans(ctx)
}

func (e *Engine) deleteFinishedRuns(ctx context.Context) error {
	return e.DB.WithTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			DELETE FROM hatamoto_runs r
			WHERE r.created_at < $1
			  AND EXISTS (SELECT 1 FROM hatamoto_changesets c WHERE c.run_id = r.id)
			  AND NOT EXISTS (SELECT 1 FROM hatamoto_changesets c WHERE c.run_id = r.id AND c.state IN ('staged', 'published'))`,
			e.now().Add(-OrphanGrace))
		if err != nil {
			return fmt.Errorf("failed to delete finished runs: %w", err)
		}
		return nil
	})
}

func (e *Engine) deleteOrphans(ctx context.Context) error {
	keys, err := e.Blobs.ListStagedKeys(ctx)
	if err != nil || len(keys) == 0 {
		return err
	}
	referenced := map[string]bool{}
	liveRuns := map[uuid.UUID]bool{}
	err = e.DB.WithTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT c.run_id, unnest(i.blob_keys)
			FROM hatamoto_changesets c LEFT JOIN hatamoto_changeset_items i ON i.changeset_id = c.id
			WHERE c.state = 'staged'`)
		if err != nil {
			return fmt.Errorf("failed to read staged files: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var run uuid.UUID
			var key *string
			if err := rows.Scan(&run, &key); err != nil {
				return err
			}
			liveRuns[run] = true
			if key != nil {
				referenced[*key] = true
			}
		}
		return rows.Err()
	})
	if err != nil {
		return err
	}
	cutoff := e.now().Add(-OrphanGrace)
	var orphans []string
	for _, key := range keys {
		if referenced[key] {
			continue
		}
		if run, ok := runOfKey(key); ok && liveRuns[run] {
			continue
		}
		mt, err := e.Blobs.StagedKeyModTime(key)
		if err != nil || mt.After(cutoff) { // gone already, or too young to judge
			continue
		}
		orphans = append(orphans, key)
	}
	if len(orphans) == 0 {
		return nil
	}
	e.logger().Info("changeset sweep: removing orphan staged files", "count", len(orphans))
	return e.Blobs.DeleteStaged(ctx, orphans)
}

// runOfKey reads the run id out of a staged key ".../staged/<run>/<file>".
func runOfKey(key string) (uuid.UUID, bool) {
	id, err := uuid.Parse(path.Base(path.Dir(key)))
	return id, err == nil
}
