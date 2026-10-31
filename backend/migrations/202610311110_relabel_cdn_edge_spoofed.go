// hatamoto:backfill idempotent

package migrations

import (
	"context"
	"database/sql"
	"strings"

	"github.com/pressly/goose/v3"

	"hatamoto.dev/core/internal/agents"
)

func init() {
	goose.AddMigrationContext(relabelCDNEdgeSpoofed, noopDown)
}

// relabelCDNEdgeSpoofed turns the spoofed verdicts that were reached against a
// CDN edge address into unverifiable. Before the client address was derived
// correctly, hits behind the CDN stored the edge IP, so a genuine crawler
// looked like a forger. From an edge address the real origin is unknowable,
// so the honest verdict is unverifiable, not spoofed.
//
// hatamoto_agent_daily moves by the same per-(day, agent, path, status)
// deltas: spoofed_hits drops and unverifiable_hits rises by the relabelled
// count, so hits >= verified + spoofed + unverifiable still holds. Daily rows
// are locked first in the writer's order (day, agent_id, path, status; bytewise
// text) so a running writer or verify worker cannot deadlock with this.
// Re-running finds no spoofed edge hit left and changes nothing. No network.
func relabelCDNEdgeSpoofed(ctx context.Context, tx *sql.Tx) error {
	fallback := agents.CloudflareFallback()
	prefixes := make([]string, 0, len(fallback))
	for _, p := range fallback {
		prefixes = append(prefixes, p.String())
	}
	arr := "{" + strings.Join(prefixes, ",") + "}"
	if _, err := tx.ExecContext(ctx, `
SELECT 1 FROM public.hatamoto_agent_daily d
JOIN (
    SELECT DISTINCT agent_id, (occurred_at AT TIME ZONE 'UTC')::date AS day, path, COALESCE(status, 0) AS status
    FROM public.hatamoto_agent_hits
    WHERE verification = 'spoofed' AND agent_id IS NOT NULL AND NOT ip_truncated
      AND ip <<= ANY($1::text::inet[])
) k ON d.day = k.day AND d.agent_id = k.agent_id AND d.path = k.path AND d.status = k.status
ORDER BY d.day, d.agent_id COLLATE "C", d.path COLLATE "C", d.status
FOR UPDATE OF d`, arr); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `
WITH moved AS (
    UPDATE public.hatamoto_agent_hits
    SET verification = 'unverifiable'
    WHERE verification = 'spoofed' AND NOT ip_truncated AND ip <<= ANY($1::text::inet[])
    RETURNING agent_id, (occurred_at AT TIME ZONE 'UTC')::date AS day, path, COALESCE(status, 0) AS status
), c AS (
    SELECT agent_id, day, path, status, count(*) AS n
    FROM moved
    WHERE agent_id IS NOT NULL
    GROUP BY 1, 2, 3, 4
)
UPDATE public.hatamoto_agent_daily d
SET spoofed_hits = d.spoofed_hits - c.n, unverifiable_hits = d.unverifiable_hits + c.n
FROM c
WHERE d.day = c.day AND d.agent_id = c.agent_id AND d.path = c.path AND d.status = c.status`, arr)
	return err
}
