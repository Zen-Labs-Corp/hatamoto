package ui

import (
	"fmt"
	"hash/fnv"
	"math"
	"strconv"
	"strings"
)

// The shapes of hm/barlist, hm/tracker and hm/categorybar, server-rendered
// ports of the Tremor components of the same name. Widths and offsets are
// SVG attributes, never style="" (the admin CSP has no 'unsafe-inline'), and
// every bar carries its numbers as text, so colour and length are never the
// only cue.

// BarList is hm/barlist: a ranked list of names, each over a bar as long as
// its share of the largest value, with the value at the right.
type BarList struct {
	Label string // accessible name of the list, required
	// NameHeading and ValueHeading are the optional column captions above the
	// list ("Source", "Visits").
	NameHeading, ValueHeading string
	Items                     []BarItem
	Empty                     string // shown when Items is empty
	Quiet                     bool   // paler bars (--chart-bar-quiet), for dense lists
	Mono                      bool   // names in the monospace face (paths)
}

// BarItem is one row of a BarList. N sizes the bar; Value is the text shown
// (formatted by the handler, "1,204"). Note is a muted word after the name
// (a page type) and Share a muted percentage after the value ("12%").
type BarItem struct {
	Label, Href, Value string
	Note, Share        string
	N                  float64
}

// BarRow is a BarItem with its bar width, in percent of the list's largest N.
type BarRow struct {
	BarItem
	Width string
}

// Bars returns the rows with their widths. A non-zero value never draws a
// bar shorter than 1%, so it stays visible beside a much larger one.
func (b BarList) Bars() []BarRow {
	var maxN float64
	for _, it := range b.Items {
		maxN = max(maxN, it.N)
	}
	out := make([]BarRow, len(b.Items))
	for i, it := range b.Items {
		w := 0.0
		if maxN > 0 && it.N > 0 {
			w = max(it.N/maxN*100, 1)
		}
		out[i] = BarRow{BarItem: it, Width: pct(w)}
	}
	return out
}

// Tracker is hm/tracker: a row of small blocks, one per period (a day, a
// check), each coloured by its status. Tip names the block for the tooltip
// and for screen readers ("Sep 17: 3 errors").
//
// Start and End caption the two ends of the row ("17 Sep", "16 Oct"); Legend
// names the kinds the blocks use, so colour is never the only cue.
type Tracker struct {
	Label      string // accessible name of the row, required
	Blocks     []TrackerBlock
	Start, End string
	Legend     []TrackerKey
	// States draws the analytics cues: hatched danger blocks, shorter warning
	// blocks and hollow neutral blocks, so the state is not colour alone.
	States bool
}

// TrackerKey is one legend entry of a Tracker: a Kind swatch and its meaning.
type TrackerKey struct {
	Kind, Label string
}

// TrackerBlock is one block. Kind is success, warning, danger, info or
// neutral (no data); empty is neutral.
type TrackerBlock struct {
	Kind, Tip string
}

// CategoryBar is hm/categorybar: one bar split into segments by share, with a
// legend that names each segment and its value. Marker optionally points at a
// position on the bar (a target, the current value).
type CategoryBar struct {
	Label    string // accessible name, required
	Segments []CategorySegment
	Marker   *CategoryMarker
	// Columns lays the legend out as rows with the Value and the Share in
	// right-aligned columns, as the analytics pages do.
	Columns bool
}

// CategorySegment is one part of a CategoryBar. N sizes it; Value is the text
// shown in the legend. Kind is success, warning, danger, info or neutral; empty
// takes the next chart series colour.
type CategorySegment struct {
	Label, Value, Kind string
	// Share is the percentage column of a Columns legend; ignored otherwise.
	Share string
	N     float64
}

// CategoryMarker points at At percent (0 to 100) of the bar.
type CategoryMarker struct {
	At    float64
	Label string // "Target: 75%", shown in the legend
}

// CategorySeg is a segment placed on the bar: X and Width in percent.
type CategorySeg struct {
	CategorySegment
	Class, X, Width string
	// First and Last mark the two outer segments: only they get rounded
	// corners, so the bar reads as one rounded strip (an SVG rect cannot
	// round a single side).
	First, Last bool
}

