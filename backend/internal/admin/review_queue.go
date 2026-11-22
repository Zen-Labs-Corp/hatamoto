package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"hatamoto.dev/core/authz"
	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/reviewstate"
)

const reviewPerPage = 25

// mountReview registers the review queue and the decisions on it. The queue is
// for people who may decide; a change's page also opens for the person who sent
// it. Every POST asks authz itself: the decide right for approve, send back,
// publish and discard, being the requester for withdraw. Self-decision is refused.
func (a *Admin) mountReview(content *httpx.Group) {
	content.Group("", a.RequireCan(authz.ActionEntityReviewDecide)).HandleFunc("GET /review", a.handleReviewQueue)
	content.HandleFunc("GET /review/{id}", a.handleReviewDetail)
	content.HandleFunc("GET /review/{id}/preview", a.handleReviewPreview)
	content.HandleFunc("POST /review/{id}/approve", a.handleReviewApprove)
	content.HandleFunc("POST /review/{id}/send-back", a.handleReviewSendBack)
	content.HandleFunc("POST /review/{id}/publish", a.handleReviewRetryPublish)
	content.HandleFunc("POST /review/{id}/withdraw", a.handleReviewWithdraw)
	content.HandleFunc("POST /review/{id}/discard", a.handleReviewDiscardEdit)
}

// reviewURL is the address of the review screens.
func (a *Admin) reviewURL(rest ...string) string {
	return a.adminPath + "/review" + strings.Join(rest, "")
}

type reviewQueuePage struct{ Table ui.Table }

func (a *Admin) handleReviewQueue(w http.ResponseWriter, r *http.Request) {
	rv := a.deps.Review
	if rv == nil {
		a.unavailable(w, r, "Review")
		return
	}
	page := pageParam(r)
	me := userFrom(r.Context())
	visible := func(p PendingEdit) bool {
		// Only the entries the viewer may decide (or sent themselves) are listed.
		return (me != nil && me.ID == p.Requester) || a.mayDecideStage(r.Context(), p.EntityID, p.Stage)
	}
	var rows []PendingEdit
	var total int
	var err error
	grouped := reviewstate.GroupsRegistered()
	if grouped {
		// A group stage is visible to some viewers only, so filter before paging:
		// a page of rows the viewer may not see must not hide the later ones.
		// At most reviewstate.MaxPending of the oldest waiting changes are read.
		rows, total, err = a.visibleQueue(r, page, visible)
	} else {
		rows, total, err = rv.Queue(r.Context(), reviewPerPage, (page-1)*reviewPerPage)
	}
	if err != nil {
		a.serverError(w, r, "The review queue could not be loaded", err)
		return
	}
	loc, now := a.siteLocation(r.Context()), time.Now()
	t := ui.Table{
		ID: "review-queue", Caption: "Changes waiting for review", BaseURL: a.reviewURL(),
		Columns: []ui.Col{{Key: "entry", Label: "Entry", Role: "title"}, {Key: "by", Label: "Requested by"}, {Key: "since", Label: "Waiting since"}},
		Page:    ui.Pagination{Page: page, PerPage: reviewPerPage, Total: total, BaseURL: a.reviewURL()},
		Empty:   ui.Empty{Icon: "check", Title: "Nothing is waiting for review.", Body: "Changes to published content that need a decision show up here."},
	}
	for _, p := range rows {
		if !grouped && !visible(p) { // with groups, visibleQueue already filtered
			continue
		}
		open := a.reviewURL("/", strconv.FormatInt(p.ID, 10))
		who := p.RequesterName
		if who == "" {
			who = "Unknown"
		}
		var notes []string
		if label := stageLabel(p.Stage); label != "" {
			notes = []string{label}
		}
		t.Rows = append(t.Rows, ui.Row{ID: strconv.FormatInt(p.ID, 10), Label: p.EntityName, Cells: []ui.Cell{
			{Text: p.EntityName, Link: open, Clip: true, Notes: notes},
			{Text: who},
			{Text: relTime(now, p.Since), Title: fmtDateTime(p.Since.In(loc)), Nowrap: true},
		}, Actions: []ui.Action{{Label: "Open", Href: open, Button: true}}})
	}
	v := a.NewView(w, r, "Review")
	v.Data = reviewQueuePage{Table: t}
	a.Render(w, r, http.StatusOK, "review/queue", v)
}

