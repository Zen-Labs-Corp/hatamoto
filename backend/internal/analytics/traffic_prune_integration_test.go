//go:build integration

package analytics

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestPruneTraffic(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	day := func(back int) time.Time { return time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC).AddDate(0, 0, -back) }

	var rows []TrafficRow
	for i := 0; i < 150; i++ { // keys 0..149, key i has i+1 visits: the top 100 are 50..149
		rows = append(rows, TrafficRow{Day: day(5), Dim: DimLanding, K1: fmt.Sprintf("/p%03d", i), Counts: Counts{Visits: int64(i + 1), Pageviews: 1}})
	}
	rows = append(rows,
		TrafficRow{Day: day(5), Dim: DimLanding, K1: KeyOther, Counts: Counts{Visits: 7}}, // an earlier (other) merges with the fold
		TrafficRow{Day: day(5), Dim: DimChannel, K1: ChannelDirect, Counts: Counts{Visits: 1000}},
		TrafficRow{Day: day(5), Dim: DimDevice, K1: "mobile", Counts: Counts{Visits: 1}},
		TrafficRow{Day: day(5), Dim: DimCountry, K1: "DE", Counts: Counts{Visits: 1}},
		TrafficRow{Day: day(1), Dim: DimLanding, K1: "/yesterday", Counts: Counts{Visits: 1}}, // too recent to compact
		TrafficRow{Day: day(0), Dim: DimSource, K1: "today", K2: ChannelDirect, Counts: Counts{Visits: 1}},
		TrafficRow{Day: day(430), Dim: DimSource, K1: "old", K2: ChannelDirect, Counts: Counts{Visits: 1}}, // > 13 months
		TrafficRow{Day: day(430), Dim: DimChannel, K1: ChannelDirect, Counts: Counts{Visits: 5}},           // kept forever
		TrafficRow{Day: day(430), Dim: DimCountry, K1: "FR", Counts: Counts{Visits: 5}},                    // kept forever
	)
	conn, err := db.GetConn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	repo := NewPostgresRepository(db)
	if err := NewPostgresTrafficRepository(db).AddTraffic(ctx, rows); err != nil {
		t.Fatal(err)
	}
	var wantVisits int64
	for i := 0; i < 150; i++ {
		wantVisits += int64(i + 1)
	}
	wantVisits += 7

	for run := 0; run < 2; run++ { // the second run must change nothing
		if err := repo.PruneTraffic(ctx, now); err != nil {
			t.Fatal(err)
		}
		count := func(where string, args ...any) (n int, visits int64) {
			err := conn.QueryRow(ctx, `SELECT count(*), COALESCE(sum(visits), 0)::bigint FROM hatamoto_traffic_daily WHERE `+where, args...).Scan(&n, &visits)
			if err != nil {
				t.Fatal(err)
			}
			return
		}
		if n, v := count(`dim = 'landing' AND day = $1`, day(5)); n != 101 || v != wantVisits {
			t.Fatalf("run %d: day(5) landing rows=%d visits=%d, want 101 / %d", run, n, v, wantVisits)
		}
		var other, pv int64
		if err := conn.QueryRow(ctx, `SELECT visits, pageviews FROM hatamoto_traffic_daily WHERE dim='landing' AND day=$1 AND k1=$2`, day(5), KeyOther).Scan(&other, &pv); err != nil {
			t.Fatal(err)
		}
		// 50 folded keys (visits 1..50, one pageview each) plus the 7 already in (other).
		if other != 50*51/2+7 || pv != 50 {
			t.Fatalf("(other) = %d visits, %d pageviews", other, pv)
		}
		if n, _ := count(`dim = 'landing' AND day = $1`, day(1)); n != 1 {
			t.Fatalf("yesterday compacted: %d rows", n)
		}
		if n, _ := count(`dim = 'source' AND k1 = 'old'`); n != 0 {
			t.Fatal("14-month-old source row kept")
		}
		if n, _ := count(`dim = 'source' AND k1 = 'today'`); n != 1 {
			t.Fatal("recent source row removed")
		}
		if n, v := count(`dim IN ('channel','device','country')`); n != 5 || v != 1000+1+1+5+5 {
			t.Fatalf("coarse dims touched: rows=%d visits=%d", n, v)
		}
	}
}
