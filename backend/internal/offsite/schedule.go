package offsite

import (
	"fmt"
	"time"
)

// Schedule kinds.
const (
	ScheduleOff    = "off"
	ScheduleDaily  = "daily"
	ScheduleWeekly = "weekly"
)

// Schedule says when the scheduled backup runs. All times are UTC, on the hour.
type Schedule struct {
	Kind    string // off, daily or weekly
	Hour    int    // 0-23
	Weekday int    // 0 (Sunday) to 6, weekly only
}

// Validate checks the schedule fields.
func (s Schedule) Validate() error {
	switch s.Kind {
	case ScheduleOff, ScheduleDaily, ScheduleWeekly:
	default:
		return fmt.Errorf("unknown schedule %q", s.Kind)
	}
	if s.Hour < 0 || s.Hour > 23 {
		return fmt.Errorf("hour must be between 0 and 23")
	}
	if s.Weekday < 0 || s.Weekday > 6 {
		return fmt.Errorf("weekday must be between 0 and 6")
	}
	return nil
}

// Period is the time between two runs, zero when the schedule is off.
func (s Schedule) Period() time.Duration {
	switch s.Kind {
	case ScheduleDaily:
		return 24 * time.Hour
	case ScheduleWeekly:
		return 7 * 24 * time.Hour
	}
	return 0
}

// Next returns the first run strictly after the given time, or the zero time
// when the schedule is off.
func (s Schedule) Next(after time.Time) time.Time {
	after = after.UTC()
	day := time.Date(after.Year(), after.Month(), after.Day(), s.Hour, 0, 0, 0, time.UTC)
	switch s.Kind {
	case ScheduleDaily:
		if !day.After(after) {
			day = day.AddDate(0, 0, 1)
		}
		return day
	case ScheduleWeekly:
		day = day.AddDate(0, 0, (s.Weekday-int(day.Weekday())+7)%7)
		if !day.After(after) {
			day = day.AddDate(0, 0, 7)
		}
		return day
	}
	return time.Time{}
}
