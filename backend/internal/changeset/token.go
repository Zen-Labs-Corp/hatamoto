package changeset

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// liveToken is the token of the live object (see Item.BaseToken), or "" when
// it does not exist.
func liveToken(ctx context.Context, tx pgx.Tx, kind Kind, targetID string) (string, error) {
	id, err := uuid.Parse(targetID)
	if err != nil {
		return "", fmt.Errorf("%w: target %q is not an id", ErrInvalidItem, targetID)
	}
	var at time.Time
	if kind == KindEntity {
		var version int
		err = tx.QueryRow(ctx, `SELECT version, updated_at FROM hatamoto_entities WHERE id = $1`, id).Scan(&version, &at)
		if err == nil {
			return EntityToken(version, at), nil
		}
	} else {
		table, ok := originTables[kind]
		if !ok {
			return "", fmt.Errorf("%w: %q", ErrUnknownKind, kind)
		}
		err = tx.QueryRow(ctx, `SELECT COALESCE(updated_at, created_at) FROM `+table+` WHERE id = $1`, id).Scan(&at)
		if err == nil {
			return TimeToken(at), nil
		}
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return "", fmt.Errorf("failed to read token: %w", err)
}

// recordAfterTokens stores, for every applied item that left an object behind,
// the token of that object as the publish ends. Revert compares against it.
func recordAfterTokens(ctx context.Context, tx pgx.Tx, applied []appliedRef) error {
	for _, a := range applied {
		tok, err := liveToken(ctx, tx, a.kind, a.targetID)
		if err != nil {
			return err
		}
		if tok == "" {
			continue
		}
		if _, err := tx.Exec(ctx, `UPDATE hatamoto_changeset_items SET after_token = $2 WHERE id = $1`, a.itemID, tok); err != nil {
			return fmt.Errorf("failed to record token: %w", err)
		}
	}
	return nil
}

type appliedRef struct {
	itemID   int64
	kind     Kind
	targetID string
}
