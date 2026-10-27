package analytics

import (
	"context"
	"fmt"
	"time"

	"hatamoto.dev/core/internal/database"
)

// ReferralDay is the visits one AI assistant sent on one day.
type ReferralDay struct {
	Day       string `json:"day"`
	Assistant string `json:"assistant"`
	Visits    int64  `json:"visits"`
}

// Reader reads the daily rollup.
type Reader struct{ db *database.DB }

// NewReader creates the Postgres-backed rollup reader.
func NewReader(db *database.DB) *Reader { return &Reader{db: db} }

// AIReferrals returns visits per assistant per day from..to (inclusive, UTC
// dates), oldest day first. The rollup is per assistant and day only: it
// keeps no page path, by design (no visitor profiles).
func (r *Reader) AIReferrals(ctx context.Context, from, to time.Time) ([]ReferralDay, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, fmt.Errorf("get connection: %w", err)
	}
	defer conn.Release()
	rows, err := conn.Query(ctx, `SELECT day, key, count FROM hatamoto_analytics_daily
		WHERE kind = $1 AND day BETWEEN $2 AND $3 ORDER BY day, key`, KindAIReferral, from, to)
	if err != nil {
		return nil, fmt.Errorf("ai referrals: %w", err)
	}
	defer rows.Close()
	out := []ReferralDay{}
	for rows.Next() {
		var (
			d  time.Time
			rd ReferralDay
		)
		if err := rows.Scan(&d, &rd.Assistant, &rd.Visits); err != nil {
			return nil, err
		}
		rd.Day = d.Format("2006-01-02")
		out = append(out, rd)
	}
	return out, rows.Err()
}

// DayCount is the pageviews of one day.
type DayCount struct {
	Day   string `json:"day"`
	Views int64  `json:"views"`
}

// Pageviews returns the pageviews per day from..to (inclusive, UTC dates),
// oldest day first. Days without a row are absent.
func (r *Reader) Pageviews(ctx context.Context, from, to time.Time) ([]DayCount, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, fmt.Errorf("get connection: %w", err)
	}
	defer conn.Release()
	rows, err := conn.Query(ctx, `SELECT day, SUM(count) FROM hatamoto_analytics_daily
		WHERE kind = $1 AND day BETWEEN $2 AND $3 GROUP BY day ORDER BY day`, KindPageView, from, to)
	if err != nil {
		return nil, fmt.Errorf("pageviews: %w", err)
	}
	defer rows.Close()
	out := []DayCount{}
	for rows.Next() {
		var (
			d time.Time
			c DayCount
		)
		if err := rows.Scan(&d, &c.Views); err != nil {
			return nil, err
		}
		c.Day = d.Format("2006-01-02")
		out = append(out, c)
	}
	return out, rows.Err()
}