// visibleQueue reads the oldest waiting changes (up to reviewstate.MaxPending),
// keeps the ones the viewer may see and returns the requested page of those
// with their count, so paging and the total follow what the viewer can open.
func (a *Admin) visibleQueue(r *http.Request, page int, visible func(PendingEdit) bool) ([]PendingEdit, int, error) {
	all, _, err := a.deps.Review.Queue(r.Context(), reviewstate.MaxPending, 0)
	if err != nil {
		return nil, 0, err
	}
	var mine []PendingEdit
	for _, p := range all {
		if visible(p) {
			mine = append(mine, p)
		}
	}
	lo := min((page-1)*reviewPerPage, len(mine))
	return mine[lo:min(lo+reviewPerPage, len(mine))], len(mine), nil
}

// reviewCallout is a notice at the top of the detail page.
type reviewCallout struct{ Kind, Body string }

type reviewDetailPage struct {
	ID      int64
	Title   string
	Intro   string
	Badge   ui.Badge
	Callout []reviewCallout
	Rows    []diffRow
	Same    int
	// Actions holds the way to the preview, when the change can be previewed.
	Actions []ui.Button
	// Decide shows the comment box with Send back and, unless the entry moved, Approve.
	Decide     bool
	CanApprove bool
	// Retry shows the publish button of an approved change that is not live.
	Retry bool
	// Withdraw shows the requester's button; Discard the decider's.
	Withdraw, Discard bool
	Comment, Error    string
	URL               string
}

// reviewID reads the changeset id of the path.
func reviewID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil && id > 0
}

// reviewMissing answers a change that does not exist or is no longer waiting.
func (a *Admin) reviewMissing(w http.ResponseWriter, r *http.Request) {
	a.ErrorBack(w, r, http.StatusNotFound, "Change not found", "That change does not exist, or it is no longer waiting for review.", a.reviewURL(), "Back to review")
}

// loadReview reads the change of the path for a person who may see it: a decider
// or the requester. Anyone else gets the same answer as for a change that is not there.
func (a *Admin) loadReview(w http.ResponseWriter, r *http.Request) (ReviewDetail, bool) {
	rv := a.deps.Review
	if rv == nil {
		a.unavailable(w, r, "Review")
		return ReviewDetail{}, false
	}
	id, ok := reviewID(r)
	if !ok {
		a.reviewMissing(w, r)
		return ReviewDetail{}, false
	}
	d, err := rv.Detail(r.Context(), id)
	switch {
	case errors.Is(err, ErrReviewNotFound), errors.Is(err, ErrReviewNotStaged):
		a.reviewMissing(w, r)
		return d, false
	case err != nil:
		a.serverError(w, r, "The change could not be loaded", err)
		return d, false
	}
	if !a.isRequester(r, d) && !a.mayDecideStage(r.Context(), d.EntityID, d.Stage) {
		a.reviewMissing(w, r)
		return d, false
	}
	return d, true
}

// reviewObject is the object the review flow checks the decide right on.
func reviewObject(entityID string) authz.Object { return authz.Object{Kind: "entity", ID: entityID} }

// canDecide reports whether the viewer holds the decide right on one entry, as the flow checks it.
func (a *Admin) canDecide(ctx context.Context, entityID string) bool {
	return a.canObj(ctx, authz.ActionEntityReviewDecide, reviewObject(entityID))
}

// mayDecideStage is the decide right on the entry plus the group check of its current stage, as the flow applies it.
func (a *Admin) mayDecideStage(ctx context.Context, entityID string, st ReviewStage) bool {
	if !a.canDecide(ctx, entityID) {
		return false
	}
	return a.deps.Review == nil || a.deps.Review.MayDecideStage(ctx, entityID, st.Group)
}

// stageLabel is "Stage n of m: Name", or "" for no stage and for the single default stage (today's look).
func stageLabel(st ReviewStage) string {
	if st.Total == 0 || (st.Total == 1 && st.Name == reviewstate.DefaultStageName) {
		return ""
	}
	return fmt.Sprintf("Stage %d of %d: %s", st.Index+1, st.Total, st.Name)
}

func (a *Admin) isRequester(r *http.Request, d ReviewDetail) bool {
	u := userFrom(r.Context())
	return u != nil && u.ID == d.Requester
}

func (a *Admin) handleReviewDetail(w http.ResponseWriter, r *http.Request) {
	d, ok := a.loadReview(w, r)
	if !ok {
		return
	}
	a.renderReviewDetail(w, r, http.StatusOK, d, "", "")
}

