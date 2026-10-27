package ui

// DataTable is the hm/datatable partial: the numbers behind a chart as a plain
// table, readable without JavaScript and by screen readers. The first column is
// the row header.
type DataTable struct {
	Caption string
	Columns []string
	Rows    []DataRow
	// Total, when set, is a closing row (column sums).
	Total *DataRow
	Empty string
	// Flat drops the scroll box: the table is as tall as its rows (chart data).
	Flat bool
}

// DataRow is one row of a DataTable.
type DataRow struct {
	Label string
	Cells []string
}

// DateRange is the hm/daterange partial: a picker button opening from/to date
// inputs plus presets. Hidden carries the other URL state (tab, agent) through
// the form.
type DateRange struct {
	// ID prefixes the popover and input ids (default "range"), so two pickers can share a page.
	ID       string
	Action   string
	From, To string
	Hidden   map[string]string
	// Label is the picker button text: the matching preset's name and the span ("Last 30 days · 20 Sep – 19 Oct"), else "From - To".
	Label string
	// Quick is the preset list inside the picker popover; Min and Max bound the
	// date inputs (Max is today, UTC).
	Quick    []DatePreset
	Min, Max string
	// Compare adds a "Compare to previous period" switch to the popover. Nil (the default) renders the picker unchanged.
	Compare *DateCompare
}

// DateCompare is the optional compare switch of hm/daterange. The switch posts
// Field=1 with the form, so Apply carries it; the caller puts Field=1 on the
// preset hrefs itself.
type DateCompare struct {
	Field    string // query param the switch posts, "compare"
	On       bool
	Disabled bool   // no earlier data to compare with
	Label2   string // the button's second line, "vs 25 Aug – 23 Sep" (shown only when On)
	Help     string // "Compared with 25 Aug – 23 Sep."
}

// DatePreset is one entry of the picker's preset list: a link (Href) that
// applies the range at once.
type DatePreset struct {
	Label, Href string
	Active      bool
}
