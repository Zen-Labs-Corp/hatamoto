package agents

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/database"
)

// Read side of agent analytics for the admin API.
//
// Hits vs rollup: hatamoto_agent_daily is written by the Writer for EVERY hit
// (same transaction as the raw row) and kept long-term, so it is complete for
// any date range, including days whose raw hits were already dropped by the
// ~90 day partition retention. Counts therefore come from the rollup only;
// adding raw hits on top would double count. The raw hits table is read only
// where the rollup cannot answer: second-precision timestamps for recent
// activity, and the spoof log (the rollup has no IP, by design).
//
// Privacy: no query here returns hatamoto_agent_hits.ip. The spoof log returns
// the /24 (v4) or /48 (v6) network computed in SQL; VerificationHealth only
// counts distinct addresses.

// aiPurposes are the purposes counted as "AI" fetches.
var aiPurposes = []string{
	string(PurposeTraining), string(PurposeSearchIndex), string(PurposeUserFetch), string(PurposeOtherAI),
}

// Range is an inclusive UTC date range.
type Range struct{ From, To time.Time }

// endExclusive is the first instant after the range, for timestamptz filters.
func (r Range) endExclusive() time.Time { return r.To.AddDate(0, 0, 1) }

// Page is a limit/offset window.
type Page struct{ Limit, Offset int }

// OverviewTotals are the headline numbers. Verified/Spoofed/Unverifiable only
// count hits the verifier has already decided (pending hits are in AIFetches
// but in none of the three).
type OverviewTotals struct {
	AIFetches    int64
	Verified     int64
	Spoofed      int64
	Unverifiable int64
	AIVisits     int64 // hatamoto_analytics_daily ai_referral rows
	Pageviews    int64 // hatamoto_analytics_daily pageview rows
}

// SeriesPoint is hits for one day and one group key (operator or purpose).
type SeriesPoint struct {
	Day  string `json:"day"`
	Key  string `json:"key"`
	Hits int64  `json:"hits"`
}

// AgentTotals aggregates one agent over a range.
type AgentTotals struct {
	AgentID      string `json:"agent_id"`
	Operator     string `json:"operator"`
	Purpose      string `json:"purpose"`
	Hits         int64  `json:"hits"`
	Verified     int64  `json:"verified_hits"`
	Spoofed      int64  `json:"spoofed_hits"`
	Unverifiable int64  `json:"unverifiable_hits"`
	LastSeenDay  string `json:"last_seen_day"`
}

// PageRow is one fetched path.
type PageRow struct {
	Path       string     `json:"path"`
	EntityID   *uuid.UUID `json:"entity_id"`
	EntityName *string    `json:"entity_name"`
	Hits       int64      `json:"hits"`
	Agents     int64      `json:"agents"`
	LastDay    string     `json:"last_fetch_day"`
	SchemaSlug *string    `json:"schema_slug"`
}

// NeverFetchedRow is a published entity no AI agent has ever fetched.
type NeverFetchedRow struct {
	EntityID    uuid.UUID  `json:"entity_id"`
	Name        string     `json:"name"`
	Slug        string     `json:"slug"`
	SchemaSlug  *string    `json:"schema_slug"`
	PublishedAt *time.Time `json:"published_at"`
}

// ErrorRow is one (path, status>=400) group.
type ErrorRow struct {
	Path     string   `json:"path"`
	Status   int      `json:"status"`
	Hits     int64    `json:"hits"`
	AgentIDs []string `json:"agent_ids"`
	LastDay  string   `json:"last_seen_day"`
}

// SpoofedRow is one spoofed hit. IPMasked is the /24 or /48 network, never the
// stored address.
type SpoofedRow struct {
	OccurredAt time.Time `json:"occurred_at"`
	AgentID    string    `json:"agent_id"`
	Operator   string    `json:"operator"`
	Path       string    `json:"path"`
	Status     *int      `json:"status"`
	IPMasked   *string   `json:"ip_masked"`
}

// StatusCount is hits for one status code.
type StatusCount struct {
	Status int   `json:"status"`
	Hits   int64 `json:"hits"`
}

