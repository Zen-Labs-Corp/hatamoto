// Package ui holds the typed shapes of the admin partials (web/admin/partials).
// Templates may use either these structs or plain dicts; handlers build the
// structs so a misspelt field is a compile error, not an empty cell.
package ui

import (
	"net/url"
	"strconv"
	"strings"
)

// Option is one choice of a select or radio group.
type Option struct {
	Value, Label string
	Selected     bool
}

// Button is the hm/button partial.
type Button struct {
	Label     string
	Variant   string // primary | secondary | ghost | danger
	Size      string // md | sm
	Type      string // button | submit
	Href      string // renders an <a>
	Icon      string
	IconOnly  bool
	AriaLabel string // required with IconOnly
	Attrs     map[string]string
}

// Dict converts the button for {{template "hm/button" .Dict}}.
func (b Button) Dict() map[string]any {
	return map[string]any{
		"Label": b.Label, "Variant": b.Variant, "Size": b.Size, "Type": b.Type, "Href": b.Href,
		"Icon": b.Icon, "IconOnly": b.IconOnly, "AriaLabel": b.AriaLabel, "Attrs": b.Attrs,
	}
}

// Badge is the hm/badge partial. Status is never conveyed by colour alone: a
// dot (unless Icon, Plain or Mono is set) and the label always render.
type Badge struct {
	Label string
	Kind  string // neutral | success | warning | danger | info | muted (Off, Disabled, Paused, Hidden) | live (the active theme)
	// Icon is an optional icon name drawn before the label (the delta arrows).
	Icon string
	// Plain drops the dot of a neutral label, Wrap lets long text wrap, Mono is a read-only
	// identifier or tag (monospace, wraps). A coloured Kind keeps its dot either way.
	Plain, Wrap, Mono bool
	// Prefix is read by screen readers before the label ("Kind: ") and not drawn.
	Prefix string
	// Size "sm" is the compact pill (hm-badge--sm); empty is the default size.
	Size string
}

// Cell is one table cell: text, optionally a link, or a badge, optionally followed by a
// proportion bar or a trend line. No raw HTML.
type Cell struct {
	Text     string
	Link     string
	Badge    *Badge
	Depth    int    // indent of a tree row's name, in levels
	Nowrap   bool   // keep the text on one line (dates)
	Mono     bool   // muted monospace text (a slug or an address)
	Clip     bool   // cut long text with an ellipsis; the full text is the title (addresses)
	Sub      string // muted monospace line under the text (a URL path)
	Initials string // decorative avatar before the text (a person's initials)
	Suffix   string // muted text after the name, e.g. "(you)"
	Tag      *Badge // small badge after the text, e.g. "not encrypted"
	Bar      *Bar   // inline proportion bar after the text
	Spark    *Spark // trend line after the text (a focusable role=img with a summary)
}

// Action is a row or overflow-menu action: a link (Href) or a POST form (Post).
type Action struct {
	Label  string
	Href   string
	Post   string // form action; the CSRF token is added
	Icon   string // optional icon name shown before the label in a row menu
	Danger bool
	// Hint is a second, muted line under the label of a pageheader overflow item.
	Hint   string
	NewTab bool // open Href in a new tab (a link menu item only)
	// Open is the id of a dialog the link opens with JavaScript; Href stays the
	// no-JS page (a link menu item only).
	Open   string
	Fields map[string]string // hidden fields sent with a Post action
	// Confirm makes the action ask first. Href is then the confirmation page
	// (works without JavaScript) and Post the action the page submits; with JS
	// the link opens a native <dialog> showing Confirm instead of navigating.
	Confirm      string
	ConfirmLabel string // the dialog's confirm button (default: Label)
	// Button shows the action as a visible button in the actions cell instead of
	// a row menu item (a link action only: Href, Open or Confirm). AriaLabel
	// names it when the row is not clear from the label alone.
	Button    bool
	AriaLabel string
}