// reviewRows is what the change would do: name, address and each changed value.
func reviewRows(d ReviewDetail) ([]diffRow, int) {
	p, live := d.Payload, d.Live
	if p == nil || live == nil {
		return nil, 0
	}
	var rows []diffRow
	if p.Name != live.Name {
		rows = append(rows, diffRow{"name", "changed", live.Name, p.Name})
	}
	if p.Slug != live.Slug {
		rows = append(rows, diffRow{"slug", "changed", live.Slug, p.Slug})
	}
	attrs, same := diffAttributes(live.Attributes, p.Attributes)
	for _, row := range attrs {
		// An editor that writes an empty field the entry never had changes nothing a reviewer can see.
		if strings.Trim(row.Left+row.Right, `"`) == "" {
			same++
			continue
		}
		rows = append(rows, row)
	}
	return rows, same
}

func (a *Admin) renderReviewDetail(w http.ResponseWriter, r *http.Request, status int, d ReviewDetail, comment, errText string) {
	mine := a.isRequester(r, d)
	decider := !mine && a.mayDecideStage(r.Context(), d.EntityID, d.Stage)
	p := reviewDetailPage{
		ID: d.ID, Title: d.EntityName, Comment: comment, Error: errText,
		URL:      a.reviewURL("/", strconv.FormatInt(d.ID, 10)),
		Withdraw: mine,
		Discard:  !mine && a.canDecide(r.Context(), d.EntityID),
	}
	loc := a.siteLocation(r.Context())
	who := d.RequesterName
	if who == "" {
		who = "Someone"
	}
	verb := "change"
	if d.Payload != nil && (d.Payload.Status == entity.StatusPublished || d.Payload.Status == entity.StatusScheduled) && d.Live != nil && d.Live.Status == entity.StatusDraft {
		verb = "publish"
	}
	p.Intro = who + " asked to " + verb + " this entry on " + fmtDateTime(d.Since.In(loc)) + "."
	p.Badge = ui.Badge{Label: "Waiting for review", Kind: "warning"}
	p.Rows, p.Same = reviewRows(d)
	if d.Live == nil {
		p.Callout = append(p.Callout, reviewCallout{"warning", "This entry was deleted after the change was sent for review."})
	}
	switch {
	case d.Rejected:
		p.Badge = ui.Badge{Label: "Sent back", Kind: "muted"}
		by := "Sent back"
		if d.Reviewer != "" {
			by += " by " + d.Reviewer
		}
		p.Callout = append(p.Callout, reviewCallout{"warning", by + ": " + d.Comment})
	case d.Stale:
		p.Callout = append(p.Callout, reviewCallout{"warning", "This entry changed after it was sent for review. Send it back so it can be redone."})
	}
	if !d.Rejected {
		if label := stageLabel(d.Stage); label != "" {
			body := label + "."
			if d.Stage.Group != reviewstate.DefaultGroup && d.NeedsGroup == "" {
				body += " Waiting for: group " + d.Stage.Group + "."
			}
			p.Callout = append(p.Callout, reviewCallout{"info", body})
		}
		switch {
		case d.NeedsGroup != "":
			p.Callout = append(p.Callout, reviewCallout{"info", "This stage needs the group " + d.NeedsGroup + ", which this site does not define. An owner can discard the change."})
		case d.Approved:
			p.Callout = append(p.Callout, reviewCallout{"warning", "Approved. Publishing failed: try again."})
			p.Retry = decider
		default:
			p.Decide = decider
			p.CanApprove = decider && !d.Stale
		}
	}
	if !d.Rejected && d.Live != nil && a.deps.Growth.Changesets != nil && a.deps.Growth.ChangesetPreview != nil {
		p.Actions = []ui.Button{{Label: "Preview", Href: a.reviewURL("/", strconv.FormatInt(d.ID, 10), "/preview"), Icon: "eye"}}
	}
	v := a.NewView(w, r, "Review: "+d.EntityName)
	v.Width = "narrow"
	v.Crumbs = []Crumb{{Label: "Review", Href: a.reviewURL()}, {Label: d.EntityName}}
	v.Data = p
	a.Render(w, r, status, "review/detail", v)
}

// reviewDone is what a decision that went ahead tells the person.
type reviewDone struct {
	Kind, Flash string
	Queue       bool // go back to the queue; otherwise stay on the change
}

// reviewAction runs a decision on the change of the path. The decide right is
// checked first, before anything is read; deciding on one's own change is
// refused afterwards (ownOK lets a discard through: it is the owner's way to
// drop a change they sent). do is the service call.
func (a *Admin) reviewAction(w http.ResponseWriter, r *http.Request, ownOK bool, do func(rv ReviewFlow, id int64, comment string) (reviewDone, error)) {
	if !a.requireCan(w, r, authz.ActionEntityReviewDecide) {
		return
	}
	d, ok := a.loadReview(w, r)
	if !ok {
		return
	}
	if a.isRequester(r, d) && !ownOK {
		a.reviewRefused(w, r, d, "", ErrReviewSelf)
		return
	}
	_ = r.ParseForm()
	comment := strings.TrimSpace(r.PostFormValue("comment"))
	done, err := do(a.deps.Review, d.ID, comment)
	if err != nil {
		a.reviewRefused(w, r, d, comment, err)
		return
	}
	a.SetFlash(w, r, done.Kind, done.Flash)
	if done.Queue {
		Redirect(w, r, a.reviewURL())
		return
	}
	Redirect(w, r, a.reviewURL("/", strconv.FormatInt(d.ID, 10)))
}