// PathCount is hits for one path.
type PathCount struct {
	Path string `json:"path"`
	Hits int64  `json:"hits"`
}

// DayCount is hits for one day.
type DayCount struct {
	Day  string `json:"day"`
	Hits int64  `json:"hits"`
}

// AgentDetailData is the data behind GET /agents/{id}.
type AgentDetailData struct {
	Totals     AgentTotals
	Series     []DayCount
	TopPaths   []PathCount
	ByStatus   []StatusCount
	LastSeenAt *time.Time
	// PagesRead is the distinct paths the agent was served in the range (TopPaths is capped at 10).
	PagesRead int
	Found     bool
}

// StatsStore is the read interface the admin handlers depend on.
type StatsStore interface {
	Overview(ctx context.Context, rg Range) (OverviewTotals, error)
	Series(ctx context.Context, rg Range, groupBy string) ([]SeriesPoint, error)
	SeriesWithSearch(ctx context.Context, rg Range, groupBy string) ([]SeriesPoint, error)
	TopAgents(ctx context.Context, rg Range, limit int) ([]AgentTotals, error)
	TopPages(ctx context.Context, rg Range, p Page) ([]PageRow, int, error)
	NeverFetched(ctx context.Context, p Page) ([]NeverFetchedRow, int, error)
	Agents(ctx context.Context, rg Range) ([]AgentTotals, error)
	AgentDetail(ctx context.Context, rg Range, agentID string) (AgentDetailData, error)
	Errors(ctx context.Context, rg Range, p Page) ([]ErrorRow, int, error)
	Spoofed(ctx context.Context, rg Range, p Page) ([]SpoofedRow, int, error)
}

// StatsRepository is the Postgres StatsStore.
type StatsRepository struct{ db *database.DB }

// NewStatsRepository creates the Postgres-backed stats store.
func NewStatsRepository(db *database.DB) *StatsRepository { return &StatsRepository{db: db} }

const dayFmt = "2006-01-02"

func (r *StatsRepository) Overview(ctx context.Context, rg Range) (OverviewTotals, error) {
	var t OverviewTotals
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return t, fmt.Errorf("get connection: %w", err)
	}
	defer conn.Release()

	if err := conn.QueryRow(ctx, `
		SELECT COALESCE(SUM(hits),0), COALESCE(SUM(verified_hits),0),
		       COALESCE(SUM(spoofed_hits),0), COALESCE(SUM(unverifiable_hits),0)
		FROM hatamoto_agent_daily
		WHERE day BETWEEN $1 AND $2 AND purpose = ANY($3)`,
		rg.From, rg.To, aiPurposes).Scan(&t.AIFetches, &t.Verified, &t.Spoofed, &t.Unverifiable); err != nil {
		return t, fmt.Errorf("overview totals: %w", err)
	}
	// AI-referred visits and pageviews come from the daily analytics rollup.
	if err := conn.QueryRow(ctx, `
		SELECT COALESCE(SUM(count) FILTER (WHERE kind = 'ai_referral'), 0),
		       COALESCE(SUM(count) FILTER (WHERE kind = 'pageview'), 0)
		FROM hatamoto_analytics_daily
		WHERE day BETWEEN $1 AND $2`,
		rg.From, rg.To).Scan(&t.AIVisits, &t.Pageviews); err != nil {
		return t, fmt.Errorf("overview analytics: %w", err)
	}
	return t, nil
}

// Series returns daily hits grouped by "operator" or "purpose" (caller
// whitelists groupBy; it is interpolated as a column name).
func (r *StatsRepository) Series(ctx context.Context, rg Range, groupBy string) ([]SeriesPoint, error) {
	return r.series(ctx, rg, groupBy, aiPurposes)
}

