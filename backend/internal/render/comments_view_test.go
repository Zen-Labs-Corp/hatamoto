package render

import (
	"testing"
	"time"
)

func TestAgoBoundaries(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	nowFunc = func() time.Time { return now }
	defer func() { nowFunc = time.Now }()
	for _, c := range []struct {
		age  time.Duration
		want Ago
	}{
		{-time.Hour, Ago{Unit: "now"}},
		{59 * time.Second, Ago{Unit: "now"}},
		{time.Minute, Ago{"min", 1}},
		{59 * time.Minute, Ago{"min", 59}},
		{time.Hour, Ago{"hour", 1}},
		{23 * time.Hour, Ago{"hour", 23}},
		{24 * time.Hour, Ago{"day", 1}},
		{30 * 24 * time.Hour, Ago{"day", 30}},
		{31 * 24 * time.Hour, Ago{Unit: "date"}},
	} {
		if got := ago(now.Add(-c.age)); got != c.want {
			t.Errorf("ago(-%s) = %+v, want %+v", c.age, got, c.want)
		}
	}
}

func TestCommentFieldsZeroValueSafe(t *testing.T) {
	var v PageView
	if v.Comments != nil || v.CommentForm != nil || v.CommentsOpen || v.CommentCount != 0 {
		t.Fatal("comment fields must default to off")
	}
}
