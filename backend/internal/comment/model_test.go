package comment

import (
	"errors"
	"testing"
	"time"
)

func TestDefaults(t *testing.T) {
	t.Parallel()
	d := Defaults()
	want := Settings{
		Thresholds:    Thresholds{KnownApproved: 3, KnownDays: 7, TrustedApproved: 15, TrustedDays: 30},
		AutoCloseDays: 30, MaxDepth: 5, PageSize: 50, Sort: SortNewest,
		SlowModeSeconds: 120, DeviceCookieDays: 90, PurgeDays: 30, NotifyMode: NotifyPerComment,
	}
	if d != want {
		t.Errorf("Defaults() = %+v, want %+v", d, want)
	}
	if d.Enabled {
		t.Error("comments must be off by default")
	}
	if err := d.Validate(); err != nil {
		t.Errorf("Defaults().Validate() = %v", err)
	}
}

func TestADRNumbers(t *testing.T) {
	t.Parallel()
	if EditWindow != 15*time.Minute {
		t.Errorf("EditWindow = %v", EditWindow)
	}
	if DraftTTL != 24*time.Hour {
		t.Errorf("DraftTTL = %v", DraftTTL)
	}
}

func TestNormalize(t *testing.T) {
	t.Parallel()
	s := Defaults()
	s.MaxDepth, s.PageSize, s.Sort = 0, 10000, "bogus"
	s.Thresholds.KnownApproved = 0
	n := s.Normalize()
	if n.MaxDepth != 5 || n.PageSize != 50 || n.Sort != SortNewest || n.Thresholds.KnownApproved != 3 {
		t.Errorf("Normalize() = %+v", n)
	}
	if err := n.Validate(); err != nil {
		t.Errorf("normalized settings invalid: %v", err)
	}
	var fe *FieldError
	if err := s.Validate(); !errors.As(err, &fe) || fe.Field != "max_depth" {
		t.Errorf("Validate() = %v, want max_depth field error", err)
	}
}

func TestNotifyMode(t *testing.T) {
	t.Parallel()
	if got := Defaults().NotifyMode; got != NotifyPerComment {
		t.Errorf("Defaults().NotifyMode = %q, want per_comment", got)
	}
	s := Defaults()
	s.NotifyMode = "weird"
	if got := s.Normalize().NotifyMode; got != NotifyPerComment {
		t.Errorf("Normalize() NotifyMode = %q, want per_comment", got)
	}
	s.NotifyMode = ""
	if got := s.Normalize().NotifyMode; got != NotifyPerComment {
		t.Errorf("Normalize() of empty NotifyMode = %q, want per_comment", got)
	}
	s.NotifyMode = "weird"
	var fe *FieldError
	if err := s.Validate(); !errors.As(err, &fe) || fe.Field != "notify_mode" {
		t.Errorf("Validate() = %v, want notify_mode field error", err)
	}
	for _, m := range []NotifyMode{NotifyPerComment, NotifyDigest, NotifyOff} {
		s.NotifyMode = m
		if err := s.Validate(); err != nil {
			t.Errorf("Validate() with %q = %v", m, err)
		}
	}
}

func TestCanTransition(t *testing.T) {
	t.Parallel()
	cases := []struct {
		from, to Status
		want     bool
	}{
		{StatusDraft, StatusPending, true},
		{StatusDraft, StatusApproved, true},
		{StatusDraft, StatusSpam, true},
		{StatusPending, StatusApproved, true},
		{StatusPending, StatusDraft, false},
		{StatusApproved, StatusPending, true},
		{StatusApproved, StatusDraft, false},
		{StatusSpam, StatusApproved, true},
		{StatusTrash, StatusPending, true},
		{StatusApproved, StatusApproved, false},
		{StatusTrash, StatusTrash, false},
		{StatusApproved, StatusDead, true},
		{StatusPending, StatusDead, true},
		{StatusDead, StatusApproved, true},
		{StatusDead, StatusTrash, true},
		{StatusDead, StatusSpam, false},
		{StatusDead, StatusPending, false},
		{StatusSpam, StatusDead, false},
		{StatusTrash, StatusDead, false},
		{StatusDraft, StatusDead, false},
		{"bogus", StatusApproved, false},
	}
	for _, c := range cases {
		if got := CanTransition(c.from, c.to); got != c.want {
			t.Errorf("CanTransition(%s, %s) = %v, want %v", c.from, c.to, got, c.want)
		}
	}
}