func (r *StatsRepository) series(ctx context.Context, rg Range, groupBy string, purposes []string) ([]SeriesPoint, error) {
	if groupBy != "operator" && groupBy != "purpose" {
		return nil, fmt.Errorf("invalid group %q", groupBy)
	}
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, fmt.Errorf("get connection: %w", err)
	}
	defer conn.Release()
	rows, err := conn.Query(ctx, fmt.Sprintf(`
		SELECT day, COALESCE(NULLIF(%[1]s,''),'unknown') AS k, SUM(hits)
		FROM hatamoto_agent_daily
		WHERE day BETWEEN $1 AND $2 AND purpose = ANY($3)
		GROUP BY day, k ORDER BY day, k`, groupBy), rg.From, rg.To, purposes)
	if err != nil {
		return nil, fmt.Errorf("series: %w", err)
	}
	defer rows.Close()
	out := []SeriesPoint{}
	for rows.Next() {
		var d time.Time
		var p SeriesPoint
		if err := rows.Scan(&d, &p.Key, &p.Hits); err != nil {
			return nil, err
		}
		p.Day = d.Format(dayFmt)
		out = append(out, p)
	}
	return out, rows.Err()
}

const agentTotalsSQL = `
	SELECT agent_id, MAX(operator), MAX(purpose), SUM(hits), SUM(verified_hits),
	       SUM(spoofed_hits), SUM(unverifiable_hits), MAX(day)
	FROM hatamoto_agent_daily
	WHERE day BETWEEN $1 AND $2 %s
	GROUP BY agent_id
	ORDER BY SUM(hits) DESC, agent_id`

func (r *StatsRepository) scanAgentTotals(ctx context.Context, q string, args ...any) ([]AgentTotals, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, fmt.Errorf("get connection: %w", err)
	}
	defer conn.Release()
	rows, err := conn.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("agent totals: %w", err)
	}
	defer rows.Close()
	out := []AgentTotals{}
	for rows.Next() {
		var a AgentTotals
		var last time.Time
		if err := rows.Scan(&a.AgentID, &a.Operator, &a.Purpose, &a.Hits, &a.Verified, &a.Spoofed, &a.Unverifiable, &last); err != nil {
			return nil, err
		}
		a.LastSeenDay = last.Format(dayFmt)
		out = append(out, a)
	}
	return out, rows.Err()
}

// TopAgents returns the AI agents with the most hits.
func (r *StatsRepository) TopAgents(ctx context.Context, rg Range, limit int) ([]AgentTotals, error) {
	return r.scanAgentTotals(ctx, fmt.Sprintf(agentTotalsSQL+" LIMIT $4", "AND purpose = ANY($3)"),
		rg.From, rg.To, aiPurposes, limit)
}

// Agents lists every agent (any purpose) seen in the range.
func (r *StatsRepository) Agents(ctx context.Context, rg Range) ([]AgentTotals, error) {
	return r.scanAgentTotals(ctx, fmt.Sprintf(agentTotalsSQL, ""), rg.From, rg.To)
}

// Top pages group by path only: entity_id is not part of the rollup key, so one
// path can have NULL and set entity rows. The entity (and its name) is the
// first non-null one; Postgres has no max(uuid).
const (
	topPagesGroupBy = `GROUP BY d.path`
	topPagesSelect  = `SELECT d.path,
		(array_agg(d.entity_id ORDER BY d.entity_id) FILTER (WHERE d.entity_id IS NOT NULL))[1],
		(array_agg(e.name ORDER BY d.entity_id) FILTER (WHERE d.entity_id IS NOT NULL))[1],
		SUM(d.hits), COUNT(DISTINCT d.agent_id), MAX(d.day),
		(array_agg(s.slug ORDER BY d.entity_id) FILTER (WHERE d.entity_id IS NOT NULL))[1] `
)