// Row is one table row.
type Row struct {
	ID      string
	Label   string // accessible name of the row (checkbox label)
	Cells   []Cell
	Actions []Action
}

// SafeActions returns the row's non-destructive menu actions, in order.
func (r Row) SafeActions() []Action { return filterActions(r.Actions, false) }

// ButtonActions returns the actions shown as visible buttons, in order.
func (r Row) ButtonActions() []Action {
	var out []Action
	for _, a := range r.Actions {
		if a.Button {
			out = append(out, a)
		}
	}
	return out
}

// HasMenu reports whether any action belongs in the row menu.
func (r Row) HasMenu() bool { return len(r.SafeActions())+len(r.DangerActions()) > 0 }

// DangerActions returns the row's destructive actions, in order. A row menu
// lists them last, behind a separator.
func (r Row) DangerActions() []Action { return filterActions(r.Actions, true) }

func filterActions(actions []Action, danger bool) []Action {
	var out []Action
	for _, a := range actions {
		if a.Danger == danger && !a.Button {
			out = append(out, a)
		}
	}
	return out
}

// Col is a table column.
type Col struct {
	Key, Label string
	Sortable   bool
	Align      string // "" | right
	// Role shapes the column: title (takes the slack and wraps), num (right,
	// tabular digits), check, actions, plain (wraps but takes no slack and has
	// no minimum width: for a narrow table). With no title column, the first is it.
	Role string
	// WideOnly drops the column's cells from the compact phone cards (below
	// 640px); the header stays in the sort strip only if it is sortable.
	WideOnly bool
}

// Filter is a select filter above the table.
type Filter struct {
	Name, Label string
	Options     []Option
	// Input renders an <input type=Input> (for example "date") holding Value
	// instead of a <select>.
	Input, Value string
	// Control selects another popover control: "checkbox" renders Options as a
	// group of checkboxes (Option.Selected is the checked state, repeated Name),
	// "switch" a single switch posting Value when on and OffValue when off.
	// Only the filter bar's popover honours it; "" is the select or Input above.
	Control  string
	Checked  bool
	OffValue string
}

// BulkAction posts the checked row ids to Action.
type BulkAction struct {
	Label, Action, Value string
	Danger               bool
	// Confirm makes the button ask first when JavaScript runs ("{n}" is the
	// number of checked rows). Without JavaScript the button posts and the
	// handler answers with a confirmation page.
	Confirm string
	// Select adds a choice that posts with the button (the button first, then
	// the select), for one action with a target such as a language.
	Select *BulkSelect
}

// BulkSelect is the select beside a bulk button. Label names it for screen
// readers; the chosen option posts as Name.
type BulkSelect struct {
	Name, Label string
	Options     []Option
}

// Table is the hm/table partial. State lives in the URL (?q=&sort=&dir=&page=)
// so tables are linkable and testable.
type Table struct {
	ID, Caption, BaseURL string
	CSRF                 string
	Columns              []Col
	Rows                 []Row
	SortKey, SortDir     string // asc | desc
	Query                string // search text
	SearchLabel          string // "" hides the search box
	Filters              []Filter
	// PrimaryFilters (selects) sit beside the search box. PopoverFilters live
	// in a "Filters" popover; Chips list the active popover filters, each with
	// a link that drops only that one, and ClearHref drops them all. Setting
	// either PopoverFilters or Chips selects the filter bar layout over Filters.
	PrimaryFilters []Filter
	PopoverFilters []Filter
	Chips          []Chip
	ClearHref      string
	// Opt-in filter bar layout (all zero by default, which renders the plain
	// bar). Segmented is a radio control before the primary filters that
	// applies live. Live makes the popover fields apply on change (no Apply
	// button; the noscript Apply stays). Target is the id of the results the
	// swap replaces (default ID) so the form and an open popover stay put.
	// FilterCount is the Filters badge (default len Chips). Bare has no
	// card of its own (a padded strip inside the list card) and groups
	// segmented + Filters apart from search + selects.
	// ChipsOutside leaves rendering the chips (hm/filterchips) to the page.
	Segmented    *Segmented
	Live         bool
	Target       string
	FilterCount  int
	Bare         bool
	ChipsOutside bool
	OOB          string            // extra out-of-band selectors the filter swap also refreshes, comma separated
	Hidden       map[string]string // fixed query values the filter form carries, e.g. the open form
	Bulk         []BulkAction
	BulkURL      string
	Page         Pagination
	Empty        Empty
	Error        string // non-empty renders the error state with Retry
	// CreatedID marks the row with this ID as just created: it gets the
	// hm-row--new class and a visually hidden "(new)" so colour is not alone.
	CreatedID string
}