// Segs places the segments end to end. Segments with N <= 0 are skipped on
// the bar but stay in the legend (Legend).
func (c CategoryBar) Segs() []CategorySeg {
	var total float64
	for _, s := range c.Segments {
		if s.N > 0 {
			total += s.N
		}
	}
	var out []CategorySeg
	x := 0.0
	for i, s := range c.Segments {
		if s.N <= 0 || total == 0 {
			continue
		}
		w := s.N / total * 100
		out = append(out, CategorySeg{CategorySegment: s, Class: segClass(s.Kind, i), X: pct(x), Width: pct(w)})
		x += w
	}
	if n := len(out); n > 0 {
		out[0].First, out[n-1].Last = true, true
	}
	return out
}

// Legend is every segment with its colour class, including empty ones.
func (c CategoryBar) Legend() []CategorySeg {
	out := make([]CategorySeg, len(c.Segments))
	for i, s := range c.Segments {
		out[i] = CategorySeg{CategorySegment: s, Class: segClass(s.Kind, i)}
	}
	return out
}

// MarkerAt is the marker position clamped to the bar.
func (c CategoryBar) MarkerAt() string {
	if c.Marker == nil {
		return ""
	}
	return pct(min(max(c.Marker.At, 0), 100))
}

var vizKinds = map[string]bool{"success": true, "warning": true, "danger": true, "info": true, "neutral": true,
	"series-1": true, "series-2": true, "series-3": true, "series-4": true, "series-5": true, "series-6": true}

func segClass(kind string, i int) string {
	if vizKinds[kind] {
		return kind
	}
	return "series-" + strconv.Itoa(i%6+1)
}

// pct formats a percentage for an SVG attribute: at most two decimals.
func pct(f float64) string { return strconv.FormatFloat(math.Round(f*100)/100, 'f', -1, 64) }

// Spark is hm/spark: a small trend line with a gradient area under it, for a
// KPI card. Line and Area are SVG geometry in a 100 by 24 box that the CSS
// stretches. Summary is the text alternative ("Trend over 30 days: ending higher"): it is the
// accessible name and the tooltip of the wrapper, which takes focus so a
// keyboard user reaches the tooltip (the svg itself is aria-hidden). ID names the gradient; it comes from the
// raw values and the unit, so the same data always renders the same markup. Kind picks the
// hue ("series-1" to "series-6", or a status kind); empty is series-4.
type Spark struct {
	Line, Area string
	ID         string
	Summary    string
	Kind       string
	// PrevLine and Divider are set only by SparkSplitOf: the earlier period's
	// polyline (drawn grey, Line and Area then cover the later period) and the
	// x of the divider between the two, in the same box.
	PrevLine, Divider string
	// Live, when set, leaves Line and Area empty: js/aia/page.js reads the chart of a data
	// endpoint and sets the two paths, the label and the tooltip on this same markup.
	Live *SparkLive
}

// SparkLive names the data a live Spark draws: the JSON endpoint Src and the key Chart under
// "charts". Ratio ("referrals,pageviews") draws the first over the second instead, Dataset picks
// one dataset of the chart, MinMax stretches min to max instead of starting at zero (shares).
type SparkLive struct {
	Src, Chart, Ratio, Dataset string
	MinMax                     bool
}

// SparkOf draws vals oldest first; nil when there are fewer than two points
// or nothing but zeros, because a flat line says nothing. unit names a point
// in the summary ("days", the default, or "weeks").
func SparkOf(vals []int64, unit ...string) *Spark {
	if len(vals) < 2 {
		return nil
	}
	var top int64
	for _, v := range vals {
		top = max(top, v)
	}
	if top <= 0 {
		return nil
	}
	var line strings.Builder
	n := float64(len(vals) - 1)
	for i, v := range vals {
		if i > 0 {
			line.WriteByte(' ')
		}
		line.WriteString(pct(float64(i)/n*100) + "," + pct(22-float64(max(v, 0))/float64(top)*20))
	}
	u := "days"
	if len(unit) > 0 {
		u = unit[0]
	}
	h := fnv.New32a()
	// the raw values, not the geometry (which is normalised to the maximum), so
	// proportional series on one page get different ids
	h.Write([]byte(fmt.Sprint(vals, u)))
	return &Spark{
		Line:    line.String(),
		Area:    "M0,24 L" + strings.ReplaceAll(line.String(), " ", " L") + " L100,24 Z",
		ID:      "spark-" + strconv.FormatUint(uint64(h.Sum32()), 36),
		Summary: sparkSummary(vals, u),
	}
}

