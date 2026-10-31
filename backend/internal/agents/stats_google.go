package agents

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Googlebot views. Everything here reads the daily rollup (no IP) and the
// search first-crawl table. Page-level numbers (pages crawled, crawls, last
// crawl, last status, stale) leave out spoofed hits: a forged user agent must
// not make a page look crawled. Only the hit totals and the status chart count
// every hit.

const googlebotID = "googlebot"

// googleNotFilePath excludes the crawler-facing files that are not pages (see IsFilePath).
const googleNotFilePath = `d.path NOT IN ('/robots.txt', '/llms.txt', '/llms-full.txt', '/sitemap.xml')
	AND d.path NOT LIKE '/sitemap-%.xml'`

// aiAndSearchPurposes are the purposes shown next to the AI ones in the hero chart.
var aiAndSearchPurposes = append(append([]string{}, aiPurposes...), string(PurposeSearch))

// SeriesWithSearch is Series over the AI purposes plus the search engines.
// Search engine hits come back under the key "search" (group "purpose") or
// under their operator (group "operator").
func (r *StatsRepository) SeriesWithSearch(ctx context.Context, rg Range, groupBy string) ([]SeriesPoint, error) {
	return r.series(ctx, rg, groupBy, aiAndSearchPurposes)
}

// GooglebotStatusSeries returns Googlebot hits per day and response class
// (keys "2xx", "3xx", "4xx", "5xx"; unknown status is left out), all hits
// included, spoofed or not, like the site-wide health chart.
func (r *StatsRepository) GooglebotStatusSeries(ctx context.Context, rg Range) ([]SeriesPoint, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, fmt.Errorf("get connection: %w", err)
	}
	defer conn.Release()
	rows, err := conn.Query(ctx, `
		SELECT day, (status / 100)::text || 'xx' AS k, SUM(hits)
		FROM hatamoto_agent_daily
		WHERE day BETWEEN $1 AND $2 AND agent_id = $3 AND status BETWEEN 200 AND 599
		GROUP BY day, k ORDER BY day, k`, rg.From, rg.To, googlebotID)
	if err != nil {
		return nil, fmt.Errorf("googlebot status series: %w", err)
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

// GoogleKPIs are the headline numbers of the Google view.
type GoogleKPIs struct {
	Hits     int64 // every Googlebot hit in the range
	Verified int64 // of those, verified
	// PagesCrawled of Published: published pages with a served, non-spoofed
	// Googlebot fetch in the range.
	PagesCrawled int
	Published    int
	// MedianFirstCrawl is the median publish -> first Googlebot crawl of pages
	// published since tracking began and first crawled in the range. Nil when
	// there is none.
	MedianFirstCrawl *time.Duration
}

// GoogleKPIs computes the headline numbers for the range.
func (r *StatsRepository) GoogleKPIs(ctx context.Context, rg Range) (GoogleKPIs, error) {
	var k GoogleKPIs
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return k, fmt.Errorf("get connection: %w", err)
	}
	defer conn.Release()
	if err := conn.QueryRow(ctx, `
		SELECT COALESCE(SUM(hits), 0), COALESCE(SUM(verified_hits), 0)
		FROM hatamoto_agent_daily
		WHERE day BETWEEN $1 AND $2 AND agent_id = $3`, rg.From, rg.To, googlebotID).Scan(&k.Hits, &k.Verified); err != nil {
		return k, fmt.Errorf("google totals: %w", err)
	}
	if err := conn.QueryRow(ctx, `
		SELECT COUNT(*),
		       COUNT(*) FILTER (WHERE EXISTS (
		           SELECT 1 FROM hatamoto_agent_daily d
		           WHERE d.entity_id = e.id AND d.agent_id = $3 AND d.day BETWEEN $1 AND $2
		             AND d.status > 0 AND d.status < 400 AND d.hits - d.spoofed_hits > 0))
		FROM hatamoto_entities e WHERE `+publishedFilter, rg.From, rg.To, googlebotID).Scan(&k.Published, &k.PagesCrawled); err != nil {
		return k, fmt.Errorf("google pages crawled: %w", err)
	}
	var secs *float64
	if err := conn.QueryRow(ctx, `
		SELECT percentile_cont(0.5) WITHIN GROUP (ORDER BY EXTRACT(EPOCH FROM f.first_at - f.published_at))
		FROM hatamoto_agent_search_first_crawl f, hatamoto_agent_tracking t
		WHERE f.agent_id = $3 AND f.published_at IS NOT NULL AND f.published_at >= t.started_at
		  AND f.first_at >= f.published_at AND f.first_at >= $1::date AND f.first_at < $2::date + 1`,
		rg.From, rg.To, googlebotID).Scan(&secs); err != nil {
		return k, fmt.Errorf("google median first crawl: %w", err)
	}
	if secs != nil {
		d := time.Duration(*secs * float64(time.Second))
		k.MedianFirstCrawl = &d
	}
	return k, nil
}

// Google page table filters.
const (
	GoogleFilterAll        = "all"
	GoogleFilterNotCrawled = "not-crawled"
	GoogleFilterErrors     = "errors"
	GoogleFilterStale      = "stale"
)

