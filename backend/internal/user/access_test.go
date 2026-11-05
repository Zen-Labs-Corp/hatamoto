package user

import (
	"encoding/json"
	"testing"
	"time"
)

func TestAccessEndHelpers(t *testing.T) {
	cases := []struct {
		zone, date string
		wantUTC    string
	}{
		{"UTC", "2026-12-31", "2027-01-01T00:00:00Z"},
		{"Europe/Berlin", "2026-10-24", "2026-10-24T22:00:00Z"}, // CEST until the night
		{"Europe/Berlin", "2026-10-25", "2026-10-25T23:00:00Z"}, // clock change day: CET after
		{"America/Los_Angeles", "2026-11-01", "2026-11-02T08:00:00Z"},
		{"America/Los_Angeles", "2026-10-31", "2026-11-01T07:00:00Z"},
		{"Asia/Tokyo", "2026-02-28", "2026-02-28T15:00:00Z"},
	}
	for _, c := range cases {
		loc, err := time.LoadLocation(c.zone)
		if err != nil {
			t.Fatal(err)
		}
		end, err := AccessEndFromDate(c.date, loc)
		if err != nil {
			t.Fatal(err)
		}
		if got := end.Format(time.RFC3339); got != c.wantUTC || end.Location() != time.UTC {
			t.Errorf("%s %s: got %s, want %s", c.zone, c.date, got, c.wantUTC)
		}
		if day := AccessEndDay(end, loc); day != c.date {
			t.Errorf("%s %s: round trip gave %s", c.zone, c.date, day)
		}
	}
	if _, err := AccessEndFromDate("31/12/2026", time.UTC); err == nil {
		t.Error("bad format accepted")
	}
}

func TestValidateAccessEnd(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	if ValidateAccessEnd(now.Add(time.Hour), now) != nil {
		t.Error("future refused")
	}
	if ValidateAccessEnd(now, now) != ErrAccessEndPast || ValidateAccessEnd(now.Add(-time.Hour), now) != ErrAccessEndPast {
		t.Error("past accepted")
	}
}

func TestAccessEnded(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	past, future := now.Add(-time.Second), now.Add(time.Second)
	if (&User{}).AccessEnded(now) || (&User{AccessEndsAt: &future}).AccessEnded(now) {
		t.Error("open user ended")
	}
	if !(&User{AccessEndsAt: &past}).AccessEnded(now) || !(&User{AccessEndsAt: &now}).AccessEnded(now) {
		t.Error("ended user open")
	}
}

func TestUpdateInputAccessEndJSON(t *testing.T) {
	var in UpdateUserInput
	if err := json.Unmarshal([]byte(`{"name":"x"}`), &in); err != nil || in.AccessEndsAt.Set {
		t.Fatalf("absent: %+v %v", in, err)
	}
	in = UpdateUserInput{}
	if err := json.Unmarshal([]byte(`{"access_ends_at":null}`), &in); err != nil || !in.AccessEndsAt.Set || in.AccessEndsAt.At != nil {
		t.Fatalf("null: %+v %v", in, err)
	}
	in = UpdateUserInput{}
	if err := json.Unmarshal([]byte(`{"access_ends_at":"2026-12-01T10:00:00Z"}`), &in); err != nil || !in.AccessEndsAt.Set || in.AccessEndsAt.At == nil || in.AccessEndsAt.At.Hour() != 10 {
		t.Fatalf("string: %+v %v", in, err)
	}
	if err := json.Unmarshal([]byte(`{"access_ends_at":"2026-12-01"}`), &UpdateUserInput{}); err == nil {
		t.Fatal("zone-less date accepted")
	}
}
