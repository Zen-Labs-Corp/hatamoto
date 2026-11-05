package changeset

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/media"
)

// Engine stages, publishes, discards, reverts and expires changesets.
type Engine struct {
	DB      *database.DB
	Writers map[Kind]KindWriter
	Blobs   StagedFiles
	// Audit records an action after it committed; nil skips it.
	Audit  func(ctx context.Context, action, target string, detail map[string]any)
	Now    func() time.Time // clock seam; nil is time.Now
	Logger *slog.Logger
}

func (e *Engine) now() time.Time {
	if e.Now != nil {
		return e.Now().UTC()
	}
	return time.Now().UTC()
}

func (e *Engine) logger() *slog.Logger {
	if e.Logger != nil {
		return e.Logger
	}
	return slog.Default()
}

func (e *Engine) audit(ctx context.Context, action string, csID int64, detail map[string]any) {
	if e.Audit != nil {
		e.Audit(ctx, action, fmt.Sprintf("changeset:%d", csID), detail)
	}
}

// Begin opens a staged changeset of plugin ("" is the owner) in run runID,
// creating the run on first use. It expires TTL from now. A run row Begin
// creates itself is not driven by anyone, so it is born 'finished' and never
// counts as the plugin's one active run; a driven run is inserted by the run
// store first (state 'running') and Begin finds it.
func (e *Engine) Begin(ctx context.Context, runID uuid.UUID, plugin string) (int64, error) {
	var id int64
	err := e.DB.WithTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO hatamoto_runs (id, plugin, state) VALUES ($1, $2, 'finished') ON CONFLICT (id) DO NOTHING`, runID, plugin); err != nil {
			return fmt.Errorf("failed to create run: %w", err)
		}
		return tx.QueryRow(ctx, `
			INSERT INTO hatamoto_changesets (run_id, plugin, expires_at) VALUES ($1, $2, $3) RETURNING id`,
			runID, plugin, e.now().Add(TTL)).Scan(&id)
	})
	return id, err
}

// Resolve returns the object plugin ("" is the owner) published under
// sourceKey in an earlier changeset; ok is false when no run has mapped the
// key. Items staged but not yet published have no mapping.
func (e *Engine) Resolve(ctx context.Context, plugin string, kind Kind, sourceKey string) (targetID string, ok bool, err error) {
	targetID, err = mappedTarget(ctx, e.DB.Pool(), Origin(plugin), kind, sourceKey)
	return targetID, targetID != "", err
}

// HasStaged reports whether the changeset already holds an item of kind with
// sourceKey, so a later item can name it (a child's parent_key).
func (e *Engine) HasStaged(ctx context.Context, csID int64, kind Kind, sourceKey string) (bool, error) {
	var ok bool
	err := e.DB.Pool().QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM hatamoto_changeset_items WHERE changeset_id = $1 AND kind = $2 AND source_key = $3)`,
		csID, string(kind), sourceKey).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("failed to look up staged item: %w", err)
	}
	return ok, nil
}

// The most one changeset may hold: items, and the bytes of their payloads.
// Without them a run could fill the database before the 72 h expiry.
var (
	maxStagedItems = 50000
	maxStagedBytes = int64(256 << 20)
)

// Stage adds it to a staged changeset. Nothing live changes.
func (e *Engine) Stage(ctx context.Context, csID int64, it Item) error {
	if singletonKinds[it.Kind] {
		return fmt.Errorf("%w: %q", ErrSingleton, it.Kind)
	}
	if _, ok := e.Writers[it.Kind]; !ok {
		return fmt.Errorf("%w: %q", ErrNoWriter, it.Kind)
	}
	if it.Op != OpUpsert && it.Op != OpDelete {
		return fmt.Errorf("%w: operation %q", ErrInvalidItem, it.Op)
	}
	if it.Op == OpDelete && it.TargetID == "" {
		return fmt.Errorf("%w: delete needs a target", ErrInvalidItem)
	}
	payload := it.Payload
	if len(payload) == 0 {
		payload = jsontext.Value(`{}`)
	}
	keys := it.BlobKeys
	if keys == nil {
		keys = []string{}
	}
	return e.DB.WithTx(ctx, func(tx pgx.Tx) error {
		cs, err := lockChangeset(ctx, tx, csID)
		if err != nil {
			return err
		}
		if cs.State != StateStaged {
			return ErrNotStaged
		}
		if !e.now().Before(cs.ExpiresAt) {
			return ErrExpired
		}
		target := it.TargetID
		if target == "" {
			if target, err = mappedTarget(ctx, tx, Origin(cs.Plugin), it.Kind, it.SourceKey); err != nil {
				return err
			}
		}
		if err := e.checkOwnership(ctx, tx, ownerPlugin(cs.Plugin, it), it.Kind, target); err != nil {
			return err
		}
		if err := checkBlobKeys(cs.RunID, it); err != nil {
			return err
		}
		if it.Kind == KindMedia && !(it.Op == OpUpsert && ((target == "" && len(it.BlobKeys) == 1) || isAltChange(it))) {
			return fmt.Errorf("%w: media items create one file or change an alt text", ErrUnsupportedOp)
		}
		if cs.Items >= int64(maxStagedItems) || cs.PayloadBytes+int64(len(payload)) > maxStagedBytes {
			return fmt.Errorf("%w: at most %d items and %d MiB", ErrQuotaExceeded, maxStagedItems, maxStagedBytes>>20)
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO hatamoto_changeset_items (changeset_id, seq, kind, op, source_key, target_id, base_token, payload, blob_keys)
			VALUES ($1, (SELECT COALESCE(MAX(seq), 0) + 1 FROM hatamoto_changeset_items WHERE changeset_id = $1),
			        $2, $3, $4, NULLIF($5, ''), NULLIF($6, ''), $7, $8)`,
			csID, string(it.Kind), it.Op, it.SourceKey, it.TargetID, it.BaseToken, payload, keys)
		if err != nil {
			return fmt.Errorf("failed to stage item: %w", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE hatamoto_changesets SET item_count = item_count + 1, payload_bytes = payload_bytes + $2 WHERE id = $1`,
			csID, len(payload)); err != nil {
			return fmt.Errorf("failed to count the staged item: %w", err)
		}
		return nil
	})
}

