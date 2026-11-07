package admin

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/form"
)

const (
	formsListWindowDays = 2 * submissionDaysShown // 30 days and the 30 before, for the change
	formsListMaxForms   = 20                      // forms the latest-submissions list reads
	formsListLatest     = 5
	formsListExcerpt    = 90
	notPlaced           = "Not placed"
)

// formsListData is the Forms list: the stat strip, the table of forms and the
// latest submissions across them.
type formsListData struct {
	Stats  []ui.Stat
	Table  ui.Table
	Latest ui.Table
	// HasForms is false on an empty site: the page shows the empty state only.
	HasForms bool
}

// formActivity is what one form's 60-day scan yields.
type formActivity struct {
	days          []int // per site-timezone day, oldest first, formsListWindowDays long
	spam30, total int
	capped, ok    bool
	subs          []*form.FormSubmission // newest first, spam left out
}

func (fa formActivity) sum(from, to int) int {
	n := 0
	for _, d := range fa.days[from:to] {
		n += d
	}
	return n
}

// buildFormsList reads each form's submissions once (a 60-day scan) and derives
// the 30-day count, the previous 30 days, the sparkline, spam caught and the
// newest rows from it. A form whose scan fails shows dashes and is left out of
// the totals; a capped scan shows a lower bound and no comparison or sparkline.
func (a *Admin) buildFormsList(ctx context.Context, fs FormStore, forms []*form.Form) formsListData {
	loc := a.siteLocation(ctx)
	now := time.Now().In(loc)
	since := now.AddDate(0, 0, -(formsListWindowDays - 1))
	since = time.Date(since.Year(), since.Month(), since.Day(), 0, 0, 0, 0, loc)

	var usage map[string][]entity.FormPlacement
	usageOK := false
	if fn := a.deps.Growth.FormUsage; fn != nil {
		var err error
		if usage, err = fn(ctx); err != nil {
			a.logger().Error("form usage failed", "err", err)
		} else {
			usageOK = true
		}
	}

	t := ui.Table{
		ID: "forms", Caption: "Forms", BaseURL: a.adminPath + "/forms", CSRF: csrfToken(ctx),
		Columns: []ui.Col{
			{Key: "name", Label: "Form"}, {Key: "status", Label: "Status"},
			{Key: "fields", Label: "Fields", Align: "right", WideOnly: true},
			{Key: "recent", Label: "Submissions, 30 days"}, {Key: "new", Label: "Unread"},
			{Key: "last", Label: "Last one", WideOnly: true}, {Key: "used", Label: "Used on"},
		},
	}
	var (
		cur, prev, spam, unreadTotal int64
		withUnread, active           int
		anyCapped                    bool
		trend                        = make([]int64, formSparkDays)
		latest                       []latestRow
	)
	for i, f := range forms {
		id := f.ID.String()
		if f.Status == form.StatusActive {
			active++
		}
		fa := a.scanFormActivity(ctx, fs, f, since, now, loc)
		unread, unreadErr := a.formUnread(ctx, fs, f)
		if unreadErr == nil {
			unreadTotal += int64(unread)
			if unread > 0 {
				withUnread++
			}
		}
		last := "Never"
		if fa.ok {
			anyCapped = anyCapped || fa.capped
			cur += int64(fa.sum(submissionDaysShown, formsListWindowDays))
			prev += int64(fa.sum(0, submissionDaysShown))
			spam += int64(fa.spam30)
			for d := range trend {
				trend[d] += int64(fa.days[formsListWindowDays-formSparkDays+d])
			}
			if i < formsListMaxForms {
				for _, s := range fa.subs[:min(len(fa.subs), formsListLatest)] {
					latest = append(latest, latestRow{f, s})
				}
			}
			last = a.lastOne(ctx, fs, f, fa, now)
		}
		t.Rows = append(t.Rows, ui.Row{ID: id, Label: f.Name, Cells: []ui.Cell{
			{Text: f.Name, Link: a.adminPath + "/forms/design/" + id, Sub: f.Slug},
			{Badge: formStatusBadge(f.Status)},
			{Text: strconv.Itoa(len(f.Fields))},
			a.recentCell(fa),
			a.unreadCell(f, unread, unreadErr),
			{Text: last, Nowrap: true},
			a.usedOnCell(ctx, f, usage, usageOK),
		}, Actions: []ui.Action{
			{Label: "Submissions", Href: a.tabURL(f.ID, "", "", 1, "")},
			{Label: "Share and embed", Href: a.adminPath + "/forms/design/" + id + "/share"},
			{Label: "Edit", Href: a.adminPath + "/forms/design/" + id},
			{Label: "Delete", Href: a.adminPath + "/forms/delete/" + id, Post: a.adminPath + "/forms/delete/" + id, Danger: true, ConfirmLabel: "Delete form",
				Confirm: "The form " + f.Name + " and all of its submissions are deleted. Pages that embed it stop showing it."},
		}})
	}

	subsStat := ui.Stat{Label: "Submissions, 30 days", Value: fmtNum(cur)}
	if anyCapped {
		subsStat.Value += "+"
		subsStat.Note = "Counted from the newest " + fmtNum(submissionTrackerCap) + " per form"
	} else if ch := ui.StatChangeOf(cur, prev, fmtNum); ch != nil {
		subsStat.Change = ch
	}
	trendStat := ui.Stat{Label: "Last 14 days", Spark: ui.SparkOf(trend)}
	if trendStat.Spark == nil {
		trendStat.Value = "0"
	}
	d := formsListData{
		HasForms: len(forms) > 0,
		Table:    t,
		Stats: []ui.Stat{
			subsStat,
			{Label: "Unread", Value: fmtNum(unreadTotal), Note: "across " + strconv.Itoa(withUnread) + " " + plural(withUnread, "form", "forms")},
			{Label: "Spam caught", Value: fmtNum(spam), Note: "never reached your inbox"},
			{Label: "Active forms", Value: strconv.Itoa(active), Note: "of " + strconv.Itoa(len(forms))},
			trendStat,
		},
		Latest: a.latestTable(latest, now, loc),
	}
	return d
}