// SparkSplitOf is SparkOf for a series that compares two periods: vals[:prev]
// is the earlier one. The earlier period is PrevLine (grey), the later one
// Line and Area (accent), on one continuous x axis and one y scale, with a
// divider between them. Without at least one point on each side it is SparkOf.
func SparkSplitOf(vals []int64, prev int, unit ...string) *Spark {
	s := SparkOf(vals, unit...)
	if s == nil || prev < 1 || prev >= len(vals) {
		return s
	}
	pts := strings.Split(s.Line, " ")
	s.PrevLine = strings.Join(pts[:prev], " ")
	s.Line = strings.Join(pts[prev:], " ")
	s.Area = "M" + strings.Split(pts[prev], ",")[0] + ",24 L" + strings.ReplaceAll(s.Line, " ", " L") + " L100,24 Z"
	s.Divider = pct((float64(prev) - 0.5) / float64(len(vals)-1) * 100)
	s.Kind = "info"
	s.ID += "-split" + strconv.Itoa(prev)
	return s
}

// sparkSummary describes the shape of vals, never a percentage: a KPI card
// shows the change against the previous period next to the spark, and a second
// number from a different baseline would contradict it. It compares the second
// half of vals with the first: "Trend over 30 days: ending higher", "ending
// lower", or "steady" within half a percent.
func sparkSummary(vals []int64, unit string) string {
	var first, second int64
	for i, v := range vals {
		if i < len(vals)/2 {
			first += v
		} else if i >= (len(vals)+1)/2 {
			second += v
		}
	}
	lead := "Trend over " + strconv.Itoa(len(vals)) + " " + unit + ": "
	switch {
	case first == 0 && second > 0:
		return lead + "ending higher"
	case first == 0:
		return lead + "steady"
	}
	switch change := float64(second-first) / float64(first) * 100; {
	case change >= 0.5:
		return lead + "ending higher"
	case change <= -0.5:
		return lead + "ending lower"
	}
	return lead + "steady"
}

// Progress is hm/progress: a labelled bar filled to Pct (0 to 100), with
// Value as the text at the right ("38%", "12 of 40"). Kind colours the fill:
// info (default), success, warning or danger.
type Progress struct {
	Label, Value, Kind string
	Pct                float64
}

// Width is Pct clamped to the bar.
func (p Progress) Width() string { return pct(min(max(p.Pct, 0), 100)) }

// Bar is hm/cellbar: a compact proportion bar for a table cell, filled to Pct (0 to 100).
// Label is its text alternative ("38% of pageviews"); Kind colours it like Progress.
type Bar struct {
	Label, Kind string
	Pct         float64
}

// Width is Pct clamped to the bar.
func (b Bar) Width() string { return pct(min(max(b.Pct, 0), 100)) }

// Callout is hm/callout: a titled notice inside the content (not a toast).
// Kind is info (default), success, warning or danger and picks the icon (error
// is accepted as danger). Title is optional when there is a Body. A DismissKey
// adds a dismiss button that is remembered for the browser session.
type Callout struct {
	Kind, Title, Body, ID, DismissKey string
	Compact                           bool
	Attrs                             map[string]string // extra data-*/aria-* attributes on the root
}

// Icon is the glyph for the Kind.
func (c Callout) Icon() string {
	switch c.Kind {
	case "success":
		return "check-circle"
	case "warning":
		return "alert"
	case "danger":
		return "alert-circle"
	}
	return "info"
}

// KindOr is the Kind, info when unset or unknown.
func (c Callout) KindOr() string {
	switch c.Kind {
	case "success", "warning", "danger":
		return c.Kind
	}
	return "info"
}
