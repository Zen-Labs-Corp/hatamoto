package admin

import (
	"cmp"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/admin/ui"
)

// Moderation status tabs; the whitelist excludes drafts (unconfirmed commenters).
const (
	modStatusPending  = "pending"
	modStatusApproved = "approved"
	modStatusSpam     = "spam"
	modStatusTrash    = "trash"
	modPerPage        = 50
	modOptionLimit    = 50
	modMaxQuery       = 200
	modExcerptRunes   = 140
)

var modStatusTabs = []struct{ Value, Label string }{
	{modStatusPending, "Pending"}, {modStatusApproved, "Approved"}, {modStatusSpam, "Spam"}, {modStatusTrash, "Trash"},
}

var modTrustOptions = []ui.Option{
	{Value: "", Label: "All commenters"}, {Value: "new", Label: "New"}, {Value: "known", Label: "Known"},
	{Value: "trusted", Label: "Trusted"}, {Value: "blocked", Label: "Blocked"},
}

// commentListParams are the moderation screen's filters, read from the URL.
type commentListParams struct {
	Status, Q, Entity, Trust string
	HasLinks                 bool
	Page                     int
}

// filtered reports whether a filter other than the status tab is set.
func (p commentListParams) filtered() bool {
	return p.Q != "" || p.Entity != "" || p.Trust != "" || p.HasLinks
}

// query is the URL state the pagination links carry.
func (p commentListParams) query() url.Values {
	q := url.Values{}
	if p.Status != modStatusPending {
		q.Set("status", p.Status)
	}
	if p.Q != "" {
		q.Set("q", p.Q)
	}
	if p.Entity != "" {
		q.Set("entity", p.Entity)
	}
	if p.Trust != "" {
		q.Set("trust", p.Trust)
	}
	if p.HasLinks {
		q.Set("has_links", "1")
	}
	return q
}

func readCommentParams(r *http.Request) commentListParams {
	q := r.URL.Query()
	p := commentListParams{Status: modStatusPending, Q: strings.TrimSpace(q.Get("q")), Page: 1}
	if rs := []rune(p.Q); len(rs) > modMaxQuery {
		p.Q = string(rs[:modMaxQuery])
	}
	for _, t := range modStatusTabs {
		if q.Get("status") == t.Value {
			p.Status = t.Value
		}
	}
	if _, err := uuid.Parse(q.Get("entity")); err == nil {
		p.Entity = q.Get("entity")
	}
	for _, o := range modTrustOptions[1:] {
		if q.Get("trust") == o.Value {
			p.Trust = o.Value
		}
	}
	p.HasLinks = q.Get("has_links") == "1"
	if n, err := strconv.Atoi(q.Get("page")); err == nil && n > 1 {
		p.Page = n
	}
	return p
}

// modData drives pages/comments/list.
type modData struct {
	Title, Description, Status string
	Table, Results             ui.Table
}