func (r *StatsRepository) TopPages(ctx context.Context, rg Range, p Page) ([]PageRow, int, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("get connection: %w", err)
	}
	defer conn.Release()
	// Served fetches only (status < 400, 0 = unknown); failures live in Errors.
	const where = `FROM hatamoto_agent_daily d
		LEFT JOIN hatamoto_entities e ON e.id = d.entity_id
		LEFT JOIN hatamoto_entity_schemas s ON s.id = e.schema_id
		WHERE d.day BETWEEN $1 AND $2 AND d.purpose = ANY($3) AND d.status < 400`
	var total int
	if err := conn.QueryRow(ctx, `SELECT COUNT(*) FROM (SELECT 1 `+where+` `+topPagesGroupBy+`) g`,
		rg.From, rg.To, aiPurposes).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("top pages count: %w", err)
	}
	rows, err := conn.Query(ctx, `
		`+topPagesSelect+where+`
		`+topPagesGroupBy+`
		ORDER BY SUM(d.hits) DESC, d.path
		LIMIT $4 OFFSET $5`, rg.From, rg.To, aiPurposes, p.Limit, p.Offset)
	if err != nil {
		return nil, 0, fmt.Errorf("top pages: %w", err)
	}
	defer rows.Close()
	out := []PageRow{}
	for rows.Next() {
		var pr PageRow
		var last time.Time
		if err := rows.Scan(&pr.Path, &pr.EntityID, &pr.EntityName, &pr.Hits, &pr.Agents, &last, &pr.SchemaSlug); err != nil {
			return nil, 0, err
		}
		pr.LastDay = last.Format(dayFmt)
		out = append(out, pr)
	}
	return out, total, rows.Err()
}

// Variant rows are plugin-owned and share the base slug; hits resolve to the
// base entity, so a variant can never be fetched (same filter as public/sitemap).
const publishedFilter = `e.status = 'published' AND e.variant_key IS NULL`

const neverFetchedFrom = `FROM hatamoto_entities e
		LEFT JOIN hatamoto_entity_schemas s ON s.id = e.schema_id
		WHERE ` + publishedFilter + ` AND NOT EXISTS (
			SELECT 1 FROM hatamoto_agent_daily d
			WHERE d.entity_id = e.id AND d.purpose = ANY($1) AND d.status < 400)`

// PublishedPages counts the pages coverage is measured against: the same
// filter as NeverFetched, so fetched plus never-fetched always adds up.
func (r *StatsRepository) PublishedPages(ctx context.Context) (int, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return 0, fmt.Errorf("get connection: %w", err)
	}
	defer conn.Release()
	var n int
	if err := conn.QueryRow(ctx, `SELECT COUNT(*) FROM hatamoto_entities e WHERE `+publishedFilter).Scan(&n); err != nil {
		return 0, fmt.Errorf("published pages: %w", err)
	}
	return n, nil
}

// NeverFetched lists published entities with no served AI fetch in the whole
// rollup (all time, not just the selected range: "never" means never).
func (r *StatsRepository) NeverFetched(ctx context.Context, p Page) ([]NeverFetchedRow, int, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("get connection: %w", err)
	}
	defer conn.Release()
	const where = neverFetchedFrom
	var total int
	if err := conn.QueryRow(ctx, `SELECT COUNT(*) `+where, aiPurposes).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("never fetched count: %w", err)
	}
	rows, err := conn.Query(ctx, `SELECT e.id, e.name, e.slug, s.slug, e.published_at `+where+`
		ORDER BY e.published_at DESC NULLS LAST, e.name, e.id
		LIMIT $2 OFFSET $3`, aiPurposes, p.Limit, p.Offset)
	if err != nil {
		return nil, 0, fmt.Errorf("never fetched: %w", err)
	}
	defer rows.Close()
	out := []NeverFetchedRow{}
	for rows.Next() {
		var n NeverFetchedRow
		if err := rows.Scan(&n.EntityID, &n.Name, &n.Slug, &n.SchemaSlug, &n.PublishedAt); err != nil {
			return nil, 0, err
		}
		out = append(out, n)
	}
	return out, total, rows.Err()
}