// scanFormActivity runs the one scan a form's row is built from.
func (a *Admin) scanFormActivity(ctx context.Context, fs FormStore, f *form.Form, since, now time.Time, loc *time.Location) formActivity {
	subs, spam, total, capped, err := scanRecentSubmissions(ctx, fs, f.ID, since, submissionTrackerCap)
	if err != nil {
		a.logger().Error("form activity failed", "form", f.ID.String(), "err", err)
		return formActivity{}
	}
	spamDays := submissionDays(spam, now, formsListWindowDays, loc)
	fa := formActivity{
		days: submissionDays(subs, now, formsListWindowDays, loc), total: total, capped: capped, ok: true, subs: subs,
	}
	for _, n := range spamDays[submissionDaysShown:] {
		fa.spam30 += n
	}
	return fa
}

// formUnread counts the form's unread submissions.
func (a *Admin) formUnread(ctx context.Context, fs FormStore, f *form.Form) (int, error) {
	_, n, err := fs.ListSubmissions(ctx, f.ID, &form.ListSubmissionsParams{Limit: 1, Status: form.SubmissionStatusNew})
	if err != nil {
		a.logger().Error("form new count failed", "form", f.ID.String(), "err", err)
	}
	return n, err
}

// lastOne words when the form last heard from a visitor. The scan covers 60
// days; a form quiet for longer asks the store for its newest row.
func (a *Admin) lastOne(ctx context.Context, fs FormStore, f *form.Form, fa formActivity, now time.Time) string {
	if len(fa.subs) > 0 {
		return relTime(now, fa.subs[0].CreatedAt)
	}
	if fa.total == 0 {
		return "Never"
	}
	rows, _, err := fs.ListSubmissions(ctx, f.ID, &form.ListSubmissionsParams{Limit: 1})
	if err != nil || len(rows) == 0 {
		return "—"
	}
	return relTime(now, rows[0].CreatedAt)
}

// recentCell is the 30-day count with its sparkline. A capped scan shows a
// lower bound without a spark, because days beyond the cap would read as a
// false drop-off; a failed scan shows a dash.
func (a *Admin) recentCell(fa formActivity) ui.Cell {
	if !fa.ok {
		return ui.Cell{Text: "—"}
	}
	n := fa.sum(submissionDaysShown, formsListWindowDays)
	if fa.capped {
		return ui.Cell{Text: strconv.Itoa(n) + "+"}
	}
	vals := make([]int64, submissionDaysShown)
	for i, d := range fa.days[submissionDaysShown:] {
		vals[i] = int64(d)
	}
	return ui.Cell{Text: strconv.Itoa(n), Spark: ui.SparkOf(vals)}
}