func (a *Admin) handleComments(w http.ResponseWriter, r *http.Request) {
	svc := a.deps.Comments
	if svc == nil {
		a.unavailable(w, r, "Comments")
		return
	}
	ctx := r.Context()
	p := readCommentParams(r)
	base := a.adminPath + "/comments"

	counts, countsErr := svc.Counts(ctx)
	if countsErr != nil {
		a.logger().Error("comment counts failed", "err", countsErr)
	}
	showCounts := countsErr == nil && !p.filtered()

	status := &ui.Segmented{Mode: "radio", Label: "Status", Name: "status"}
	for _, t := range modStatusTabs {
		item := ui.SegmentedItem{Label: t.Label, Value: t.Value, Pressed: p.Status == t.Value}
		if showCounts {
			item.Count, item.CountID = strconv.Itoa(modCount(counts, t.Value)), "comments-seg-"+t.Value+"-n"
		}
		status.Items = append(status.Items, item)
	}

	entityOpts := []ui.Option{{Value: "", Label: "All entities", Selected: p.Entity == ""}}
	if svc.Supports("entity-options") {
		if opts, err := svc.EntityOptions(ctx, modOptionLimit); err == nil {
			found := p.Entity == ""
			for _, o := range opts {
				entityOpts = append(entityOpts, ui.Option{Value: o.ID, Label: o.Title, Selected: p.Entity == o.ID})
				found = found || p.Entity == o.ID
			}
			if !found {
				entityOpts = append(entityOpts, ui.Option{Value: p.Entity, Label: "Selected entity", Selected: true})
			}
		} else {
			a.logger().Error("comment entity options failed", "err", err)
		}
	}
	var primary []ui.Filter
	if len(entityOpts) > 1 {
		primary = append(primary, ui.Filter{Name: "entity", Label: "Entity", Options: entityOpts})
	}
	trustOpts := make([]ui.Option, len(modTrustOptions))
	for i, o := range modTrustOptions {
		o.Selected = p.Trust == o.Value
		trustOpts[i] = o
	}
	primary = append(primary, ui.Filter{Name: "trust", Label: "Commenter trust", Options: trustOpts})
	popover := []ui.Filter{{Control: "switch", Name: "has_links", Label: "Has links", Value: "1", Checked: p.HasLinks}}

	var chips []ui.Chip
	if p.HasLinks {
		rest := p
		rest.HasLinks = false
		chips = append(chips, ui.Chip{Label: "Has links", RemoveHref: modHref(base, rest.query())})
	}

	t := ui.Table{
		ID: "comments", Caption: "Comments", BaseURL: base, CSRF: csrfToken(ctx), Wrap: true,
		Columns: []ui.Col{
			{Key: "comment", Label: "Comment", Role: "title"}, {Key: "trust", Label: "Trust"},
			{Key: "entity", Label: "On"}, {Key: "when", Label: "Written"},
		},
		Segmented: status, Query: p.Q, SearchLabel: "Search comments",
		PrimaryFilters: primary, PopoverFilters: popover, Chips: chips,
		ClearHref: modHref(base, commentListParams{Status: p.Status, Q: p.Q, Entity: p.Entity, Trust: p.Trust}.query()),
		ListBar:   true, Live: true, Target: "comments-results", ChipsOutside: true,
		Empty: ui.Empty{Icon: "message-square", Title: "No comments here", Body: "Nothing matches this view."},
	}

	page, err := svc.Queue(ctx, CommentFilter{
		Status: p.Status, Query: p.Q, EntityID: p.Entity, Trust: p.Trust, HasLinks: p.HasLinks, Page: p.Page, PerPage: modPerPage,
	})
	if err != nil {
		a.logger().Error("comment queue failed", "err", err)
		t.Error = "The comments could not be loaded."
	}
	back := r.URL.RequestURI()
	for _, c := range page.Rows {
		t.Rows = append(t.Rows, a.modRow(c, p.Status, back))
	}
	total := (p.Page-1)*modPerPage + len(page.Rows)
	if page.HasMore {
		total = p.Page*modPerPage + 1 // the store has no total: just enough for a next link
	}
	if showCounts && countsErr == nil {
		total = modCount(counts, p.Status)
	}
	t.Page = ui.Pagination{Page: p.Page, PerPage: modPerPage, Total: total, BaseURL: base, Query: p.query()}

	d := modData{Title: "Comments", Description: "Review what visitors wrote.", Table: t}
	if countsErr == nil {
		d.Description = fmt.Sprintf("%d waiting for review.", counts.Pending)
	}
	d.Status = fmt.Sprintf("%d %s on this page", len(page.Rows), plural(len(page.Rows), "comment", "comments"))
	d.Results = t
	d.Results.SearchLabel, d.Results.PrimaryFilters, d.Results.PopoverFilters, d.Results.Segmented = "", nil, nil, nil
	v := a.NewView(w, r, "Comments")
	v.Data = d
	a.Render(w, r, http.StatusOK, "comments/list", v)
}

func modCount(c CommentCounts, status string) int {
	switch status {
	case modStatusApproved:
		return c.Approved
	case modStatusSpam:
		return c.Spam
	case modStatusTrash:
		return c.Trash
	}
	return c.Pending
}

func modHref(base string, q url.Values) string {
	if len(q) == 0 {
		return base
	}
	return base + "?" + q.Encode()
}

