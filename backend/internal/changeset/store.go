package changeset

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

type changesetRow struct {
	ID        int64
	Plugin    string
	State     string
	ExpiresAt time.Time
}

// lockChangeset reads and row-locks a changeset.
func lockChangeset(ctx context.Context, tx pgx.Tx, id int64) (changesetRow, error) {
	var c changesetRow
	err := tx.QueryRow(ctx, `SELECT id, plugin, state, expires_at FROM hatamoto_changesets WHERE id = $1 FOR UPDATE`, id).
		Scan(&c.ID, &c.Plugin, &c.State, &c.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, ErrNotFound
	}
	if err != nil {
		return c, fmt.Errorf("failed to read changeset: %w", err)
	}
	return c, nil
}

func loadItems(ctx context.Context, tx pgx.Tx, csID int64, reverse bool) ([]StoredItem, error) {
	order := "ASC"
	if reverse {
		order = "DESC"
	}
	rows, err := tx.Query(ctx, `
		SELECT id, seq, kind, op, COALESCE(source_key, ''), COALESCE(target_id, ''), COALESCE(base_token, ''),
		       payload, before, blob_keys, COALESCE(result, ''), COALESCE(result_detail, ''), COALESCE(after_token, '')
		FROM hatamoto_changeset_items WHERE changeset_id = $1 ORDER BY seq `+order, csID)
	if err != nil {
		return nil, fmt.Errorf("failed to read items: %w", err)
	}
	defer rows.Close()
	var out []StoredItem
	for rows.Next() {
		var it StoredItem
		var kind string
		var payload, before []byte
		if err := rows.Scan(&it.ID, &it.Seq, &kind, &it.Op, &it.SourceKey, &it.TargetID, &it.BaseToken,
			&payload, &before, &it.BlobKeys, &it.Result, &it.ResultDetail, &it.AfterToken); err != nil {
			return nil, err
		}
		it.Kind = Kind(kind)
		it.Payload = json.RawMessage(payload)
		if before != nil {
			it.Before = json.RawMessage(before)
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func mappedTarget(ctx context.Context, tx pgx.Tx, origin string, kind Kind, sourceKey string) (string, error) {
	if sourceKey == "" {
		return "", nil
	}
	var id string
	err := tx.QueryRow(ctx, `SELECT target_id FROM hatamoto_changeset_source_keys WHERE origin = $1 AND kind = $2 AND source_key = $3`,
		origin, string(kind), sourceKey).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("failed to read source key: %w", err)
	}
	return id, nil
}

func saveMapping(ctx context.Context, tx pgx.Tx, origin string, kind Kind, sourceKey, targetID string) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO hatamoto_changeset_source_keys (origin, kind, source_key, target_id) VALUES ($1, $2, $3, $4)
		ON CONFLICT (origin, kind, source_key) DO UPDATE SET target_id = EXCLUDED.target_id`,
		origin, string(kind), sourceKey, targetID)
	if err != nil {
		return fmt.Errorf("failed to save source key: %w", err)
	}
	return nil
}

func dropMapping(ctx context.Context, tx pgx.Tx, origin string, kind Kind, sourceKey string) error {
	_, err := tx.Exec(ctx, `DELETE FROM hatamoto_changeset_source_keys WHERE origin = $1 AND kind = $2 AND source_key = $3`,
		origin, string(kind), sourceKey)
	if err != nil {
		return fmt.Errorf("failed to drop source key: %w", err)
	}
	return nil
}
