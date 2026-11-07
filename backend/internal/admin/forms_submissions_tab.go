package admin

import (
	"context"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/analytics"
	"hatamoto.dev/core/internal/form"
)

// submissionsTabData is the Submissions tab of the form designer: the stat
// cards and tracker, the filtered table, and the drawer of the open submission.
type submissionsTabData struct {
	formDesignData
	Stats     []ui.Stat
	Tracker   ui.Tracker
	Filter    ui.Segmented
	Q, Status string
	ExportURL string
	Table     ui.Table
	Drawer    *submissionDrawer // nil when no submission is open
}

// submissionDrawer is the side panel of one submission.
type submissionDrawer struct {
	ID, Title, Subtitle, ReplyURL string
	Answers                       []drawerAnswer
	Details                       []drawerDetail
	Status, CSRF                  string
	PrevURL, NextURL              string
	ShowWebhooks                  bool
}

type drawerAnswer struct{ Label, Value string }

// drawerDetail is one row of the drawer's Details card. Exact is the full
// timestamp behind a relative Value ("" when Value is already exact).
type drawerDetail struct{ Label, Value, Exact string }

const (
	notRecorded  = "Not recorded"
	notAvailable = "Not available"
)

// relTime words how long ago t was, relative to now: "just now", "7h ago",
// "yesterday", "3 days ago". Past a month it falls back to the date.
func relTime(now, t time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmtNum(int64(d/time.Minute)) + "m ago"
	case d < 24*time.Hour:
		return fmtNum(int64(d/time.Hour)) + "h ago"
	case d < 48*time.Hour:
		return "yesterday"
	case d < 30*24*time.Hour:
		return fmtNum(int64(d/(24*time.Hour))) + " days ago"
	}
	return t.Format("2 Jan 2006")
}

// choiceColumns are the first two choice fields (select or radio) of the form:
// the table shows their answers as columns.
func choiceColumns(f *form.Form) []form.FormField {
	var out []form.FormField
	for _, ff := range f.Fields {
		if ff.Type == form.FieldTypeSelect || ff.Type == form.FieldTypeRadio {
			out = append(out, ff)
			if len(out) == 2 {
				break
			}
		}
	}
	return out
}

// pagePath is the path of a stored page URL ("" when it has none).
func pagePath(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if u.Path == "" {
		return "/"
	}
	return u.Path
}

// formPages are the pages the form is known to sit on: the paths its recent
// submissions came from, and its own endpoint page. The submissions-share-list
// work replaces this with the form's real placements.
func (a *Admin) formPages(_ context.Context, f *form.Form, subs []*form.FormSubmission) []string {
	seen := map[string]bool{"/f/" + f.Slug: true}
	for _, s := range subs {
		if p := pagePath(s.Metadata.PageURL); p != "" {
			seen[p] = true
		}
	}
	pages := make([]string, 0, len(seen))
	for p := range seen {
		pages = append(pages, p)
	}
	sort.Strings(pages)
	return pages
}

// formConversion is the Conversion card: submissions from the form's pages
// over the visits those pages had. Visits are page visits, not form views.
func (a *Admin) formConversion(ctx context.Context, f *form.Form, subs []*form.FormSubmission, from, to time.Time) ui.Stat {
	st := ui.Stat{Label: "Conversion", Value: notAvailable}
	if a.deps.Growth.Traffic == nil {
		return st
	}
	pages := a.formPages(ctx, f, subs)
	on := make(map[string]bool, len(pages))
	for _, p := range pages {
		on[p] = true
	}
	top, err := a.deps.Growth.Traffic.TrafficTop(ctx, analytics.DimLanding, from, to, 0)
	if err != nil {
		a.logger().Error("form conversion traffic failed", "form", f.ID.String(), "err", err)
		return st
	}
	var visits int64
	for _, t := range top {
		if on[t.K1] {
			visits += t.Visits
		}
	}
	if visits == 0 {
		return st
	}
	var n int64
	for _, s := range subs {
		if on[pagePath(s.Metadata.PageURL)] {
			n++
		}
	}
	st.Value = strconv.FormatFloat(float64(n)/float64(visits)*100, 'f', 1, 64) + "%"
	st.Note = fmtNum(visits) + " page " + plural(int(visits), "visit", "visits")
	return st
}

// submissionsTabOverview builds the four stat cards and the 30-day tracker.
func (a *Admin) submissionsTabOverview(ctx context.Context, fs FormStore, f *form.Form) ([]ui.Stat, ui.Tracker, error) {
	_, tracker, err := a.submissionOverview(ctx, fs, f)
	if err != nil {
		return nil, ui.Tracker{}, err
	}
	now := time.Now()
	cur := now.AddDate(0, 0, -submissionDaysShown)
	prev := now.AddDate(0, 0, -2*submissionDaysShown)
	recent, spam, _, capped, err := scanRecentSubmissions(ctx, fs, f.ID, prev, submissionTrackerCap)
	if err != nil {
		return nil, ui.Tracker{}, err
	}
	var inCur, inPrev []*form.FormSubmission
	for _, s := range recent {
		if s.CreatedAt.Before(cur) {
			inPrev = append(inPrev, s)
		} else {
			inCur = append(inCur, s)
		}
	}
	spamCur := 0
	for _, s := range spam {
		if !s.CreatedAt.Before(cur) {
			spamCur++
		}
	}
	_, unread, err := fs.ListSubmissions(ctx, f.ID, &form.ListSubmissionsParams{Status: form.SubmissionStatusNew, Limit: 1})
	if err != nil {
		return nil, ui.Tracker{}, err
	}
	subsStat := ui.Stat{Label: "Submissions", Value: fmtNum(int64(len(inCur))), Note: "Last 30 days"}
	if !capped {
		subsStat.Change = ui.StatChangeOf(int64(len(inCur)), int64(len(inPrev)), fmtNum)
	}
	return []ui.Stat{
		subsStat,
		{Label: "Unread", Value: fmtNum(int64(unread))},
		a.formConversion(ctx, f, inCur, cur, now),
		{Label: "Spam", Value: fmtNum(int64(spamCur)), Note: "Last 30 days"},
	}, tracker, nil
}