// reviewApproved words what an approval did.
func reviewApproved(o ReviewOutcome) reviewDone {
	switch {
	case o.NotApplied:
		return reviewDone{"error", "Approved, but not applied: the entry changed at the same moment.", true}
	case o.Pending:
		return reviewDone{"success", "Approved. It waits for the next reviewer.", true}
	}
	return reviewDone{"success", "Approved. The change is live.", true}
}

func (a *Admin) handleReviewApprove(w http.ResponseWriter, r *http.Request) {
	a.reviewAction(w, r, false, func(rv ReviewFlow, id int64, comment string) (reviewDone, error) {
		o, err := rv.Approve(r.Context(), id, comment)
		return reviewApproved(o), err
	})
}

func (a *Admin) handleReviewRetryPublish(w http.ResponseWriter, r *http.Request) {
	a.reviewAction(w, r, false, func(rv ReviewFlow, id int64, _ string) (reviewDone, error) {
		o, err := rv.Publish(r.Context(), id)
		return reviewApproved(o), err
	})
}

func (a *Admin) handleReviewSendBack(w http.ResponseWriter, r *http.Request) {
	a.reviewAction(w, r, false, func(rv ReviewFlow, id int64, comment string) (reviewDone, error) {
		if comment == "" {
			return reviewDone{}, ErrReviewNoComment
		}
		return reviewDone{"success", "Sent back. The author sees your comment on the entry.", true}, rv.SendBack(r.Context(), id, comment)
	})
}

func (a *Admin) handleReviewDiscardEdit(w http.ResponseWriter, r *http.Request) {
	a.reviewAction(w, r, true, func(rv ReviewFlow, id int64, _ string) (reviewDone, error) {
		return reviewDone{"success", "Discarded. The change is gone.", true}, rv.Discard(r.Context(), id)
	})
}

// handleReviewWithdraw lets the person who sent a change take it back. Only the
// requester may: a decider who is not the requester discards instead.
func (a *Admin) handleReviewWithdraw(w http.ResponseWriter, r *http.Request) {
	d, ok := a.loadReview(w, r)
	if !ok {
		return
	}
	if !a.isRequester(r, d) {
		a.forbidden(w, r)
		return
	}
	if err := a.deps.Review.Withdraw(r.Context(), d.ID); err != nil {
		a.reviewRefused(w, r, d, "", err)
		return
	}
	a.SetFlash(w, r, "success", "Withdrawn. The change is gone.")
	Redirect(w, r, a.adminPath+"/entities/"+d.EntityID)
}

// reviewRefused answers a decision that did not go ahead.
func (a *Admin) reviewRefused(w http.ResponseWriter, r *http.Request, d ReviewDetail, comment string, err error) {
	switch {
	case errors.Is(err, ErrReviewSelf):
		a.ErrorBack(w, r, http.StatusUnprocessableEntity, "Not allowed", "You cannot approve your own change.", a.reviewURL("/", strconv.FormatInt(d.ID, 10)), "Back to the change")
	case errors.Is(err, ErrReviewNotDecider), errors.Is(err, ErrReviewNotRequester):
		a.forbidden(w, r)
	case errors.Is(err, ErrReviewNoComment):
		a.renderReviewDetail(w, r, http.StatusUnprocessableEntity, d, comment, "Add a comment so the author knows what to change.")
	case errors.Is(err, ErrReviewStale):
		d.Stale = true
		a.renderReviewDetail(w, r, http.StatusConflict, d, comment, "")
	case errors.Is(err, ErrReviewNotFound), errors.Is(err, ErrReviewNotStaged):
		a.ErrorBack(w, r, http.StatusConflict, "Not waiting for review", "This change is no longer waiting for a decision.", a.reviewURL(), "Back to review")
	default:
		a.logger().Error("review decision failed", "changeset", d.ID, "err", err)
		a.SetFlash(w, r, "error", "The decision could not be saved. Try again.")
		Redirect(w, r, a.reviewURL("/", strconv.FormatInt(d.ID, 10)))
	}
}
