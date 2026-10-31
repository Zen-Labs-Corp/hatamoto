package agents

import (
	"context"
	"fmt"

	"hatamoto.dev/core/internal/database"
)

// The first verified search engine crawl per page lives in
// hatamoto_agent_search_first_crawl, keyed by (entity, agent). It is a sibling
// of hatamoto_agent_first_crawl, which stays AI only and is untouched, and it
// fires no hook: it feeds the Google view (publish -> first crawl).
//
// Only a verified, served (below 400) search hit with an entity writes a row.
// Spoofed, unverifiable and pending hits never do, so a forged user agent
// cannot claim a first crawl. A later verification of an older hit lowers
// first_at (LEAST); it is never raised.

// Both inserts go in (entity_id, agent_id) order so two writers of the same
// rows (a verify run and the seed) cannot wait on each other in opposite order.

// searchFirstCrawlFromFlipped is the CTE body inserted in applyVerificationSQL:
// the hits that flipped to verified in this statement ($3 is the new state).
const searchFirstCrawlFromFlipped = `
INSERT INTO hatamoto_agent_search_first_crawl AS s (entity_id, agent_id, first_at, published_at)
SELECT f.entity_id, $1, min(f.at), e.published_at
FROM u f JOIN hatamoto_entities e ON e.id = f.entity_id
WHERE $3::text = 'verified' AND f.purpose = 'search' AND f.entity_id IS NOT NULL
  AND f.raw_status >= 200 AND f.raw_status < 400
GROUP BY f.entity_id, e.published_at
ORDER BY f.entity_id
ON CONFLICT (entity_id, agent_id) DO UPDATE SET first_at = LEAST(s.first_at, EXCLUDED.first_at)`

// seedSearchFirstCrawlSQL builds the same rows from the retained verified hits.
const seedSearchFirstCrawlSQL = `
INSERT INTO hatamoto_agent_search_first_crawl AS s (entity_id, agent_id, first_at, published_at)
SELECT h.entity_id, h.agent_id, min(h.occurred_at), e.published_at
FROM hatamoto_agent_hits h JOIN hatamoto_entities e ON e.id = h.entity_id
WHERE h.verification = 'verified' AND h.purpose = 'search' AND h.entity_id IS NOT NULL
  AND h.agent_id IS NOT NULL AND h.status >= 200 AND h.status < 400
GROUP BY h.entity_id, h.agent_id, e.published_at
ORDER BY h.entity_id, h.agent_id
ON CONFLICT (entity_id, agent_id) DO UPDATE SET first_at = LEAST(s.first_at, EXCLUDED.first_at)`

// SeedSearchFirstCrawl records the first crawl of every page from the verified
// search hits still in the raw table. Idempotent: it only ever lowers first_at.
func SeedSearchFirstCrawl(ctx context.Context, db *database.DB) (int64, error) {
	tag, err := db.Pool().Exec(ctx, seedSearchFirstCrawlSQL)
	if err != nil {
		return 0, fmt.Errorf("seed search first crawl: %w", err)
	}
	return tag.RowsAffected(), nil
}
