package changeset

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Header is the read-only summary of a changeset.
type Header struct {
	ID        int64
	RunID     uuid.UUID
	Plugin    string
	State     string
	ExpiresAt time.Time
	ItemCount int
}

// Get reads a changeset and its items ordered by seq. It is a plain SELECT on
// the pool: no transaction, no row lock, it never writes. ErrNotFound when the
// changeset does not exist.
func (e *Engine) Get(ctx context.Context, id int64) (Header, []StoredItem, error) {
	h, err := e.Header(ctx, id)
	if err != nil {
		return Header{}, nil, err
	}
	items, err := loadItems(ctx, e.DB.Pool(), id, false)
	if err != nil {
		return Header{}, nil, err
	}
	return h, items, nil
}

// Header reads the summary of a changeset without its items. ErrNotFound when
// the changeset does not exist.
func (e *Engine) Header(ctx context.Context, id int64) (Header, error) {
	var h Header
	err := e.DB.Pool().QueryRow(ctx, `SELECT id, run_id, plugin, state, expires_at, item_count FROM hatamoto_changesets WHERE id = $1`, id).
		Scan(&h.ID, &h.RunID, &h.Plugin, &h.State, &h.ExpiresAt, &h.ItemCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return Header{}, ErrNotFound
	}
	if err != nil {
		return Header{}, fmt.Errorf("failed to read changeset: %w", err)
	}
	return h, nil
}

// ItemSummary is one row of a changeset's item list: what a person needs to
// recognise the change, read without its payload.
type ItemSummary struct {
	Seq       int
	Kind      Kind
	Op        string
	SourceKey string
	TargetID  string
	// Label is the payload's name, title, source path or slug ("" when none).
	Label string
}

// ItemPage reads limit item summaries from offset, ordered by seq. Only the
// columns the list shows are selected, so a large changeset never loads its
// payloads for it.
func (e *Engine) ItemPage(ctx context.Context, id int64, limit, offset int) ([]ItemSummary, error) {
	rows, err := e.DB.Pool().Query(ctx, `
		SELECT seq, kind, op, source_key, COALESCE(target_id, ''),
		       COALESCE(NULLIF(payload->>'name', ''), NULLIF(payload->>'title', ''), NULLIF(payload->>'source_path', ''), NULLIF(payload->>'slug', ''), '')
		FROM hatamoto_changeset_items WHERE changeset_id = $1 ORDER BY seq LIMIT $2 OFFSET $3`, id, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to read items: %w", err)
	}
	defer rows.Close()
	var out []ItemSummary
	for rows.Next() {
		var it ItemSummary
		var kind string
		if err := rows.Scan(&it.Seq, &kind, &it.Op, &it.SourceKey, &it.TargetID, &it.Label); err != nil {
			return nil, err
		}
		it.Kind = Kind(kind)
		out = append(out, it)
	}
	return out, rows.Err()
}

// PreviewSeqs lists, in order, the seq of every item the preview can open:
// entity and redirect upserts. No payload is read.
func (e *Engine) PreviewSeqs(ctx context.Context, id int64) ([]int, error) {
	rows, err := e.DB.Pool().Query(ctx, `
		SELECT seq FROM hatamoto_changeset_items
		WHERE changeset_id = $1 AND op = $2 AND kind IN ($3, $4) ORDER BY seq`, id, OpUpsert, string(KindEntity), string(KindRedirect))
	if err != nil {
		return nil, fmt.Errorf("failed to read items: %w", err)
	}
	defer rows.Close()
	var out []int
	for rows.Next() {
		var seq int
		if err := rows.Scan(&seq); err != nil {
			return nil, err
		}
		out = append(out, seq)
	}
	return out, rows.Err()
}
