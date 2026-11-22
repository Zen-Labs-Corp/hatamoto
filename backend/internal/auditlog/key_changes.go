package auditlog

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"hatamoto.dev/core/internal/jsonx"
)

// MaxKeyChanges is the most rows KeyChanges returns.
const MaxKeyChanges = 1000

// KeyChange is one audit row written for an API key.
type KeyChange struct {
	ID                                 int64
	At                                 time.Time
	Actor, Action, Target, Result, Via string
	Detail                             map[string]any
}

// The key id, not the actor name, ties a row to a key: names are mutable and
// actor_id holds the key's creator.
const keyChangesSQL = `SELECT id, at, coalesce(actor, ''), action, coalesce(target, ''),
	coalesce(detail, '{}'::jsonb), coalesce(result, ''), coalesce(detail->>'via', '')
FROM hatamoto_admin_audit
WHERE actor_key_id = $1 AND at >= $2
ORDER BY at, id
LIMIT $3`

// KeyChanges lists the rows written for keyID since since, oldest first. A
// limit outside 1..MaxKeyChanges means MaxKeyChanges.
func KeyChanges(ctx context.Context, pool *pgxpool.Pool, keyID uuid.UUID, since time.Time, limit int) ([]KeyChange, error) {
	if limit <= 0 || limit > MaxKeyChanges {
		limit = MaxKeyChanges
	}
	rows, err := pool.Query(ctx, keyChangesSQL, keyID, since.UTC(), limit)
	if err != nil {
		return nil, fmt.Errorf("list key changes: %w", err)
	}
	defer rows.Close()
	var out []KeyChange
	for rows.Next() {
		var (
			c   KeyChange
			raw []byte
		)
		if err := rows.Scan(&c.ID, &c.At, &c.Actor, &c.Action, &c.Target, &raw, &c.Result, &c.Via); err != nil {
			return nil, fmt.Errorf("scan key change: %w", err)
		}
		c.At = c.At.UTC()
		_ = jsonx.Unmarshal(raw, &c.Detail, jsonx.Stored) // a malformed detail shows as empty
		out = append(out, c)
	}
	return out, rows.Err()
}
