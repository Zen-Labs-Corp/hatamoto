package admin

import (
	"testing"
	"time"
)

func TestFmtDates(t *testing.T) {
	t.Parallel()

	ts := time.Date(2024, time.March, 5, 7, 9, 0, 0, time.UTC)
	for _, c := range []struct {
		name, got, want string
	}{
		{"date", fmtDate(ts), "5 Mar 2024"},
		{"datetime", fmtDateTime(ts), "5 Mar 2024 07:09"},
		{"time", fmtTime(ts), "07:09"},
	} {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}
}
