package ui

import "strconv"

// Stat is one card of hm/stats. Variant "ticker" shows Label, Value and an
// optional Change, Spark, Note and Link; "ring" shows a progress ring (Pct, 0 to 100) beside the
// percentage, the Value line ("550 / 1,000") and the Label; "overview" is one
// card of rings, each item with Label, Detail ("N of M ...") and Pct.
type Stat struct {
	Label, Value, Detail string
	Pct                  int
	// Ring picks the ring card inside a ticker grid; Pct is then the ratio.
	Ring   bool
	Change *StatChange
	// Note is the optional muted line under a ticker card.
	Note string
	// Link is the optional footer link of a ring card ("View more"); a ticker
	// card shows it as a plain link under its Note.
	Link, LinkLabel string
	// Badge replaces Value on a ticker card whose value is a status, not a number
	// ("Up to date"): a metric is 28px medium, a status is a badge.
	Badge *Badge
	// Spark is the optional trend line under a ticker card's value.
	Spark *Spark
	// Muted marks Value as a placeholder such as "--"; it renders in the muted colour.
	Muted bool
	// Small shows Value one step smaller than a metric, for a secondary figure such as a timestamp.
	Small bool
}

// StatChange is the change against the previous period. Dir is "up", "down" or
// "flat" and picks the screen reader word; Tone (success, danger
// or neutral) picks the colour of the delta text; Text is the amount and Caption what it
// is measured against.
type StatChange struct {
	Dir, Text, Tone, Caption string
}

// LowerIsBetter flips the tone for a metric that should fall (spoofed fetches,
// errors): a rise is danger, a fall is success. It returns c for chaining and
// is safe on nil.
func (c *StatChange) LowerIsBetter() *StatChange {
	if c == nil {
		return nil
	}
	switch c.Dir {
	case "up":
		c.Tone = "danger"
	case "down":
		c.Tone = "success"
	}
	return c
}

const prevPeriod = "vs previous period"

// StatChangeOf describes cur against prev, computed from the two numbers; nil
// when both are zero, because there is nothing to compare. A rise is
// success-toned; use LowerIsBetter for metrics where it is not.
func StatChangeOf(cur, prev int64, fmtNum func(int64) string) *StatChange {
	if cur == 0 && prev == 0 {
		return nil
	}
	d := cur - prev
	if d == 0 {
		return &StatChange{Dir: "flat", Text: "No change", Tone: "neutral", Caption: prevPeriod}
	}
	dir, tone, sign, abs := "up", "success", "+", d
	if d < 0 {
		dir, tone, sign, abs = "down", "danger", "-", -d
	}
	text := sign + fmtNum(abs)
	if prev > 0 {
		text += " (" + sign + strconv.FormatFloat(float64(abs)/float64(prev)*100, 'f', 1, 64) + "%)"
	}
	return &StatChange{Dir: dir, Text: text, Tone: tone, Caption: prevPeriod}
}
