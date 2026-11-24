package admin

import (
	"cmp"
	"context"
	"errors"
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
	modStatusDead     = "dead"
	modPerPage        = 50
	modOptionLimit    = 50
	modMaxQuery       = 200
	modExcerptRunes   = 140
)

var modStatusTabs = []struct{ Value, Label string }{
	{modStatusPending, "Pending"}, {modStatusApproved, "Approved"}, {modStatusSpam, "Spam"}, {modStatusTrash, "Trash"},
	{modStatusDead, "Dead"},
}

var modTrustOptions = []ui.Option{
	{Value: "", Label: "All commenters"}, {Value: "new", Label: "New"}, {Value: "known", Label: "Known"},
	{Value: "trusted", Label: "Trusted"}, {Value: "blocked", Label: "Blocked"},
}

// commentListParams are the moderation screen's filters, read from the URL.
type commentListParams struct {
	Status, Q, Entity, Commenter, Trust string
	HasLinks                            bool
	Page                                int
}

// filtered reports whether a filter other than the status tab is set.
func (p commentListParams) filtered() bool {
	return p.Q != "" || p.Entity != "" || p.Commenter != "" || p.Trust != "" || p.HasLinks
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
	if p.Commenter != "" {
		q.Set("commenter", p.Commenter)
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
	if id, err := uuid.Parse(q.Get("commenter")); err == nil {
		p.Commenter = id.String()
	}
	for _, o := range modTrustOptions[1:] {
		if q.Get("trust") == o.Value {
			p.Trust = o.Value
		}
	}
	p.HasLinks = q.Get("has_links") == "1"
	if n, err := strconv.Atoi(q.Get("page")); err == nil && n > 1 {
		p.Page = min(n, maxListPage)
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
	if p.Commenter != "" {
		rest := p
		rest.Commenter = ""
		chips = append(chips, ui.Chip{Label: "One commenter", RemoveHref: modHref(base, rest.query())})
	}
	if p.HasLinks {
		rest := p
		rest.HasLinks = false
		chips = append(chips, ui.Chip{Label: "Has links", RemoveHref: modHref(base, rest.query())})
	}

	var hidden map[string]string
	if p.Commenter != "" {
		hidden = map[string]string{"commenter": p.Commenter}
	}
	t := ui.Table{
		ID: "comments", Hidden: hidden, Caption: "Comments", BaseURL: base, CSRF: csrfToken(ctx), Wrap: true,
		Columns: []ui.Col{
			{Key: "comment", Label: "Comment", Role: "title"}, {Key: "trust", Label: "Trust"},
			{Key: "entity", Label: "On"}, {Key: "when", Label: "Written"},
		},
		Segmented: status, Query: p.Q, SearchLabel: "Search comments",
		PrimaryFilters: primary, PopoverFilters: popover, Chips: chips,
		ClearHref: modHref(base, commentListParams{Status: p.Status, Q: p.Q, Entity: p.Entity, Commenter: p.Commenter, Trust: p.Trust}.query()),
		ListBar:   true, Live: true, Target: "comments-results", ChipsOutside: true,
		Bulk: modBulkActions(a.adminPath, p.Status), BulkURL: a.adminPath + "/comments/bulk",
		Empty: ui.Empty{Icon: "inbox", Title: "No comments here", Body: "Nothing matches this view."},
	}

	page, err := svc.Queue(ctx, CommentFilter{
		Status: p.Status, Query: p.Q, EntityID: p.Entity, CommenterID: p.Commenter, Trust: p.Trust, HasLinks: p.HasLinks, Page: p.Page, PerPage: modPerPage,
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
	case modStatusDead:
		return c.Dead
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
	who := ui.Cell{Text: name, Notes: []string{modExcerpt(c.BodyRaw)}}
	if why := heldWhy(c); why != "" {
		who.Notes = append(who.Notes, why)
	}
	if tab == modStatusDead {
		who.Tag = &ui.Badge{Label: "Dead", Kind: "muted"}
		if c.DeadBy != "" {
			who.Notes = append(who.Notes, "Marked dead by plugin "+c.DeadBy+".")
		}
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
	case modStatusDead:
		approve.Label = "Restore"
		// Delete posts to the bulk handler, which asks on its confirm page first.
		del := ui.Action{Label: "Delete", Post: a.adminPath + "/comments/bulk", Danger: true,
			Fields: map[string]string{"back": back, "action": "delete", "id": c.ID}}
		actions = []ui.Action{approve, trash, del}
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

// modLoad is the shared start of a single-comment action: the service, the
// comment id and the comment itself. It answers 503, 404 or 500 and reports
// false when it cannot go on.
func (a *Admin) modLoad(w http.ResponseWriter, r *http.Request) (CommentsService, CommentRow, bool) {
	svc := a.deps.Comments
	if svc == nil {
		a.unavailable(w, r, "Comments")
		return nil, CommentRow{}, false
	}
	id, ok := uuidParam(r, "id")
	if !ok {
		a.modNotFound(w, r)
		return nil, CommentRow{}, false
	}
	row, err := svc.Comment(r.Context(), id.String())
	if errors.Is(err, ErrCommentNotFound) {
		a.modNotFound(w, r)
		return nil, CommentRow{}, false
	}
	if err != nil {
		a.serverError(w, r, "comment load failed", err)
		return nil, CommentRow{}, false
	}
	return svc, row, true
}

func (a *Admin) modNotFound(w http.ResponseWriter, r *http.Request) {
	a.notFound(w, r, "Comment", a.adminPath+"/comments", "Back to comments")
}

// modActor is the signed-in user's id, as the service records it.
func modActor(r *http.Request) string {
	if u := userFrom(r.Context()); u != nil {
		return u.ID
	}
	return ""
}

// modBack is where an action returns to: the page it came from, kept under the comments area.
func (a *Admin) modBack(r *http.Request) string {
	return backTo(r, a.adminPath+"/comments", a.adminPath+"/comments")
}

// modFail logs err, tells the user the action failed without detail and goes back.
func (a *Admin) modFail(w http.ResponseWriter, r *http.Request, what string, err error) {
	a.logger().Error("comment "+what+" failed", "err", err)
	a.SetFlash(w, r, "error", "The comment could not be "+what+".")
	Redirect(w, r, a.modBack(r))
}

func (a *Admin) handleCommentApprove(w http.ResponseWriter, r *http.Request) {
	svc, row, ok := a.modLoad(w, r)
	if !ok {
		return
	}
	ctx, actor := r.Context(), modActor(r)
	if _, err := svc.Approve(ctx, []string{row.ID}, actor); err != nil {
		a.modFail(w, r, "approved", err)
		return
	}
	a.audit(r, "comments.approve", row.ID, map[string]any{"ids": []string{row.ID}, "done": 1})
	msg := "Comment approved."
	if r.PostFormValue("vouch") == "1" && row.CommenterID != "" {
		trust, err := svc.Vouch(ctx, row.CommenterID, actor)
		if errors.Is(err, ErrCommentBlocked) {
			a.SetFlash(w, r, "success", "Comment approved. The commenter is blocked, so they were not vouched for.")
			Redirect(w, r, a.modBack(r))
			return
		}
		if err != nil {
			a.logger().Error("comment vouch failed", "err", err)
			a.SetFlash(w, r, "error", "Comment approved, but the commenter could not be vouched for.")
			Redirect(w, r, a.modBack(r))
			return
		}
		a.audit(r, "comments.commenter.vouch", row.CommenterID, map[string]any{"comment": row.ID, "trust": trust})
		msg += " " + cmp.Or(row.AuthorName, "The commenter") + " is now " + trust + "."
	}
	a.SetFlash(w, r, "success", msg)
	Redirect(w, r, a.modBack(r))
}

func (a *Admin) handleCommentTrash(w http.ResponseWriter, r *http.Request) {
	svc, row, ok := a.modLoad(w, r)
	if !ok {
		return
	}
	if _, err := svc.Trash(r.Context(), []string{row.ID}, modActor(r)); err != nil {
		a.modFail(w, r, "moved to trash", err)
		return
	}
	a.audit(r, "comments.trash", row.ID, map[string]any{"ids": []string{row.ID}, "done": 1})
	a.SetFlash(w, r, "success", "Comment moved to trash.")
	Redirect(w, r, a.modBack(r))
}

// modSpamData drives pages/comments/spam.
type modSpamData struct {
	ID, Excerpt, Back string
	HasLinks          bool
}

func (a *Admin) handleCommentSpamPage(w http.ResponseWriter, r *http.Request) {
	_, row, ok := a.modLoad(w, r)
	if !ok {
		return
	}
	v := a.NewView(w, r, "Mark as spam")
	v.Width = "narrow"
	v.Crumbs = crumbList("Comments", a.adminPath+"/comments", "Mark as spam")
	v.Data = modSpamData{ID: row.ID, Excerpt: modExcerpt(row.BodyRaw), Back: a.modBack(r), HasLinks: row.HasLinks}
	a.Render(w, r, http.StatusOK, "comments/spam", v)
}

func (a *Admin) handleCommentSpam(w http.ResponseWriter, r *http.Request) {
	svc, row, ok := a.modLoad(w, r)
	if !ok {
		return
	}
	on := func(name string) bool { v := r.PostFormValue(name); return v == "1" || v == "true" }
	teach := SpamTeach{LinkDomains: on("link_domains"), EmailDomain: on("email_domain")}
	if _, err := svc.MarkSpam(r.Context(), []string{row.ID}, teach, modActor(r)); err != nil {
		a.modFail(w, r, "marked as spam", err)
		return
	}
	a.audit(r, "comments.spam", row.ID, map[string]any{"ids": []string{row.ID}, "done": 1, "link_domains": teach.LinkDomains, "email_domain": teach.EmailDomain})
	a.SetFlash(w, r, "success", "Comment marked as spam.")
	Redirect(w, r, a.modBack(r))
}

// modFormData drives pages/comments/reply and pages/comments/edit.
type modFormData struct {
	ID, Parent, Body, Back string
	Errs                   map[string]string
	Summary                []ui.FieldError
}

// modCheckBody trims the text and says what is wrong with it ("" when fine).
// The formatter has the last word; this only refuses early with its limit.
func modCheckBody(body, empty string) (string, string) {
	body = strings.TrimSpace(body)
	switch {
	case body == "":
		return body, empty
	case len(body) > maxCommentBodyBytes:
		return body, "This text is too long."
	}
	return body, ""
}

func (a *Admin) renderModForm(w http.ResponseWriter, r *http.Request, status int, page, title, label string, d modFormData, msg string) {
	d.Errs = map[string]string{}
	if msg != "" {
		d.Errs["body"] = msg
		d.Summary = []ui.FieldError{{ID: "body", Label: label, Message: msg}}
	}
	v := a.NewView(w, r, title)
	v.Width = "narrow"
	v.Crumbs = crumbList("Comments", a.adminPath+"/comments", title)
	v.Data = d
	a.Render(w, r, status, page, v)
}

// replyAuthor is the public name of a site reply: the user's display name, or
// the site name. CurrentUser.Name falls back to the login email, which must
// never be published.
func (a *Admin) replyAuthor(ctx context.Context, u *CurrentUser) string {
	if u != nil && u.Name != "" && !strings.EqualFold(u.Name, u.Email) {
		return u.Name
	}
	return a.siteName(ctx)
}

func (a *Admin) handleCommentReplyPage(w http.ResponseWriter, r *http.Request) {
	_, row, ok := a.modLoad(w, r)
	if !ok {
		return
	}
	a.renderModForm(w, r, http.StatusOK, "comments/reply", "Reply", "Reply", modFormData{ID: row.ID, Parent: modExcerpt(row.BodyRaw), Back: a.modBack(r)}, "")
}

func (a *Admin) handleCommentReply(w http.ResponseWriter, r *http.Request) {
	svc, row, ok := a.modLoad(w, r)
	if !ok {
		return
	}
	body, msg := modCheckBody(r.PostFormValue("body"), "Write a reply.")
	d := modFormData{ID: row.ID, Parent: modExcerpt(row.BodyRaw), Body: body, Back: a.modBack(r)}
	if msg == "" {
		u := userFrom(r.Context())
		replyID, err := svc.Reply(r.Context(), row.ID, body, modActor(r), a.replyAuthor(r.Context(), u))
		switch {
		case err == nil:
			a.audit(r, "comments.reply", row.ID, map[string]any{"reply": replyID})
			a.SetFlash(w, r, "success", "Reply posted.")
			Redirect(w, r, a.adminPath+"/comments?status=approved")
			return
		case errors.Is(err, ErrCommentEmpty):
			msg = "Write a reply."
		case errors.Is(err, ErrCommentTooLong):
			msg = "This text is too long."
		default:
			a.modFail(w, r, "posted", err)
			return
		}
	}
	a.renderModForm(w, r, http.StatusUnprocessableEntity, "comments/reply", "Reply", "Reply", d, msg)
}

func (a *Admin) handleCommentEditPage(w http.ResponseWriter, r *http.Request) {
	_, row, ok := a.modLoad(w, r)
	if !ok {
		return
	}
	a.renderModForm(w, r, http.StatusOK, "comments/edit", "Edit comment", "Comment", modFormData{ID: row.ID, Body: row.BodyRaw, Back: a.modBack(r)}, "")
}

func (a *Admin) handleCommentEdit(w http.ResponseWriter, r *http.Request) {
	svc, row, ok := a.modLoad(w, r)
	if !ok {
		return
	}
	body, msg := modCheckBody(r.PostFormValue("body"), "Write a comment.")
	d := modFormData{ID: row.ID, Body: body, Back: a.modBack(r)}
	if msg == "" {
		err := svc.Edit(r.Context(), row.ID, body, modActor(r))
		switch {
		case err == nil:
			a.audit(r, "comments.edit", row.ID, map[string]any{})
			a.SetFlash(w, r, "success", "Comment saved.")
			Redirect(w, r, d.Back)
			return
		case errors.Is(err, ErrCommentEmpty):
			msg = "Write a comment."
		case errors.Is(err, ErrCommentTooLong):
			msg = "This text is too long."
		default:
			a.modFail(w, r, "saved", err)
			return
		}
	}
	a.renderModForm(w, r, http.StatusUnprocessableEntity, "comments/edit", "Edit comment", "Comment", d, msg)
}

// modMaxBulk caps one bulk request.
const modMaxBulk = 100

// modBulkActions are the bulk actions that make sense on a tab: no Approve on
// Approved, no Spam on Spam, no Trash on Trash.
func modBulkActions(adminPath, tab string) []ui.BulkAction {
	target := adminPath + "/comments/bulk"
	var out []ui.BulkAction
	switch tab {
	case modStatusPending:
		out = append(out, ui.BulkAction{Label: "Approve selected", Action: target, Value: "approve"})
	case modStatusSpam:
		out = append(out, ui.BulkAction{Label: "Not spam", Action: target, Value: "approve"})
	case modStatusTrash:
		out = append(out, ui.BulkAction{Label: "Restore", Action: target, Value: "approve"})
	case modStatusDead:
		out = append(out, ui.BulkAction{Label: "Restore", Action: target, Value: "approve"})
	}
	if tab != modStatusSpam {
		out = append(out, ui.BulkAction{Label: "Spam", Action: target, Value: "spam", Danger: true})
	}
	if tab != modStatusTrash {
		out = append(out, ui.BulkAction{Label: "Trash", Action: target, Value: "trash", Danger: true})
	}
	return append(out, ui.BulkAction{Label: "Delete", Action: target, Value: "delete", Danger: true, Confirm: "Delete the selected comments and their replies for good?"})
}

// modUniqueIDs keeps the valid, distinct ids in order.
func modUniqueIDs(raw []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range raw {
		id, err := uuid.Parse(s)
		if err != nil || seen[id.String()] {
			continue
		}
		seen[id.String()] = true
		out = append(out, id.String())
	}
	return out
}

func (a *Admin) handleCommentsBulk(w http.ResponseWriter, r *http.Request) {
	svc := a.deps.Comments
	if svc == nil {
		a.unavailable(w, r, "Comments")
		return
	}
	_ = r.ParseForm()
	back := a.modBack(r)
	ids := modUniqueIDs(r.PostForm["id"])
	if len(ids) == 0 {
		a.SetFlash(w, r, "error", "Select at least one comment.")
		Redirect(w, r, back)
		return
	}
	if len(ids) > modMaxBulk {
		a.SetFlash(w, r, "error", fmt.Sprintf("Select at most %d comments.", modMaxBulk))
		Redirect(w, r, back)
		return
	}
	ctx, actor := r.Context(), modActor(r)
	action := r.PostFormValue("action")
	var (
		n    int
		err  error
		verb string
	)
	switch action {
	case "approve":
		n, err = svc.Approve(ctx, ids, actor)
		verb = "approved"
	case "spam":
		n, err = svc.MarkSpam(ctx, ids, SpamTeach{}, actor)
		verb = "marked as spam"
	case "trash":
		n, err = svc.Trash(ctx, ids, actor)
		verb = "moved to trash"
	case "delete":
		if r.PostFormValue("confirmed") != "1" {
			hidden := []hiddenField{{"action", "delete"}, {"confirmed", "1"}, {"back", back}}
			for _, id := range ids {
				hidden = append(hidden, hiddenField{"id", id})
			}
			noun := plural(len(ids), "comment", "comments")
			a.renderConfirm(w, r, confirmData{
				Title:   fmt.Sprintf("Delete %d %s?", len(ids), noun),
				Message: fmt.Sprintf("Delete %d %s and their replies for good? This cannot be undone.", len(ids), noun),
				Post:    a.adminPath + "/comments/bulk", Label: fmt.Sprintf("Delete %d %s", len(ids), noun), Back: back, Danger: true, Hidden: hidden,
			})
			return
		}
		n, err = svc.Delete(ctx, ids, actor)
		verb = "deleted"
	default:
		a.Error(w, r, http.StatusBadRequest, "Unknown action", "Choose an action for the selected comments.")
		return
	}
	// Rows changed before an error are real: audit them either way.
	if n > 0 {
		a.audit(r, "comments."+action, "comments", map[string]any{"ids": ids, "done": n, "skipped": len(ids) - n})
	}
	if err != nil {
		a.logger().Error("comment bulk "+action+" failed", "err", err)
		a.SetFlash(w, r, "error", fmt.Sprintf("%d of %d %s %s, then it stopped. Try again.", n, len(ids), plural(len(ids), "comment", "comments"), verb))
		Redirect(w, r, back)
		return
	}
	msg := fmt.Sprintf("%d %s %s.", n, plural(n, "comment", "comments"), verb)
	kind := "success"
	switch skipped := len(ids) - n; {
	case n == 0:
		kind, msg = "error", fmt.Sprintf("Nothing changed: the selected %s cannot be %s.", plural(len(ids), "comment", "comments"), verb)
	case skipped > 0:
		msg += fmt.Sprintf(" %d skipped (already in that state or gone).", skipped)
	}
	a.SetFlash(w, r, kind, msg)
	Redirect(w, r, back)
}
