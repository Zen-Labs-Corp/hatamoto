package agents

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// FirstCrawlRow is the first time an AI agent fetched one published page.
type FirstCrawlRow struct {
	EntityID    uuid.UUID  `json:"entity_id"`
	Name        string     `json:"name"`
	Slug        string     `json:"slug"`
	AgentID     string     `json:"agent_id"`
	FirstAt     time.Time  `json:"first_crawled_at"`
	PublishedAt *time.Time `json:"published_at"`
}

// FirstCrawls lists the pages whose first AI crawl fell inside rg, newest
// first. A page published before first-crawl tracking began shows the first
// crawl since tracking began, not necessarily its real first crawl.
func (r *StatsRepository) FirstCrawls(ctx context.Context, rg Range, p Page) ([]FirstCrawlRow, int, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("get connection: %w", err)
	}
	defer conn.Release()
	const from = `FROM hatamoto_agent_first_crawl c JOIN hatamoto_entities e ON e.id = c.entity_id
		WHERE c.first_at >= $1 AND c.first_at < $2`
	var total int
	if err := conn.QueryRow(ctx, `SELECT COUNT(*) `+from, rg.From, rg.endExclusive()).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("first crawls count: %w", err)
	}
	rows, err := conn.Query(ctx, `SELECT c.entity_id, e.name, e.slug, c.agent_id, c.first_at, c.published_at `+from+`
		ORDER BY c.first_at DESC, c.entity_id LIMIT $3 OFFSET $4`, rg.From, rg.endExclusive(), p.Limit, p.Offset)
	if err != nil {
		return nil, 0, fmt.Errorf("first crawls: %w", err)
	}
	defer rows.Close()
	out := []FirstCrawlRow{}
	for rows.Next() {
		var f FirstCrawlRow
		if err := rows.Scan(&f.EntityID, &f.Name, &f.Slug, &f.AgentID, &f.FirstAt, &f.PublishedAt); err != nil {
			return nil, 0, err
		}
		out = append(out, f)
	}
	return out, total, rows.Err()
}

// AgentCount is a number of first crawls credited to one agent.
type AgentCount struct {
	AgentID string `json:"agent_id"`
	Count   int    `json:"count"`
}

// FirstCrawlAgg summarises every first crawl in a range (not one page of the
// list): how long after publishing the page was first fetched, and by whom.
// NotRecorded holds crawls with no publish time, or one after the crawl.
type FirstCrawlAgg struct {
	SameDay, D1to3, D4to7, D8Plus, NotRecorded int
	ByAgent                                    []AgentCount // most first, then by id
}

// FirstCrawlAggregates counts the first crawls that fell inside rg, by time since
// publishing and by agent, over the same rows as FirstCrawls.
func (r *StatsRepository) FirstCrawlAggregates(ctx context.Context, rg Range) (FirstCrawlAgg, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return FirstCrawlAgg{}, fmt.Errorf("get connection: %w", err)
	}
	defer conn.Release()
	const from = `FROM hatamoto_agent_first_crawl c JOIN hatamoto_entities e ON e.id = c.entity_id
		WHERE c.first_at >= $1 AND c.first_at < $2`
	var agg FirstCrawlAgg
	const recorded = `c.published_at IS NOT NULL AND c.first_at >= c.published_at`
	err = conn.QueryRow(ctx, `SELECT
		COUNT(*) FILTER (WHERE `+recorded+` AND c.first_at - c.published_at < interval '1 day'),
		COUNT(*) FILTER (WHERE `+recorded+` AND c.first_at - c.published_at >= interval '1 day' AND c.first_at - c.published_at < interval '4 days'),
		COUNT(*) FILTER (WHERE `+recorded+` AND c.first_at - c.published_at >= interval '4 days' AND c.first_at - c.published_at < interval '8 days'),
		COUNT(*) FILTER (WHERE `+recorded+` AND c.first_at - c.published_at >= interval '8 days'),
		COUNT(*) FILTER (WHERE NOT (`+recorded+`))
		`+from, rg.From, rg.endExclusive()).Scan(&agg.SameDay, &agg.D1to3, &agg.D4to7, &agg.D8Plus, &agg.NotRecorded)
	if err != nil {
		return FirstCrawlAgg{}, fmt.Errorf("first crawl buckets: %w", err)
	}
	rows, err := conn.Query(ctx, `SELECT c.agent_id, COUNT(*) `+from+` GROUP BY c.agent_id ORDER BY COUNT(*) DESC, c.agent_id`,
		rg.From, rg.endExclusive())
	if err != nil {
		return FirstCrawlAgg{}, fmt.Errorf("first crawls by agent: %w", err)
	}
	defer rows.Close()
	agg.ByAgent = []AgentCount{}
	for rows.Next() {
		var c AgentCount
		if err := rows.Scan(&c.AgentID, &c.Count); err != nil {
			return FirstCrawlAgg{}, err
		}
		agg.ByAgent = append(agg.ByAgent, c)
	}
	return agg, rows.Err()
}
