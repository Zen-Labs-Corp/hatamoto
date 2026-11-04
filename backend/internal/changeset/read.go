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
	var h Header
	err := e.DB.Pool().QueryRow(ctx, `SELECT id, run_id, plugin, state, expires_at, item_count FROM hatamoto_changesets WHERE id = $1`, id).
		Scan(&h.ID, &h.RunID, &h.Plugin, &h.State, &h.ExpiresAt, &h.ItemCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return Header{}, nil, ErrNotFound
	}
	if err != nil {
		return Header{}, nil, fmt.Errorf("failed to read changeset: %w", err)
	}
	items, err := loadItems(ctx, e.DB.Pool(), id, false)
	if err != nil {
		return Header{}, nil, err
	}
	return h, items, nil
}
