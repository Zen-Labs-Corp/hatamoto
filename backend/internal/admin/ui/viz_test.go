package ui

import (
	"strings"
	"testing"
)

func TestBarListBars(t *testing.T) {
	got := BarList{Items: []BarItem{{N: 3}, {N: 1}, {N: 0.001}, {N: 0}}}.Bars()
	want := []string{"100", "33.33", "1", "0"}
	for i, w := range want {
		if got[i].Width != w {
			t.Errorf("bar %d: width %q, want %q", i, got[i].Width, w)
		}
	}
	if len(BarList{Items: []BarItem{{N: 0}}}.Bars()) != 1 {
		t.Error("an all-zero list must still return its rows")
	}
}

func TestCategoryBarSegs(t *testing.T) {
	c := CategoryBar{Segments: []CategorySegment{{N: 1, Kind: "danger"}, {N: 0}, {N: 2, Kind: "bogus"}}, Marker: &CategoryMarker{At: 140}}
	s := c.Segs()
	if len(s) != 2 || s[0].X != "0" || s[0].Width != "33.33" || s[1].X != "33.33" || s[1].Width != "66.67" {
		t.Errorf("segs = %+v", s)
	}
	if s[0].Class != "danger" || s[1].Class != "series-3" {
		t.Errorf("classes = %q, %q", s[0].Class, s[1].Class)
	}
	if len(c.Legend()) != 3 {
		t.Error("the legend must keep the empty segment")
	}
	if c.MarkerAt() != "100" {
		t.Errorf("marker = %q, want clamped to 100", c.MarkerAt())
	}
	if (CategoryBar{}).MarkerAt() != "" || len((CategoryBar{Segments: []CategorySegment{{N: 0}}}).Segs()) != 0 {
		t.Error("empty bar: no marker, no segments")
	}
}

func TestSparkOf(t *testing.T) {
	if SparkOf(nil) != nil || SparkOf([]int64{4}) != nil || SparkOf([]int64{0, 0, 0}) != nil {
		t.Error("fewer than two points or all zeros: no spark")
	}
	if s := SparkOf([]int64{2, 0}); s == nil || s.Line != "0,2 100,22" {
		t.Errorf("spark = %+v", s)
	}
	if s := SparkOf([]int64{1, 1, 2, 2}); s.Summary != "Trend over 4 days: ending higher" || !strings.HasPrefix(s.ID, "spark-") {
		t.Errorf("summary %q id %q", s.Summary, s.ID)
	}
	if s := SparkOf([]int64{2, 2, 2, 2}, "weeks"); s.Summary != "Trend over 4 weeks: steady" {
		t.Errorf("flat summary %q", s.Summary)
	}
	if s := SparkOf([]int64{4, 4, 2, 2}); s.Summary != "Trend over 4 days: ending lower" {
		t.Errorf("down summary %q", s.Summary)
	}
	// A KPI badge already gives the change against the previous period: the
	// spark must not add a second percentage that can disagree with it.
	if s := SparkOf([]int64{4, 4, 2, 2}); strings.Contains(s.Summary, "%") {
		t.Errorf("summary %q has a percentage", s.Summary)
	}
	if (Progress{Pct: -3}).Width() != "0" || (Progress{Pct: 250}).Width() != "100" {
		t.Error("progress width must be clamped")
	}
	if (Callout{Kind: "bogus"}).KindOr() != "info" || (Callout{Kind: "danger"}).Icon() != "alert-circle" {
		t.Error("callout kind fallback")
	}
}

func TestSparkSplitOf(t *testing.T) {
	vals := []int64{1, 2, 1, 2, 1, 2, 1, 4, 3, 4, 3, 4, 3, 4}
	s := SparkSplitOf(vals, 7)
	if s == nil || s.PrevLine == "" || s.Divider == "" || s.Kind != "info" {
		t.Fatalf("split spark = %+v", s)
	}
	if got := len(strings.Fields(s.PrevLine)); got != 7 {
		t.Errorf("PrevLine has %d points, want 7", got)
	}
	if got := len(strings.Fields(s.Line)); got != 7 {
		t.Errorf("Line has %d points, want 7", got)
	}
	if s.Divider != "50" && s.Divider != "50.0" && !strings.HasPrefix(s.Divider, "4") && !strings.HasPrefix(s.Divider, "5") {
		t.Errorf("divider %q sits between the two halves", s.Divider)
	}
	if !strings.HasPrefix(s.Area, "M53.85,24 L") || !strings.HasSuffix(s.Area, " L100,24 Z") {
		t.Errorf("area %q covers only the later period", s.Area)
	}
	if !strings.HasPrefix(s.Line, "53.85,") || !strings.HasPrefix(s.PrevLine, "0,") {
		t.Errorf("x axis is not continuous: prev %q line %q", s.PrevLine, s.Line)
	}
	if s.Summary != SparkOf(vals).Summary || s.ID == SparkOf(vals).ID {
		t.Errorf("summary %q or id %q", s.Summary, s.ID)
	}
	// Nothing to split: the plain spark, byte for byte.
	plain := SparkOf(vals)
	for _, prev := range []int{0, -1, len(vals), 99} {
		if got := SparkSplitOf(vals, prev); got.PrevLine != "" || got.Divider != "" || got.Line != plain.Line || got.ID != plain.ID || got.Kind != "" {
			t.Errorf("prev %d: %+v", prev, got)
		}
	}
	if SparkSplitOf(nil, 7) != nil || SparkSplitOf(make([]int64, 14), 7) != nil {
		t.Error("fewer than two points or all zeros: no spark")
	}
	if plain.PrevLine != "" || plain.Divider != "" {
		t.Error("SparkOf must stay unsplit")
	}
}

func TestCategoryBarRoundsOnlyTheOuterSegments(t *testing.T) {
	segs := CategoryBar{Label: "x", Segments: []CategorySegment{{N: 1}, {N: 0}, {N: 2}, {N: 1}}}.Segs()
	if len(segs) != 3 || !segs[0].First || segs[0].Last || segs[1].First || segs[1].Last || !segs[2].Last || segs[2].First {
		t.Errorf("first/last flags wrong: %+v", segs)
	}
}

// Gradient ids are page-global: proportional series must not share one.
func TestSparkIDsDifferForProportionalSeries(t *testing.T) {
	a, b := SparkOf([]int64{0, 1, 0}), SparkOf([]int64{0, 2, 0})
	if a.ID == b.ID {
		t.Errorf("both sparks have id %q", a.ID)
	}
}