// modRow is one comment row with the actions its tab allows.
func (a *Admin) modRow(c CommentRow, tab, back string) ui.Row {
	base := a.adminPath + "/comments/" + c.ID
	name := cmp.Or(c.AuthorName, "Unnamed")
	who := ui.Cell{Text: name, Sub: modExcerpt(c.BodyRaw)}
	if why := heldWhy(c); why != "" {
		who.Sub += " · " + why
	}
	trust := ui.Cell{}
	if c.AuthorUserID != "" {
		who.Tag = &ui.Badge{Label: "Author", Kind: "info"}
	} else {
		trust = ui.Cell{Tag: &ui.Badge{Label: c.Trust, Kind: "neutral"}, Sub: fmt.Sprintf("%d approved", c.ApprovedCount)}
	}
	on := ui.Cell{Text: c.EntityTitle}
	if c.EntityID != "" {
		on.Link = a.adminPath + "/entities/" + c.EntityID
	}
	when := ui.Cell{Text: relTime(time.Now(), c.CreatedAt), Title: fmtDateTime(c.CreatedAt), Nowrap: true}

	fields := map[string]string{"back": back}
	approve := ui.Action{Label: "Approve", Post: base + "/approve", Fields: fields}
	reply := ui.Action{Label: "Reply", Href: base + "/reply"}
	edit := ui.Action{Label: "Edit", Href: base + "/edit"}
	spam := ui.Action{Label: "Spam", Href: base + "/spam"}
	trash := ui.Action{Label: "Trash", Post: base + "/trash", Fields: fields, Danger: true}
	var actions []ui.Action
	switch tab {
	case modStatusPending:
		actions = []ui.Action{approve}
		if c.CommenterID != "" {
			actions = append(actions, ui.Action{Label: "Approve + vouch", Post: base + "/approve", Fields: map[string]string{"back": back, "vouch": "1"}})
		}
		actions = append(actions, reply, edit, spam, trash)
	case modStatusApproved:
		actions = []ui.Action{reply, edit, spam, trash}
	case modStatusSpam:
		approve.Label = "Not spam"
		actions = []ui.Action{approve, trash}
	case modStatusTrash:
		approve.Label = "Restore"
		actions = []ui.Action{approve, spam}
	}
	return ui.Row{ID: c.ID, Label: "comment by " + name, Cells: []ui.Cell{who, trust, on, when}, Actions: actions}
}

// modExcerpt is the first modExcerptRunes characters of a body on one line.
func modExcerpt(body string) string {
	s := strings.Join(strings.Fields(body), " ")
	if rs := []rune(s); len(rs) > modExcerptRunes {
		return string(rs[:modExcerptRunes]) + "…"
	}
	return s
}

// handleCommentsBulk is a stub until its screen lands.
func (a *Admin) handleCommentsBulk(w http.ResponseWriter, r *http.Request) {
	a.unavailable(w, r, "Comments")
}

// handleCommentApprove is a stub until its screen lands.
func (a *Admin) handleCommentApprove(w http.ResponseWriter, r *http.Request) {
	a.unavailable(w, r, "Comments")
}

// handleCommentSpamPage is a stub until its screen lands.
func (a *Admin) handleCommentSpamPage(w http.ResponseWriter, r *http.Request) {
	a.unavailable(w, r, "Comments")
}

// handleCommentSpam is a stub until its screen lands.
func (a *Admin) handleCommentSpam(w http.ResponseWriter, r *http.Request) {
	a.unavailable(w, r, "Comments")
}

// handleCommentTrash is a stub until its screen lands.
func (a *Admin) handleCommentTrash(w http.ResponseWriter, r *http.Request) {
	a.unavailable(w, r, "Comments")
}

// handleCommentReplyPage is a stub until its screen lands.
func (a *Admin) handleCommentReplyPage(w http.ResponseWriter, r *http.Request) {
	a.unavailable(w, r, "Comments")
}

// handleCommentReply is a stub until its screen lands.
func (a *Admin) handleCommentReply(w http.ResponseWriter, r *http.Request) {
	a.unavailable(w, r, "Comments")
}

// handleCommentEditPage is a stub until its screen lands.
func (a *Admin) handleCommentEditPage(w http.ResponseWriter, r *http.Request) {
	a.unavailable(w, r, "Comments")
}

// handleCommentEdit is a stub until its screen lands.
func (a *Admin) handleCommentEdit(w http.ResponseWriter, r *http.Request) {
	a.unavailable(w, r, "Comments")
}
