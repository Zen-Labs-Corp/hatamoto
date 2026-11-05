package offsite

import (
	"testing"
	"time"
)

func at(y int, m time.Month, d, h, mi int) time.Time {
	return time.Date(y, m, d, h, mi, 0, 0, time.UTC)
}

func TestScheduleNext(t *testing.T) {
	// 2026-11-04 is a Wednesday (3).
	for _, tc := range []struct {
		name  string
		s     Schedule
		after time.Time
		want  time.Time
	}{
		{"daily later today", Schedule{ScheduleDaily, 3, 0}, at(2026, 11, 4, 1, 30), at(2026, 11, 4, 3, 0)},
		{"daily exactly on the hour is strictly after", Schedule{ScheduleDaily, 3, 0}, at(2026, 11, 4, 3, 0), at(2026, 11, 5, 3, 0)},
		{"daily past the hour", Schedule{ScheduleDaily, 3, 0}, at(2026, 11, 4, 23, 59), at(2026, 11, 5, 3, 0)},
		{"daily month end", Schedule{ScheduleDaily, 0, 0}, at(2026, 11, 30, 0, 0), at(2026, 12, 1, 0, 0)},
		{"weekly same day before hour", Schedule{ScheduleWeekly, 5, 3}, at(2026, 11, 4, 4, 0), at(2026, 11, 4, 5, 0)},
		{"weekly same day after hour", Schedule{ScheduleWeekly, 5, 3}, at(2026, 11, 4, 5, 0), at(2026, 11, 11, 5, 0)},
		{"weekly later this week", Schedule{ScheduleWeekly, 3, 6}, at(2026, 11, 4, 12, 0), at(2026, 11, 7, 3, 0)},
		{"weekly earlier weekday wraps", Schedule{ScheduleWeekly, 3, 1}, at(2026, 11, 4, 12, 0), at(2026, 11, 9, 3, 0)},
		{"weekly sunday", Schedule{ScheduleWeekly, 3, 0}, at(2026, 11, 4, 12, 0), at(2026, 11, 8, 3, 0)},
		{"non-UTC input is read as UTC", Schedule{ScheduleDaily, 3, 0},
			time.Date(2026, 11, 4, 2, 0, 0, 0, time.FixedZone("x", 5*3600)), at(2026, 11, 4, 3, 0)},
		{"off", Schedule{Kind: ScheduleOff}, at(2026, 11, 4, 0, 0), time.Time{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.s.Next(tc.after)
			if !got.Equal(tc.want) {
				t.Fatalf("Next = %v, want %v", got, tc.want)
			}
			if !got.IsZero() && !got.After(tc.after) {
				t.Fatalf("Next %v is not after %v", got, tc.after)
			}
		})
	}
}

func TestScheduleValidateAndPeriod(t *testing.T) {
	for _, s := range []Schedule{{"hourly", 0, 0}, {ScheduleDaily, 24, 0}, {ScheduleDaily, -1, 0}, {ScheduleWeekly, 3, 7}} {
		if s.Validate() == nil {
			t.Errorf("%+v accepted", s)
		}
	}
	if err := (Schedule{ScheduleWeekly, 23, 6}).Validate(); err != nil {
		t.Fatal(err)
	}
	if (Schedule{Kind: ScheduleDaily}).Period() != 24*time.Hour || (Schedule{Kind: ScheduleWeekly}).Period() != 168*time.Hour ||
		(Schedule{Kind: ScheduleOff}).Period() != 0 {
		t.Fatal("period")
	}
}
