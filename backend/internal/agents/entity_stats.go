package agents

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// LastFetch is the most recent fetch of an entity by one agent.
type LastFetch struct {
	At     time.Time
	Status *int
}

// EntityActivityData is the raw data behind GET /entities/{id}.
type EntityActivityData struct {
	Exists       bool
	FirstAt      *time.Time // from hatamoto_agent_first_crawl
	PublishedAt  *time.Time
	TrackingFrom *time.Time // when first-crawl tracking began (hatamoto_agent_tracking)
	UserFetch7d  int64
	UserFetch30d int64
	Last         map[string]LastFetch // by agent id
}

// EntityStore reads per-entity AI activity.
type EntityStore interface {
	EntityActivity(ctx context.Context, entityID uuid.UUID, agentIDs []string, today time.Time) (EntityActivityData, error)
}

// EntityActivity combines three sources, each used where it is the best one:
//   - counts (7d/30d user fetches): the daily rollup, complete by construction
//     (the writer upserts it for every hit), so no raw-hit double counting;
//   - last fetch per agent: raw hits (second precision, ~90 days); an agent
//     whose hits aged out falls back to the newest rollup day at 00:00 UTC;
//   - first crawl: its own table, which outlives hit retention.
func (r *StatsRepository) EntityActivity(ctx context.Context, entityID uuid.UUID, agentIDs []string, today time.Time) (EntityActivityData, error) {
	d := EntityActivityData{Last: map[string]LastFetch{}}
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return d, fmt.Errorf("get connection: %w", err)
	}
	defer conn.Release()

	if err := conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM hatamoto_entities WHERE id = $1)`, entityID).Scan(&d.Exists); err != nil {
		return d, fmt.Errorf("entity exists: %w", err)
	}
	if !d.Exists {
		return d, nil
	}

	rows, err := conn.Query(ctx, `SELECT first_at, published_at FROM hatamoto_agent_first_crawl WHERE entity_id = $1`, entityID)
	if err != nil {
		return d, fmt.Errorf("first crawl: %w", err)
	}
	for rows.Next() {
		if err := rows.Scan(&d.FirstAt, &d.PublishedAt); err != nil {
			rows.Close()
			return d, err
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return d, err
	}

	if err := conn.QueryRow(ctx, `SELECT min(started_at) FROM hatamoto_agent_tracking`).Scan(&d.TrackingFrom); err != nil {
		return d, fmt.Errorf("tracking start: %w", err)
	}

	// Served user fetches (status < 400, 0 = unknown).
	if err := conn.QueryRow(ctx, `
		SELECT COALESCE(SUM(hits) FILTER (WHERE day >= $2), 0), COALESCE(SUM(hits), 0)
		FROM hatamoto_agent_daily
		WHERE entity_id = $1 AND purpose = 'user_fetch' AND status < 400 AND day >= $3`,
		entityID, today.AddDate(0, 0, -6), today.AddDate(0, 0, -29)).Scan(&d.UserFetch7d, &d.UserFetch30d); err != nil {
		return d, fmt.Errorf("user fetch counts: %w", err)
	}

	// Rollup first, then raw hits overwrite (more precise).
	for _, q := range []string{
		`SELECT DISTINCT ON (agent_id) agent_id, (day::timestamp AT TIME ZONE 'UTC'), status::int
		 FROM hatamoto_agent_daily WHERE entity_id = $1 AND agent_id = ANY($2)
		 ORDER BY agent_id, day DESC, hits DESC`,
		`SELECT DISTINCT ON (agent_id) agent_id, occurred_at, status::int
		 FROM hatamoto_agent_hits WHERE entity_id = $1 AND agent_id = ANY($2)
		 ORDER BY agent_id, occurred_at DESC`,
	} {
		rows, err := conn.Query(ctx, q, entityID, agentIDs)
		if err != nil {
			return d, fmt.Errorf("last fetch: %w", err)
		}
		for rows.Next() {
			var id string
			var lf LastFetch
			if err := rows.Scan(&id, &lf.At, &lf.Status); err != nil {
				rows.Close()
				return d, err
			}
			d.Last[id] = lf
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return d, err
		}
	}
	return d, nil
}