// Chip is one removable active filter.
type Chip struct{ Label, RemoveHref string }

// FilterBar reports whether the table uses the search + select + popover bar.
func (t Table) FilterBar() bool { return len(t.PrimaryFilters) > 0 || len(t.PopoverFilters) > 0 }

// SortHref is the URL of the column's sort link: the active column flips
// direction, any other starts ascending; paging restarts.
func (t Table) SortHref(key string) string {
	dir := "asc"
	if t.SortKey == key && t.SortDir == "asc" {
		dir = "desc"
	}
	q := url.Values{}
	for k, v := range t.Page.Query {
		q[k] = v
	}
	q.Set("sort", key)
	q.Set("dir", dir)
	q.Del("page")
	return t.BaseURL + "?" + q.Encode()
}

// AriaSort is the aria-sort value of a column header.
func (t Table) AriaSort(key string) string {
	if t.SortKey != key {
		return "none"
	}
	if t.SortDir == "desc" {
		return "descending"
	}
	return "ascending"
}

// ColLabel is the header text of column i ("" when there is none). Cells carry
// it as data-label so narrow screens can show a label next to each value.
func (t Table) ColLabel(i int) string {
	if i >= 0 && i < len(t.Columns) {
		return t.Columns[i].Label
	}
	return ""
}

// role is the effective role of column i: its own, or title for the first
// column when no column declares one.
func (t Table) role(i int) string {
	if i < 0 || i >= len(t.Columns) {
		return ""
	}
	if r := t.Columns[i].Role; r != "" {
		return r
	}
	for _, c := range t.Columns {
		if c.Role == "title" {
			return ""
		}
	}
	if i == 0 {
		return "title"
	}
	return ""
}

// CellClass is the class of column i's header and cells: its role and its
// alignment.
func (t Table) CellClass(i int) string {
	if i < 0 || i >= len(t.Columns) {
		return ""
	}
	var cls []string
	switch t.role(i) {
	case "title":
		cls = append(cls, "hm-col-title")
	case "plain":
		cls = append(cls, "hm-col-plain")
	case "num":
		cls = append(cls, "hm-col-num")
	case "check":
		cls = append(cls, "hm-table__check")
	case "actions":
		cls = append(cls, "hm-table__actions")
	}
	if t.Columns[i].WideOnly {
		cls = append(cls, "hm-col-wide")
	}
	if t.Columns[i].Align != "" {
		cls = append(cls, "hm-table__"+t.Columns[i].Align)
	}
	return strings.Join(cls, " ")
}

// HasActions reports whether any row has actions (adds the actions column).
func (t Table) HasActions() bool {
	for _, r := range t.Rows {
		if len(r.Actions) > 0 {
			return true
		}
	}
	return false
}

// Colspan is the number of columns including bulk and actions.
func (t Table) Colspan() int {
	n := len(t.Columns)
	if len(t.Bulk) > 0 {
		n++
	}
	if t.HasActions() {
		n++
	}
	return n
}

// Empty is the hm/empty partial.
type Empty struct {
	Icon, Title, Body string
	Actions           []Button
	// Compact drops the padding and the border for an empty list inside a card.
	Compact bool
	// Code is a muted status above the title ("404"); Plain centres it with no frame, for a panel.
	Code  string
	Plain bool
	// Regular sets the title in normal weight, for a plain sentence rather than a heading.
	Regular bool
	// H1 makes the title the page heading, for a full-page error.
	H1 bool
}