func (r *StatsRepository) AgentDetail(ctx context.Context, rg Range, agentID string) (AgentDetailData, error) {
	var d AgentDetailData
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return d, fmt.Errorf("get connection: %w", err)
	}
	defer conn.Release()

	// All-time existence: an agent with rollup rows outside the range still exists.
	var anyRows bool
	if err := conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM hatamoto_agent_daily WHERE agent_id = $1)`, agentID).Scan(&anyRows); err != nil {
		return d, fmt.Errorf("agent exists: %w", err)
	}
	d.Found = anyRows
	d.Totals.AgentID = agentID
	if !anyRows {
		return d, nil
	}
	var last *time.Time
	if err := conn.QueryRow(ctx, `
		SELECT COALESCE(MAX(operator),''), COALESCE(MAX(purpose),''), COALESCE(SUM(hits),0),
		       COALESCE(SUM(verified_hits),0), COALESCE(SUM(spoofed_hits),0), COALESCE(SUM(unverifiable_hits),0), MAX(day)
		FROM hatamoto_agent_daily WHERE agent_id = $1 AND day BETWEEN $2 AND $3`,
		agentID, rg.From, rg.To).Scan(&d.Totals.Operator, &d.Totals.Purpose, &d.Totals.Hits,
		&d.Totals.Verified, &d.Totals.Spoofed, &d.Totals.Unverifiable, &last); err != nil {
		return d, fmt.Errorf("agent totals: %w", err)
	}
	if last != nil {
		d.Totals.LastSeenDay = last.Format(dayFmt)
	}
	// Served fetches only (status < 400, 0 = unknown), like TopPages.
	if err := conn.QueryRow(ctx, `SELECT COUNT(DISTINCT path) FROM hatamoto_agent_daily
		WHERE agent_id = $1 AND day BETWEEN $2 AND $3 AND status < 400`, agentID, rg.From, rg.To).Scan(&d.PagesRead); err != nil {
		return d, fmt.Errorf("agent pages read: %w", err)
	}
	// Precise last seen from raw hits; when they were purged fall back to the
	// last rollup day (any range) at midnight UTC.
	if err := conn.QueryRow(ctx, `
		SELECT COALESCE(
			(SELECT MAX(occurred_at) FROM hatamoto_agent_hits WHERE agent_id = $1),
			(SELECT MAX(day)::timestamp AT TIME ZONE 'UTC' FROM hatamoto_agent_daily WHERE agent_id = $1))`,
		agentID).Scan(&d.LastSeenAt); err != nil {
		return d, fmt.Errorf("agent last seen: %w", err)
	}

	d.Series = []DayCount{}
	rows, err := conn.Query(ctx, `SELECT day, SUM(hits) FROM hatamoto_agent_daily
		WHERE agent_id = $1 AND day BETWEEN $2 AND $3 GROUP BY day ORDER BY day`, agentID, rg.From, rg.To)
	if err != nil {
		return d, fmt.Errorf("agent series: %w", err)
	}
	for rows.Next() {
		var day time.Time
		var c DayCount
		if err := rows.Scan(&day, &c.Hits); err != nil {
			rows.Close()
			return d, err
		}
		c.Day = day.Format(dayFmt)
		d.Series = append(d.Series, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return d, err
	}

	d.TopPaths = []PathCount{}
	rows, err = conn.Query(ctx, `SELECT path, SUM(hits) FROM hatamoto_agent_daily
		WHERE agent_id = $1 AND day BETWEEN $2 AND $3 GROUP BY path ORDER BY SUM(hits) DESC, path LIMIT 10`,
		agentID, rg.From, rg.To)
	if err != nil {
		return d, fmt.Errorf("agent top paths: %w", err)
	}
	for rows.Next() {
		var c PathCount
		if err := rows.Scan(&c.Path, &c.Hits); err != nil {
			rows.Close()
			return d, err
		}
		d.TopPaths = append(d.TopPaths, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return d, err
	}

	d.ByStatus = []StatusCount{}
	rows, err = conn.Query(ctx, `SELECT status, SUM(hits) FROM hatamoto_agent_daily
		WHERE agent_id = $1 AND day BETWEEN $2 AND $3 GROUP BY status ORDER BY status`, agentID, rg.From, rg.To)
	if err != nil {
		return d, fmt.Errorf("agent by status: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var c StatusCount
		if err := rows.Scan(&c.Status, &c.Hits); err != nil {
			return d, err
		}
		d.ByStatus = append(d.ByStatus, c)
	}
	return d, rows.Err()
}

// Errors groups failed (status >= 400) agent fetches by path and status. Read
// from the rollup, so it covers the whole range regardless of hit retention.
func (r *StatsRepository) Errors(ctx context.Context, rg Range, p Page) ([]ErrorRow, int, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("get connection: %w", err)
	}
	defer conn.Release()
	const where = `FROM hatamoto_agent_daily WHERE day BETWEEN $1 AND $2 AND status >= 400`
	var total int
	if err := conn.QueryRow(ctx, `SELECT COUNT(*) FROM (SELECT 1 `+where+` GROUP BY path, status) g`, rg.From, rg.To).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("errors count: %w", err)
	}
	rows, err := conn.Query(ctx, `SELECT path, status, SUM(hits), array_agg(DISTINCT agent_id), MAX(day) `+where+`
		GROUP BY path, status ORDER BY SUM(hits) DESC, path, status LIMIT $3 OFFSET $4`, rg.From, rg.To, p.Limit, p.Offset)
	if err != nil {
		return nil, 0, fmt.Errorf("errors: %w", err)
	}
	defer rows.Close()
	out := []ErrorRow{}
	for rows.Next() {
		var e ErrorRow
		var last time.Time
		if err := rows.Scan(&e.Path, &e.Status, &e.Hits, &e.AgentIDs, &last); err != nil {
			return nil, 0, err
		}
		if e.AgentIDs == nil {
			e.AgentIDs = []string{}
		}
		e.LastDay = last.Format(dayFmt)
		out = append(out, e)
	}
	return out, total, rows.Err()
}

// maskedIPSQL renders the /24 (v4) or /48 (v6) network of ip. The raw address
// is never selected.
const maskedIPSQL = `network(set_masklen(ip, CASE family(ip) WHEN 4 THEN 24 ELSE 48 END))::text`

// Spoofed lists spoofed hits (raw hits table: ~90 days) newest first.
func (r *StatsRepository) Spoofed(ctx context.Context, rg Range, p Page) ([]SpoofedRow, int, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("get connection: %w", err)
	}
	defer conn.Release()
	const where = `FROM hatamoto_agent_hits WHERE verification = 'spoofed' AND occurred_at >= $1 AND occurred_at < $2`
	var total int
	if err := conn.QueryRow(ctx, `SELECT COUNT(*) `+where, rg.From, rg.endExclusive()).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("spoofed count: %w", err)
	}
	rows, err := conn.Query(ctx, `SELECT occurred_at, COALESCE(agent_id,''), COALESCE(operator,''), COALESCE(path,''), status, `+maskedIPSQL+` `+where+`
		ORDER BY occurred_at DESC LIMIT $3 OFFSET $4`, rg.From, rg.endExclusive(), p.Limit, p.Offset)
	if err != nil {
		return nil, 0, fmt.Errorf("spoofed: %w", err)
	}
	defer rows.Close()
	out := []SpoofedRow{}
	for rows.Next() {
		var s SpoofedRow
		var status *int16
		if err := rows.Scan(&s.OccurredAt, &s.AgentID, &s.Operator, &s.Path, &status, &s.IPMasked); err != nil {
			return nil, 0, err
		}
		if status != nil {
			v := int(*status)
			s.Status = &v
		}
		out = append(out, s)
	}
	return out, total, rows.Err()
}

// otherPagesSlug keys fetches whose path is not an entity (home, listings,
// files). It is empty: a schema slug never is, so it cannot clash with a real one.
const otherPagesSlug = ""

// FetchesBySchema returns daily served AI fetches grouped by page type (schema
// slug). Fetches with no entity are keyed "".
func (r *StatsRepository) FetchesBySchema(ctx context.Context, rg Range) ([]SeriesPoint, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, fmt.Errorf("get connection: %w", err)
	}
	defer conn.Release()
	rows, err := conn.Query(ctx, `
		SELECT d.day, COALESCE(s.slug, $4), SUM(d.hits)
		FROM hatamoto_agent_daily d
		LEFT JOIN hatamoto_entities e ON e.id = d.entity_id
		LEFT JOIN hatamoto_entity_schemas s ON s.id = e.schema_id
		WHERE d.day BETWEEN $1 AND $2 AND d.purpose = ANY($3) AND d.status < 400
		GROUP BY d.day, COALESCE(s.slug, $4) ORDER BY d.day, 2`, rg.From, rg.To, aiPurposes, otherPagesSlug)
	if err != nil {
		return nil, fmt.Errorf("fetches by schema: %w", err)
	}
	defer rows.Close()
	out := []SeriesPoint{}
	for rows.Next() {
		var d time.Time
		var p SeriesPoint
		if err := rows.Scan(&d, &p.Key, &p.Hits); err != nil {
			return nil, err
		}
		p.Day = d.Format(dayFmt)
		out = append(out, p)
	}
	return out, rows.Err()
}

// SchemaCount is a number of pages of one page type (schema).
type SchemaCount struct {
	SchemaSlug *string `json:"schema_slug"`
	Count      int     `json:"count"`
}

// NeverFetchedBySchema counts every never-fetched page per page type, not just
// the page of NeverFetched. Largest first.
func (r *StatsRepository) NeverFetchedBySchema(ctx context.Context) ([]SchemaCount, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, fmt.Errorf("get connection: %w", err)
	}
	defer conn.Release()
	rows, err := conn.Query(ctx, `SELECT s.slug, COUNT(*) `+neverFetchedFrom+` GROUP BY s.slug ORDER BY COUNT(*) DESC, s.slug`, aiPurposes)
	if err != nil {
		return nil, fmt.Errorf("never fetched by schema: %w", err)
	}
	defer rows.Close()
	out := []SchemaCount{}
	for rows.Next() {
		var c SchemaCount
		if err := rows.Scan(&c.SchemaSlug, &c.Count); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// HealthDay is one day of crawler health. Hits and the status classes count every
// agent (site-wide, like Errors); the verification split counts AI purposes only
// (like Overview). Status 0 (unknown) is in Hits but in no class.
type HealthDay struct {
	Day                    string
	Hits                   int64
	S2xx, S3xx, S4xx, S5xx int64
	// AI4xx and AI5xx are the 4xx and 5xx responses to AI purposes only.
	AI4xx, AI5xx                  int64
	Verified, Spoofed, Unverified int64
}

// HealthSeries returns one row per day with data in the range, oldest first.
func (r *StatsRepository) HealthSeries(ctx context.Context, rg Range) ([]HealthDay, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, fmt.Errorf("get connection: %w", err)
	}
	defer conn.Release()
	rows, err := conn.Query(ctx, `
		SELECT day, SUM(hits),
		       COALESCE(SUM(hits) FILTER (WHERE status BETWEEN 200 AND 299), 0),
		       COALESCE(SUM(hits) FILTER (WHERE status BETWEEN 300 AND 399), 0),
		       COALESCE(SUM(hits) FILTER (WHERE status BETWEEN 400 AND 499), 0),
		       COALESCE(SUM(hits) FILTER (WHERE status BETWEEN 500 AND 599), 0),
		       COALESCE(SUM(hits) FILTER (WHERE status BETWEEN 400 AND 499 AND purpose = ANY($3)), 0),
		       COALESCE(SUM(hits) FILTER (WHERE status BETWEEN 500 AND 599 AND purpose = ANY($3)), 0),
		       COALESCE(SUM(verified_hits) FILTER (WHERE purpose = ANY($3)), 0),
		       COALESCE(SUM(spoofed_hits) FILTER (WHERE purpose = ANY($3)), 0),
		       COALESCE(SUM(unverifiable_hits) FILTER (WHERE purpose = ANY($3)), 0)
		FROM hatamoto_agent_daily
		WHERE day BETWEEN $1 AND $2
		GROUP BY day ORDER BY day`, rg.From, rg.To, aiPurposes)
	if err != nil {
		return nil, fmt.Errorf("health series: %w", err)
	}
	defer rows.Close()
	out := []HealthDay{}
	for rows.Next() {
		var d time.Time
		var h HealthDay
		if err := rows.Scan(&d, &h.Hits, &h.S2xx, &h.S3xx, &h.S4xx, &h.S5xx, &h.AI4xx, &h.AI5xx, &h.Verified, &h.Spoofed, &h.Unverified); err != nil {
			return nil, err
		}
		h.Day = d.Format(dayFmt)
		out = append(out, h)
	}
	return out, rows.Err()
}