// GooglePageRow is one row of "pages through Google's eyes": a published page
// or a path Googlebot fetched that no published page serves (an old URL, a
// missing page).
type GooglePageRow struct {
	// Path is the most fetched path of the page; empty for a published page
	// Googlebot never fetched in the range.
	Path     string
	EntityID *uuid.UUID // nil for a path no published page serves
	Name     string     // the page title; empty for a bare path
	Crawls   int64      // non-spoofed fetches in the range
	// LastDay is the UTC day of the last non-spoofed fetch; nil when none.
	LastDay *time.Time
	// LastStatus is the highest status of that last day; 0 when never crawled.
	LastStatus int
	// Stale: the page was edited on a later UTC day than the last crawl.
	Stale bool
}

// googlePagesFrom builds the row source. $1 from, $2 to, $3 agent. Published
// pages come first (also the never crawled ones), then the fetched paths that
// no published page serves.
const googlePagesFrom = `
WITH g AS (
    SELECT d.entity_id, d.path, d.day, d.status, d.hits - d.spoofed_hits AS n
    FROM hatamoto_agent_daily d
    WHERE d.agent_id = $3 AND d.day BETWEEN $1 AND $2 AND d.hits - d.spoofed_hits > 0 AND ` + googleNotFilePath + `
), ga AS (
    SELECT g.* FROM g JOIN hatamoto_entities e ON e.id = g.entity_id AND ` + publishedFilter + `
), gb AS (
    SELECT g.* FROM g WHERE NOT EXISTS (SELECT 1 FROM ga WHERE ga.entity_id = g.entity_id)
), a_last AS (
    SELECT entity_id, MAX(day) AS last_day FROM ga GROUP BY entity_id
), b_last AS (
    SELECT path, MAX(day) AS last_day FROM gb GROUP BY path
), r AS (
    SELECT e.id AS entity_id, COALESCE(s.path, '') AS path, e.name,
           COALESCE(s.crawls, 0) AS crawls, l.last_day, COALESCE(ls.last_status, 0) AS last_status,
           l.last_day IS NOT NULL AND (e.updated_at AT TIME ZONE 'UTC')::date > l.last_day AS stale
    FROM hatamoto_entities e
    LEFT JOIN (SELECT entity_id, SUM(n)::bigint AS crawls, (array_agg(path ORDER BY n DESC, path))[1] AS path
               FROM ga GROUP BY entity_id) s ON s.entity_id = e.id
    LEFT JOIN a_last l ON l.entity_id = e.id
    LEFT JOIN (SELECT ga.entity_id, MAX(ga.status) AS last_status
               FROM ga JOIN a_last l ON l.entity_id = ga.entity_id AND l.last_day = ga.day
               GROUP BY ga.entity_id) ls ON ls.entity_id = e.id
    WHERE ` + publishedFilter + `
    UNION ALL
    SELECT NULL::uuid, s.path, '', s.crawls, l.last_day, ls.last_status, false
    FROM (SELECT path, SUM(n)::bigint AS crawls FROM gb GROUP BY path) s
    JOIN b_last l ON l.path = s.path
    JOIN (SELECT gb.path, MAX(gb.status) AS last_status
          FROM gb JOIN b_last l ON l.path = gb.path AND l.last_day = gb.day
          GROUP BY gb.path) ls ON ls.path = s.path
)`

func googleFilterSQL(filter string) (string, error) {
	switch filter {
	case "", GoogleFilterAll:
		return "TRUE", nil
	case GoogleFilterNotCrawled:
		return "entity_id IS NOT NULL AND crawls = 0", nil
	case GoogleFilterErrors:
		return "last_status >= 400", nil
	case GoogleFilterStale:
		return "stale", nil
	}
	return "", fmt.Errorf("invalid google filter %q", filter)
}

// GooglePages lists the rows of the Google page table for the range, most
// crawled first, and the total number of rows matching the filter.
func (r *StatsRepository) GooglePages(ctx context.Context, rg Range, filter string, limit, offset int) ([]GooglePageRow, int, error) {
	where, err := googleFilterSQL(filter)
	if err != nil {
		return nil, 0, err
	}
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("get connection: %w", err)
	}
	defer conn.Release()
	var total int
	if err := conn.QueryRow(ctx, googlePagesFrom+` SELECT COUNT(*) FROM r WHERE `+where,
		rg.From, rg.To, googlebotID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("google pages count: %w", err)
	}
	rows, err := conn.Query(ctx, googlePagesFrom+`
		SELECT entity_id, path, name, crawls, last_day, last_status, stale FROM r WHERE `+where+`
		ORDER BY crawls DESC, COALESCE(NULLIF(name, ''), path), path, entity_id
		LIMIT $4 OFFSET $5`, rg.From, rg.To, googlebotID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("google pages: %w", err)
	}
	defer rows.Close()
	out := []GooglePageRow{}
	for rows.Next() {
		var p GooglePageRow
		if err := rows.Scan(&p.EntityID, &p.Path, &p.Name, &p.Crawls, &p.LastDay, &p.LastStatus, &p.Stale); err != nil {
			return nil, 0, err
		}
		out = append(out, p)
	}
	return out, total, rows.Err()
}

// SearchFirstCrawl returns when a search engine first crawled the page (the
// earliest verified one), or nil when none did.
func (r *StatsRepository) SearchFirstCrawl(ctx context.Context, entityID uuid.UUID) (*time.Time, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, fmt.Errorf("get connection: %w", err)
	}
	defer conn.Release()
	var at *time.Time
	if err := conn.QueryRow(ctx, `SELECT MIN(first_at) FROM hatamoto_agent_search_first_crawl WHERE entity_id = $1`, entityID).Scan(&at); err != nil {
		return nil, fmt.Errorf("search first crawl: %w", err)
	}
	return at, nil
}