// Pagination is the hm/pagination partial. Query holds the other URL state
// (filters, sort) that every page link keeps.
type Pagination struct {
	Page, PerPage, Total int
	BaseURL              string
	Query                url.Values
	Window               int // pages shown each side of the current page (default 2)
	// Param is the query parameter that carries the page number (default "page"),
	// for a page with more than one list.
	Param string
}

// PageLink is one entry of the page list; N == 0 is a gap ("…").
type PageLink struct {
	N       int
	Current bool
}

// Last is the last page number (at least 1).
func (p Pagination) Last() int {
	if p.PerPage <= 0 || p.Total <= 0 {
		return 1
	}
	return (p.Total + p.PerPage - 1) / p.PerPage
}

// From and To are the 1-based first and last row shown.
func (p Pagination) From() int {
	if p.Total == 0 {
		return 0
	}
	return (p.Page-1)*p.PerPage + 1
}

// To is the last row shown.
func (p Pagination) To() int {
	to := p.Page * p.PerPage
	if to > p.Total {
		to = p.Total
	}
	return to
}

// HasPrev and HasNext report neighbouring pages.
func (p Pagination) HasPrev() bool { return p.Page > 1 }

// HasNext reports a later page.
func (p Pagination) HasNext() bool { return p.Page < p.Last() }

// Href is the URL of page n.
func (p Pagination) Href(n int) string {
	q := url.Values{}
	for k, v := range p.Query {
		q[k] = v
	}
	param := p.Param
	if param == "" {
		param = "page"
	}
	if n > 1 {
		q.Set(param, strconv.Itoa(n))
	} else {
		q.Del(param)
	}
	if len(q) == 0 {
		return p.BaseURL
	}
	return p.BaseURL + "?" + q.Encode()
}

// Links is the windowed page list: first, last, the window around the current
// page, and gaps.
func (p Pagination) Links() []PageLink {
	w := p.Window
	if w <= 0 {
		w = 2
	}
	last := p.Last()
	var out []PageLink
	prev := 0
	for n := 1; n <= last; n++ {
		if n != 1 && n != last && (n < p.Page-w || n > p.Page+w) {
			continue
		}
		if prev != 0 && n != prev+1 {
			out = append(out, PageLink{})
		}
		out = append(out, PageLink{N: n, Current: n == p.Page})
		prev = n
	}
	return out
}

// Tab is a link tab of hm/tabs.
type Tab struct {
	Label, Href string
	Count       int
	Active      bool
}

// TabMore is the overflow of a tab row: a "More" button opening a menu of the
// remaining tabs, with a filter box (Filter is its placeholder) when set. When
// the current tab is in the menu the button shows its label instead.
type TabMore struct {
	ID, Label, Filter string
	Items             []Tab
}

// Current is the active tab in the menu, if any.
func (m TabMore) Current() *Tab {
	for i := range m.Items {
		if m.Items[i].Active {
			return &m.Items[i]
		}
	}
	return nil
}

// ButtonMenu is a primary button that opens a menu of links (the Create menu).
type ButtonMenu struct {
	ID, Label, Icon string
	Items           []Action
	// Footer items come last behind a separator.
	Footer []Action
}

// Secret is the hm/secretbanner partial: a one-time value (API key, invite
// link) shown above a list with Copy and Dismiss. DismissHref is the list URL
// without the secret, so without JS Dismiss reloads the clean list.
type Secret struct{ Heading, Note, Value, DismissHref string }

// Crumb is a breadcrumb.
type Crumb struct{ Label, Href string }

// FieldError links the error summary to a field.
type FieldError struct{ ID, Label, Message string }

// Step is one entry of the hm/steps progress list.
type Step struct {
	Label         string
	Current, Done bool
}
