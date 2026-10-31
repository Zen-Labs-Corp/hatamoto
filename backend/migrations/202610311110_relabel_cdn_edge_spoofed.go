// hatamoto:backfill idempotent

package migrations

import (
	"context"
	"database/sql"
	"strings"

	"github.com/pressly/goose/v3"
)

// relabelCDNEdgePrefixes is a frozen copy of the Cloudflare edge list as of
// 2026-10-30. A migration must not follow the live list, which later releases
// may edit.
var relabelCDNEdgePrefixes = []string{
	"173.245.48.0/20", "103.21.244.0/22", "103.22.200.0/22", "103.31.4.0/22",
	"141.101.64.0/18", "108.162.192.0/18", "190.93.240.0/20", "188.114.96.0/20",
	"197.234.240.0/22", "198.41.128.0/17", "162.158.0.0/15", "104.16.0.0/13",
	"104.24.0.0/14", "172.64.0.0/13", "131.0.72.0/22",
	"2400:cb00::/32", "2606:4700::/32", "2803:f800::/32", "2405:b500::/32",
	"2405:8100::/32", "2a06:98c0::/29", "2c0f:f248::/32",
}

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
	arr := "{" + strings.Join(relabelCDNEdgePrefixes, ",") + "}"
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
