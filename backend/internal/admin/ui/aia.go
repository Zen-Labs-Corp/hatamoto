package ui

// The shapes of the AI analytics partials (hm/aia-attention, hm/aia-chart). Like the other ui types they carry text the handler has
// already formatted; markup and classes live in the partials.

// AiaAttention is one cell of hm/aia-attention. Sev is 0 (nothing to do), 1
// (review) or 2 (act now); the badge word and colour follow it. The link shows
// only when Sev is above zero.
type AiaAttention struct {
	Sev               int
	Count, Text, Rate string
	Note              string // muted line under the rate
	Href, Action      string // link and its label ("Review spoofed")
}

// Badge is the status badge of the cell.
func (a AiaAttention) Badge() Badge {
	switch a.Sev {
	case 2:
		return Badge{Label: "Act now", Kind: "danger"}
	case 1:
		return Badge{Label: "Review", Kind: "warning"}
	}
	return Badge{Label: "Nothing to do", Kind: "success"}
}

// AiaChart is hm/aia-chart: a titled card whose figure js/aia/page.js fills
// with an SVG time chart from Src, over the server-rendered Table (which stays
// readable without JavaScript and behind "Show data").
type AiaChart struct {
	// ID is the card id ("w-ct-first"): the anchor of the attention links.
	ID, Title, Description string
	Src, Chart             string // JSON endpoint and the key under "charts"
	// Panels are further keys of the same JSON ("fetches|referrals"), Srcs one
	// endpoint per panel when they differ ("a.json|b.json"), PanelLabels the
	// title above each panel.
	Panels, Srcs, PanelLabels string
	// Sum names, per panel ("AI fetches|AI-referred visits"), the one series
	// that panel's datasets are added up into.
	Sum string
	// Ratio ("referrals,pageviews") draws the first chart over the second,
	// labelled RatioLabel.
	Ratio, RatioLabel string
	// Override is JSON restyling a series by name: {"Spoofed":{"cls":"hm-viz--danger"}}.
	Override string
	// Toggle switches the drawn chart between Chart and AltChart (js/aia/page.js
	// reads data-chart-toggle on its buttons); the data table follows, so
	// AltTable is the table of AltChart. All three are set together.
	Toggle   *Segmented
	AltChart string
	AltTable DataTable
	Hero     bool // taller plot
	Cols     bool // columns instead of an area
	Compare  bool // the page is comparing; the legend shows "Previous period"
	Table    DataTable
}

// AiaAgents is hm/aia-agents: the agents of one range, one table with a group
// per AI purpose, then the non-AI purposes as a list.
type AiaAgents struct {
	Caption string
	Groups  []AiaAgentGroup
	// Other are the non-AI purposes (search, social, SEO, archive).
	Other []AiaOtherPurpose
	// Summary is the one-line list of agents that get no group of their own.
	Summary *AiaSummary
}

// AiaSummary is a one-line list under the agents table: "Social, SEO tools,
// archives" and what each fetched.
type AiaSummary struct {
	Label string
	Items []AiaSummaryItem
}

// AiaSummaryItem is one agent of an AiaSummary with its formatted fetch count.
type AiaSummaryItem struct{ Name, Count string }

// AiaAgentGroup is one purpose of the agents table: its name, what it means,
// the totals sentence and the agents that fetched under it.
type AiaAgentGroup struct {
	Purpose, Description string
	Summary              string     // "27,997 fetches, 50% of all AI fetches."
	Policy               *AiaPolicy // the group's Allow / Block control; nil shows none
	Rows                 []AiaAgentRow
	// PolicyOnly are robots.txt names that never fetch (Google-Extended).
	PolicyOnly []AiaPolicyOnly
}

// AiaAgentRow is one agent. Share is its fetches as a percent of the group's
// busiest agent (the mini bar); Verification is the badge ("13 spoofed").
type AiaAgentRow struct {
	Name, Href, Operator string
	Fetches              string
	Share                int
	Verification         Badge
	Blocked, Ignores     bool // blocked by the purpose's policy; ignores robots.txt
	// Purpose is the purpose id the row sits under (policy.js shows "Blocked by
	// policy" from it); Fetched is true when the agent fetched in the range.
	Purpose  string
	Fetched  bool
	LastSeen string
}

// AiaPolicyOnly is a robots.txt name without fetches.
type AiaPolicyOnly struct{ Name, Href, Operator string }

// AiaOtherPurpose is one non-AI purpose row.
type AiaOtherPurpose struct {
	Purpose, Name, Description string
	Policy                     *AiaPolicy
}

// AiaPolicy is hm/aia-policy: the Allow / Block choice of one purpose. The
// owner gets a radio pair named purpose.<id> (so a plain form post carries
// every purpose); anyone else gets a badge. Legend names the group for screen
// readers ("Robots policy for all training agents").
type AiaPolicy struct {
	Purpose, Legend string
	Deny, Owner     bool
}

// AiaPolicyForm is hm/aia-policy-start and hm/aia-policy-end, which wrap the
// agents table in the one form that saves all eight purposes. Return is the
// page path the save comes back to. A non-owner gets no form and no bar.
type AiaPolicyForm struct {
	CSRF, Return string
	Owner        bool
}

// AiaRows is hm/aia-rows: a plain list as a responsive table inside a card the
// caller already opened (never-fetched pages, failing paths). Cells[i] sits
// under Columns[i]; the first is the row header. Below 720px each row stacks
// with the column name above each value. Page.Total above Page.PerPage adds the
// pager (real links).
type AiaRows struct {
	Caption string
	Columns []string
	// Right and HiddenHead, when set, are one flag per column: right-aligned
	// figures, and a header kept for screen readers but not drawn.
	Right, HiddenHead []bool
	Rows              [][]AiaCell
	Page              Pagination
	Empty             string
}

// AiaCell is one cell of AiaRows: text, optionally a link, followed by a
// monospace word (a slug), or a muted note, or a badge (after the text when both are set).
type AiaCell struct {
	Text, Href, Mono string
	Muted            bool
	Button           bool // the link is a small secondary button
	Badge            *Badge
	// Bar and Spark follow the text: a proportion bar (a share) and a trend line.
	Bar   *Bar
	Spark *Spark
}
