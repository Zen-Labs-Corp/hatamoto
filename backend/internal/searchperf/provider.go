// Package searchperf defines the read side of search performance data
// (impressions, clicks, position) that an integration can supply.
package searchperf

import (
	"context"
	"time"
)

// Range is the inclusive day window a query covers.
type Range struct{ From, To time.Time }

// DayPoint is one day of search performance.
type DayPoint struct {
	Day         time.Time
	Impressions int64
	Clicks      int64
}

// Summary is search performance over a Range.
type Summary struct {
	Impressions int64
	Clicks      int64
	CTR         float64
	Position    float64
	Daily       []DayPoint
}

// PageStats is search performance of one page path.
type PageStats struct {
	Impressions int64
	Clicks      int64
	CTR         float64
	Position    float64
	Index       string
	TopQuery    string
}

// Provider is filled by an integration. ok=false means not connected or no data.
type Provider interface {
	Summary(ctx context.Context, rg Range) (s Summary, ok bool, err error)
	Pages(ctx context.Context, rg Range, paths []string) (map[string]PageStats, error)
}
