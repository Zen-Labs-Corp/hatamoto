package analytics

import (
	"context"
	"fmt"
	"time"
)

func validDim(d Dim) bool {
	switch d {
	case DimChannel, DimSource, DimCampaign, DimLanding, DimDevice, DimCountry:
		return true
	}
	return false
}

// TrafficDaily returns the counters per day and key (k1) of one dimension from..to
// (inclusive, UTC dates), oldest day first. Days without a row are absent.
func (r *Reader) TrafficDaily(ctx context.Context, dim Dim, from, to time.Time) ([]TrafficDay, error) {
	if !validDim(dim) {
		return nil, fmt.Errorf("traffic daily: unknown dimension %q", dim)
	}
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, fmt.Errorf("get connection: %w", err)
	}
	defer conn.Release()
	rows, err := conn.Query(ctx, `SELECT day, k1,
			SUM(visits)::bigint, SUM(pageviews)::bigint, SUM(engaged)::bigint, SUM(multi)::bigint, SUM(scrolled)::bigint,
			SUM(form_seen)::bigint, SUM(form_started)::bigint
		FROM hatamoto_traffic_daily
		WHERE dim = $1 AND day BETWEEN $2 AND $3
		GROUP BY day, k1 ORDER BY day, k1`, string(dim), from, to)
	if err != nil {
		return nil, fmt.Errorf("traffic daily: %w", err)
	}
	defer rows.Close()
	out := []TrafficDay{}
	for rows.Next() {
		var (
			d  time.Time
			td TrafficDay
		)
		if err := rows.Scan(&d, &td.K1, &td.Visits, &td.Pageviews, &td.Engaged, &td.Multi, &td.Scrolled, &td.FormSeen, &td.FormStarted); err != nil {
			return nil, err
		}
		td.Day = d.Format("2006-01-02")
		out = append(out, td)
	}
	return out, rows.Err()
}

// TrafficTop returns the counters of one dimension summed over from..to,
// busiest key first (visits plus pageviews); the "(other)" bucket is last.
// limit <= 0 means no limit.
func (r *Reader) TrafficTop(ctx context.Context, dim Dim, from, to time.Time, limit int) ([]TrafficTop, error) {
	if !validDim(dim) {
		return nil, fmt.Errorf("traffic top: unknown dimension %q", dim)
	}
	if limit <= 0 {
		limit = 1 << 30
	}
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, fmt.Errorf("get connection: %w", err)
	}
	defer conn.Release()
	rows, err := conn.Query(ctx, `SELECT k1, k2,
			SUM(visits)::bigint, SUM(pageviews)::bigint, SUM(engaged)::bigint, SUM(multi)::bigint, SUM(scrolled)::bigint,
			SUM(form_seen)::bigint, SUM(form_started)::bigint
		FROM hatamoto_traffic_daily
		WHERE dim = $1 AND day BETWEEN $2 AND $3
		GROUP BY k1, k2
		ORDER BY (k1 = $4), SUM(visits) + SUM(pageviews) DESC, k1, k2
		LIMIT $5`, string(dim), from, to, KeyOther, limit)
	if err != nil {
		return nil, fmt.Errorf("traffic top: %w", err)
	}
	defer rows.Close()
	out := []TrafficTop{}
	for rows.Next() {
		var tt TrafficTop
		if err := rows.Scan(&tt.K1, &tt.K2, &tt.Visits, &tt.Pageviews, &tt.Engaged, &tt.Multi, &tt.Scrolled, &tt.FormSeen, &tt.FormStarted); err != nil {
			return nil, err
		}
		out = append(out, tt)
	}
	return out, rows.Err()
}

// TrafficTotals returns the counters of from..to summed over the channel
// dimension, which every event feeds exactly once, so the totals are exact.
func (r *Reader) TrafficTotals(ctx context.Context, from, to time.Time) (Counts, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return Counts{}, fmt.Errorf("get connection: %w", err)
	}
	defer conn.Release()
	var c Counts
	err = conn.QueryRow(ctx, `SELECT COALESCE(SUM(visits), 0)::bigint, COALESCE(SUM(pageviews), 0)::bigint,
			COALESCE(SUM(engaged), 0)::bigint, COALESCE(SUM(multi), 0)::bigint, COALESCE(SUM(scrolled), 0)::bigint,
			COALESCE(SUM(form_seen), 0)::bigint, COALESCE(SUM(form_started), 0)::bigint
		FROM hatamoto_traffic_daily WHERE dim = $1 AND day BETWEEN $2 AND $3`,
		string(DimChannel), from, to).Scan(&c.Visits, &c.Pageviews, &c.Engaged, &c.Multi, &c.Scrolled, &c.FormSeen, &c.FormStarted)
	if err != nil {
		return Counts{}, fmt.Errorf("traffic totals: %w", err)
	}
	return c, nil
}
