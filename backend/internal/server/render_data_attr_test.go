package server

import (
	"reflect"
	"testing"
	"time"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/render"
)

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	l, err := time.LoadLocation(name)
	if err != nil {
		t.Skipf("no tzdata for %s: %v", name, err)
	}
	return l
}

func TestUntilLocalMidnight(t *testing.T) {
	berlin, auckland := mustLoc(t, "Europe/Berlin"), mustLoc(t, "Pacific/Auckland")
	for name, tc := range map[string]struct {
		now  time.Time
		loc  *time.Location
		want time.Duration
	}{
		"noon utc":            {time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC), time.UTC, 12 * time.Hour},
		"last instant":        {time.Date(2026, 6, 10, 23, 59, 59, 900_000_000, time.UTC), time.UTC, time.Second},
		"exact midnight":      {time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC), time.UTC, 24 * time.Hour},
		"berlin dst start":    {time.Date(2026, 3, 28, 23, 30, 0, 0, time.UTC), berlin, 22*time.Hour + 30*time.Minute},  // 00:30 CET, a 23 hour day
		"berlin dst end":      {time.Date(2026, 10, 24, 22, 30, 0, 0, time.UTC), berlin, 24*time.Hour + 30*time.Minute}, // 00:30 CEST, a 25 hour day
		"auckland ahead":      {time.Date(2026, 6, 10, 13, 0, 0, 0, time.UTC), auckland, 23 * time.Hour},                // 01:00 on June 11 there
		"auckland near local": {time.Date(2026, 6, 10, 11, 59, 59, 0, time.UTC), auckland, time.Second},                 // 23:59:59 on June 10 there
	} {
		if got := untilLocalMidnight(tc.now, tc.loc); got != tc.want {
			t.Errorf("%s: %v, want %v", name, got, tc.want)
		}
	}
}

func TestAttrConds(t *testing.T) {
	today := time.Date(2026, 6, 11, 1, 0, 0, 0, time.UTC)
	for name, tc := range map[string]struct {
		in       render.AttrFilter
		want     []entity.AttrCond
		relative bool
	}{
		"from_today":   {render.AttrFilter{Attr: "d", Op: "from_today"}, []entity.AttrCond{{Key: "d", Op: entity.CondDateGe, Text: "2026-06-11"}}, true},
		"before_today": {render.AttrFilter{Attr: "d", Op: "before_today"}, []entity.AttrCond{{Key: "d", Op: entity.CondDateLt, Text: "2026-06-11"}}, true},
		"next_days 0":  {render.AttrFilter{Attr: "d", Op: "next_days", Days: 0}, []entity.AttrCond{{Key: "d", Op: entity.CondDateGe, Text: "2026-06-11"}, {Key: "d", Op: entity.CondDateLe, Text: "2026-06-11"}}, true},
		"next_days 30": {render.AttrFilter{Attr: "d", Op: "next_days", Days: 30}, []entity.AttrCond{{Key: "d", Op: entity.CondDateGe, Text: "2026-06-11"}, {Key: "d", Op: entity.CondDateLe, Text: "2026-07-11"}}, true},
		"eq text":      {render.AttrFilter{Attr: "s", Op: "eq", Text: "open"}, []entity.AttrCond{{Key: "s", Op: entity.CondEq, Text: "open"}}, false},
		"ne text":      {render.AttrFilter{Attr: "s", Op: "ne", Text: "cancelled"}, []entity.AttrCond{{Key: "s", Op: entity.CondNe, Text: "cancelled"}}, false},
		"eq number":    {render.AttrFilter{Attr: "n", Op: "eq", Num: 2.5, IsNum: true}, []entity.AttrCond{{Key: "n", Op: entity.CondEq, Text: "2.5", Num: 2.5}}, false},
		"lt":           {render.AttrFilter{Attr: "n", Op: "lt", Num: 3, IsNum: true}, []entity.AttrCond{{Key: "n", Op: entity.CondLt, Num: 3}}, false},
		"lte":          {render.AttrFilter{Attr: "n", Op: "lte", Num: 3, IsNum: true}, []entity.AttrCond{{Key: "n", Op: entity.CondLe, Num: 3}}, false},
		"gt":           {render.AttrFilter{Attr: "n", Op: "gt", Num: 3, IsNum: true}, []entity.AttrCond{{Key: "n", Op: entity.CondGt, Num: 3}}, false},
		"gte":          {render.AttrFilter{Attr: "n", Op: "gte", Num: 3, IsNum: true}, []entity.AttrCond{{Key: "n", Op: entity.CondGe, Num: 3}}, false},
		"gte text":     {render.AttrFilter{Attr: "n", Op: "gte", Text: "x"}, []entity.AttrCond{{Key: "n"}}, false},
	} {
		got, relative := attrConds([]render.AttrFilter{tc.in}, today)
		if !reflect.DeepEqual(got, tc.want) || relative != tc.relative {
			t.Errorf("%s: %+v relative=%v, want %+v relative=%v", name, got, relative, tc.want, tc.relative)
		}
	}
}

func TestQueryAttrKeysIncludeFilters(t *testing.T) {
	got := queryAttrKeys(render.QuerySpec{Order: "attr:start_date", Filters: []render.AttrFilter{{Attr: "status", Op: "ne"}}})
	if !reflect.DeepEqual(got, []string{"start_date", "status"}) {
		t.Errorf("keys = %v", got)
	}
}