// checkBlobKeys refuses staged files that are not the run's own: only media
// items carry files, and each must sit under the run's staged directory, or
// one run could publish or delete another run's files.
func checkBlobKeys(runID uuid.UUID, it Item) error {
	if len(it.BlobKeys) > 0 && it.Kind != KindMedia {
		return fmt.Errorf("%w: %s items carry no files", ErrInvalidItem, it.Kind)
	}
	prefix := media.StagedPrefix(runID) + "/"
	for _, k := range it.BlobKeys {
		if !strings.HasPrefix(k, prefix) || path.Clean(k) != k {
			return fmt.Errorf("%w: file %q is not staged by this run", ErrInvalidItem, k)
		}
	}
	return nil
}

// Discard drops a staged changeset: its items, the run's buffer and the staged files.
func (e *Engine) Discard(ctx context.Context, csID int64) error {
	keys, err := e.finishStaged(ctx, csID, StateDiscarded)
	if err != nil {
		return err
	}
	e.deleteBlobs(ctx, keys)
	e.audit(ctx, "changeset.discard", csID, nil)
	return nil
}

// Expire closes every staged changeset past its expiry, like Discard. It
// skips changesets another replica is working on. It returns how many it closed.
func (e *Engine) Expire(ctx context.Context) (int, error) {
	n := 0
	for {
		var id int64
		var keys []string
		err := e.DB.WithTx(ctx, func(tx pgx.Tx) error {
			err := tx.QueryRow(ctx, `
				SELECT id FROM hatamoto_changesets WHERE state = 'staged' AND expires_at <= $1
				ORDER BY id LIMIT 1 FOR UPDATE SKIP LOCKED`, e.now()).Scan(&id)
			if err != nil {
				return err
			}
			keys, err = closeStaged(ctx, tx, id, StateExpired)
			return err
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return n, nil
		}
		if err != nil {
			return n, err
		}
		n++
		e.deleteBlobs(ctx, keys)
		e.audit(ctx, "changeset.expire", id, nil)
	}
}

func (e *Engine) finishStaged(ctx context.Context, csID int64, state string) (keys []string, err error) {
	err = e.DB.WithTx(ctx, func(tx pgx.Tx) error {
		cs, err := lockChangeset(ctx, tx, csID)
		if err != nil {
			return err
		}
		if cs.State != StateStaged {
			return ErrNotStaged
		}
		keys, err = closeStaged(ctx, tx, csID, state)
		return err
	})
	return keys, err
}

// closeStaged deletes the items and the run buffer of a locked staged
// changeset, marks it state and returns the staged file keys to delete after commit.
func closeStaged(ctx context.Context, tx pgx.Tx, csID int64, state string) ([]string, error) {
	var keys []string
	rows, err := tx.Query(ctx, `SELECT unnest(blob_keys) FROM hatamoto_changeset_items WHERE changeset_id = $1`, csID)
	if err != nil {
		return nil, fmt.Errorf("failed to list staged files: %w", err)
	}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			rows.Close()
			return nil, err
		}
		keys = append(keys, k)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM hatamoto_changeset_items WHERE changeset_id = $1`, csID); err != nil {
		return nil, fmt.Errorf("failed to delete items: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM hatamoto_run_buffer WHERE run_id = (SELECT run_id FROM hatamoto_changesets WHERE id = $1)`, csID); err != nil {
		return nil, fmt.Errorf("failed to delete run buffer: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE hatamoto_changesets SET state = $2 WHERE id = $1`, csID, state); err != nil {
		return nil, fmt.Errorf("failed to close changeset: %w", err)
	}
	return keys, nil
}

func (e *Engine) deleteBlobs(ctx context.Context, keys []string) {
	if e.Blobs == nil || len(keys) == 0 {
		return
	}
	if err := e.Blobs.DeleteStaged(ctx, keys); err != nil {
		e.logger().Warn("changeset: failed to delete staged files", "error", err.Error())
	}
}
