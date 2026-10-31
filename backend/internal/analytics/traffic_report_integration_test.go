//go:build integration

package analytics

import (
	"context"
	"testing"
	"time"
)

func TestTrafficReader(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	day := func(d int) time.Time { return time.Date(2026, 10, d, 0, 0, 0, 0, time.UTC) }
	rows := []TrafficRow{
		{Day: day(1), Dim: DimChannel, K1: ChannelEmail, Counts: Counts{Visits: 3, Pageviews: 5, Engaged: 1, Multi: 1}},
		{Day: day(2), Dim: DimChannel, K1: ChannelEmail, Counts: Counts{Visits: 1, Pageviews: 1, Scrolled: 1}},
		{Day: day(2), Dim: DimChannel, K1: ChannelDirect, Counts: Counts{Visits: 2, Pageviews: 2}},
		{Day: day(9), Dim: DimChannel, K1: ChannelDirect, Counts: Counts{Visits: 50, Pageviews: 50}}, // outside the range
		{Day: day(1), Dim: DimSource, K1: "news", K2: ChannelEmail, Counts: Counts{Visits: 3, Pageviews: 5}},
		{Day: day(2), Dim: DimSource, K1: "news", K2: ChannelEmail, Counts: Counts{Visits: 1, Pageviews: 1}},
		{Day: day(2), Dim: DimSource, K1: KeyOther, Counts: Counts{Visits: 99, Pageviews: 99}},
		{Day: day(2), Dim: DimSource, K1: "(none)", K2: ChannelDirect, Counts: Counts{Visits: 2, Pageviews: 2}},
	}
	if err := NewPostgresTrafficRepository(db).AddTraffic(ctx, rows); err != nil {
		t.Fatal(err)
	}
	// Upserting again adds to the same rows.
	if err := NewPostgresTrafficRepository(db).AddTraffic(ctx, rows[:1]); err != nil {
		t.Fatal(err)
	}
	rd := NewReader(db)
	from, to := day(1), day(3)

	tot, err := rd.TrafficTotals(ctx, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if want := (Counts{Visits: 9, Pageviews: 13, Engaged: 2, Multi: 2, Scrolled: 1}); tot != want {
		t.Errorf("totals = %+v, want %+v", tot, want)
	}
	if empty, err := rd.TrafficTotals(ctx, day(20), day(21)); err != nil || empty != (Counts{}) {
		t.Errorf("empty totals = %+v, %v", empty, err)
	}

	daily, err := rd.TrafficDaily(ctx, DimChannel, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if len(daily) != 3 || daily[0].Day != "2026-10-01" || daily[0].K1 != ChannelEmail || daily[0].Visits != 6 {
		t.Errorf("daily = %+v", daily)
	}

	top, err := rd.TrafficTop(ctx, DimSource, from, to, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(top) != 3 || top[0].K1 != "news" || top[0].K2 != ChannelEmail || top[0].Visits != 4 || top[1].K1 != "(none)" || top[2].K1 != KeyOther {
		t.Errorf("top = %+v ((other) must be last)", top)
	}
	if limited, _ := rd.TrafficTop(ctx, DimSource, from, to, 1); len(limited) != 1 || limited[0].K1 != "news" {
		t.Errorf("limited = %+v", limited)
	}
	if _, err := rd.TrafficTop(ctx, Dim("bogus"), from, to, 1); err == nil {
		t.Error("unknown dim accepted")
	}
}