// unreadCell is a link to the form's unread submissions, plain 0 when none.
func (a *Admin) unreadCell(f *form.Form, n int, err error) ui.Cell {
	switch {
	case err != nil:
		return ui.Cell{Text: "—"}
	case n == 0:
		return ui.Cell{Text: "0"}
	}
	return ui.Cell{Text: strconv.Itoa(n) + " unread", Link: a.tabURL(f.ID, "", form.SubmissionStatusNew, 1, "")}
}

// usedOnCell counts where the form is placed, with the first place beneath.
func (a *Admin) usedOnCell(ctx context.Context, f *form.Form, usage map[string][]entity.FormPlacement, ok bool) ui.Cell {
	if !ok {
		return ui.Cell{Text: "—"}
	}
	ps := usage[f.Slug]
	if len(ps) == 0 {
		return ui.Cell{Text: notPlaced}
	}
	return ui.Cell{Text: strconv.Itoa(len(ps)) + " " + plural(len(ps), "place", "places"), Sub: a.firstPlace(ctx, ps)}
}

// firstPlace names one place of a form: the path of a published page, else an
// area (footer, a block), else a page's name.
func (a *Admin) firstPlace(ctx context.Context, ps []entity.FormPlacement) string {
	for _, p := range ps {
		if p.Kind == entity.PlacementPage {
			if path := a.placementPath(ctx, p); path != "" {
				return path
			}
		}
	}
	for _, p := range ps {
		if p.Kind == entity.PlacementArea {
			return strings.ToLower(p.Area)
		}
	}
	return ps[0].Name
}

// placementPath is the public path of a published page placement ("" for an
// area, an unpublished page, or when the path is unknown).
func (a *Admin) placementPath(ctx context.Context, p entity.FormPlacement) string {
	c := a.deps.Content
	if p.Kind != entity.PlacementPage || p.Status != string(entity.StatusPublished) || c.Entities == nil || c.PublicPath == nil {
		return ""
	}
	e, err := c.Entities.GetEntity(ctx, p.EntityID)
	if err != nil || e == nil {
		return ""
	}
	return c.PublicPath(ctx, e)
}

// latestRow is one submission with the form it came to.
type latestRow struct {
	form *form.Form
	sub  *form.FormSubmission
}

// latestTable lists the newest submissions across forms: who, which form, the
// message, and when. A row opens the submission in its form's Submissions tab.
func (a *Admin) latestTable(rows []latestRow, now time.Time, loc *time.Location) ui.Table {
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].sub.CreatedAt.After(rows[j].sub.CreatedAt) })
	rows = rows[:min(len(rows), formsListLatest)]
	t := ui.Table{
		ID: "forms-latest", Caption: "Latest submissions, all forms",
		Columns: []ui.Col{
			{Key: "from", Label: "From", Role: "title"}, {Key: "form", Label: "Form"},
			{Key: "message", Label: "Message", WideOnly: true}, {Key: "received", Label: "Received", Align: "right"},
		},
	}
	for _, r := range rows {
		from := submissionFrom(r.form, r.sub)
		sub := submissionEmail(r.form, r.sub)
		if sub == from {
			sub = ""
		}
		class := "hm-ss__row"
		if r.sub.Status == form.SubmissionStatusNew {
			class += " hm-ss__row--unread"
		}
		id := r.sub.ID.String()
		t.Rows = append(t.Rows, ui.Row{ID: id, Label: "submission from " + from, Class: class, Cells: []ui.Cell{
			{Text: from, Link: a.tabURL(r.form.ID, "", "", 1, id), Sub: sub},
			{Text: r.form.Name},
			{Text: submissionExcerpt(r.form, r.sub), Clip: true},
			{Text: relTime(now, r.sub.CreatedAt), Title: fmtDateTime(r.sub.CreatedAt.In(loc)), Nowrap: true},
		}})
	}
	return t
}

// submissionExcerpt is the start of the first filled multi-line answer.
func submissionExcerpt(f *form.Form, s *form.FormSubmission) string {
	for _, ff := range f.Fields {
		if ff.Type != form.FieldTypeTextarea {
			continue
		}
		if v := strings.Join(strings.Fields(formatAnswer(s.Data[ff.Name])), " "); v != "" {
			return truncate(v, formsListExcerpt)
		}
	}
	return ""
}