// submissionFrom is who sent a submission: the first text answer, else the
// email address, else "Anonymous".
func submissionFrom(f *form.Form, s *form.FormSubmission) string {
	for _, ff := range f.Fields {
		if ff.Type == form.FieldTypeText {
			if v := strings.TrimSpace(formatAnswer(s.Data[ff.Name])); v != "" {
				return v
			}
		}
	}
	if e := submissionEmail(f, s); e != "" {
		return e
	}
	return "Anonymous"
}

// submissionEmail is the first email answer of a submission.
func submissionEmail(f *form.Form, s *form.FormSubmission) string {
	for _, ff := range f.Fields {
		if ff.Type == form.FieldTypeEmail {
			if v := strings.TrimSpace(formatAnswer(s.Data[ff.Name])); v != "" {
				return v
			}
		}
	}
	return ""
}

// submissionsTabColumns are the table columns: From, Email, up to two choice
// fields, Page, Received.
func submissionsTabColumns(f *form.Form) []ui.Col {
	cols := []ui.Col{{Key: "from", Label: "From", Role: "title"}, {Key: "email", Label: "Email"}}
	for _, ff := range choiceColumns(f) {
		cols = append(cols, ui.Col{Key: "f_" + ff.Name, Label: ff.Label})
	}
	return append(cols, ui.Col{Key: "page", Label: "Page"}, ui.Col{Key: "received", Label: "Received"})
}

// submissionsTabRow is one table row; openURL opens the submission's drawer.
func submissionsTabRow(f *form.Form, s *form.FormSubmission, now time.Time, openURL string) ui.Row {
	from := submissionFrom(f, s)
	cells := []ui.Cell{{Text: from, Link: openURL}, {Text: submissionEmail(f, s), Clip: true}}
	if s.Status == form.SubmissionStatusNew {
		cells[0].Tag = &ui.Badge{Label: "New", Kind: "info"}
	}
	for _, ff := range choiceColumns(f) {
		cells = append(cells, ui.Cell{Text: formatAnswer(s.Data[ff.Name])})
	}
	cells = append(cells, ui.Cell{Text: pagePath(s.Metadata.PageURL), Mono: true, Clip: true}, ui.Cell{Text: relTime(now, s.CreatedAt), Nowrap: true})
	return ui.Row{ID: s.ID.String(), Label: "submission from " + from, Cells: cells}
}

// buildSubmissionDrawer lays one submission out for the drawer. Answers follow
// the form's fields, then answers whose field was deleted, by name; a missing
// Data map or empty metadata yields fewer rows, never a panic.
func (a *Admin) buildSubmissionDrawer(ctx context.Context, f *form.Form, s *form.FormSubmission, now time.Time, csrf string) *submissionDrawer {
	loc := a.siteLocation(ctx)
	d := &submissionDrawer{
		ID: s.ID.String(), Title: submissionFrom(f, s), Subtitle: relTime(now, s.CreatedAt),
		Status: s.Status, CSRF: csrf,
	}
	if e := submissionEmail(f, s); e != "" {
		d.ReplyURL = "mailto:" + e
	}
	used := map[string]bool{}
	for _, ff := range f.Fields {
		used[ff.Name] = true
		if ff.IsLayout() {
			continue
		}
		if v, ok := s.Data[ff.Name]; ok {
			d.Answers = append(d.Answers, drawerAnswer{ff.Label, formatAnswer(v)})
		}
	}
	keys := make([]string, 0, len(s.Data))
	for k := range s.Data {
		if !used[k] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		d.Answers = append(d.Answers, drawerAnswer{k, formatAnswer(s.Data[k])})
	}
	d.Details = []drawerDetail{{Label: "Received", Value: relTime(now, s.CreatedAt), Exact: fmtDateTime(s.CreatedAt.In(loc))}}
	if p := pagePath(s.Metadata.PageURL); p != "" {
		d.Details = append(d.Details, drawerDetail{Label: "Page", Value: p})
	}
	if s.Metadata.Referrer != "" {
		d.Details = append(d.Details, drawerDetail{Label: "Referrer", Value: s.Metadata.Referrer})
	}
	if s.Metadata.UserAgent != "" {
		d.Details = append(d.Details, drawerDetail{Label: "Device", Value: analytics.DeviceClass(s.Metadata.UserAgent)})
	}
	d.Details = append(d.Details,
		drawerDetail{Label: "Location", Value: notRecorded},
		drawerDetail{Label: "Notifications go to", Value: notifyRecipients(f)})
	return d
}

// notifyRecipients is where the form's notification email goes.
func notifyRecipients(f *form.Form) string {
	if f.Settings.NotifyEmail != nil {
		if e := strings.TrimSpace(*f.Settings.NotifyEmail); e != "" {
			return e
		}
	}
	return "the site address"
}
