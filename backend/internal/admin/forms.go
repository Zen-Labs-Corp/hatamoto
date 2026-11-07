package admin

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/mail"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/csvx"
	"hatamoto.dev/core/internal/form"
	"hatamoto.dev/core/internal/httpx"
)

const (
	submissionsPerPage = 25
	exportPageSize     = 500
	exportMaxRows      = 200000
)

// FormMailer forwards a submission by email (the "Email this" action).
type FormMailer interface {
	Configured() bool
	Forward(ctx context.Context, f *form.Form, sub *form.FormSubmission, to []string, by string) error
}

func (a *Admin) mountForms(authed *httpx.Group) {
	authed.HandleFunc("GET /forms", a.handleForms)
	authed.HandleFunc("GET /forms/new", a.handleFormNew)
	authed.HandleFunc("POST /forms", a.handleFormCreate)
	authed.HandleFunc("GET /forms/design/{id}", a.handleFormDesign)
	authed.HandleFunc("GET /forms/edit/{id}", a.handleFormEditRedirect)
	authed.HandleFunc("PUT /forms/{id}/definition", a.handleFormDefinition)
	a.mountFormTabs(authed)
	authed.HandleFunc("GET /forms/delete/{id}", a.handleFormDeleteForm)
	authed.HandleFunc("POST /forms/delete/{id}", a.handleFormDelete)
	// The list, export and bulk routes take the form as ?form={id}; a single
	// submission knows its form.
	authed.HandleFunc("GET /forms/submissions", a.handleSubmissions)
	authed.HandleFunc("GET /forms/submissions/export.csv", a.handleSubmissionsExport)
	authed.HandleFunc("POST /forms/submissions/bulk", a.handleSubmissionsBulk)
	authed.HandleFunc("GET /forms/submissions/{sid}", a.handleSubmission)
	authed.HandleFunc("POST /forms/submissions/{sid}/status", a.handleSubmissionStatus)
	authed.HandleFunc("POST /forms/submissions/{sid}/email", a.handleSubmissionEmail)
	authed.HandleFunc("GET /forms/submissions/{sid}/delete", a.handleSubmissionDeleteForm)
	authed.HandleFunc("POST /forms/submissions/{sid}/delete", a.handleSubmissionDelete)
}

// formsTabs are the tabs across the top of the Forms pages. Spam is owner-only,
// like its route. active is "forms", "submissions" or "spam".
func (a *Admin) formsTabs(role, active string) []ui.Tab {
	tabs := []ui.Tab{
		{Label: "Forms", Href: a.adminPath + "/forms", Active: active == "forms"},
		{Label: "Submissions", Href: a.adminPath + "/forms/submissions", Active: active == "submissions"},
	}
	if role == "owner" {
		tabs = append(tabs, ui.Tab{Label: "Spam", Href: a.adminPath + "/forms/spam", Active: active == "spam"})
	}
	return tabs
}

// formsTabsFor is formsTabs for the signed-in user of v.
func (a *Admin) formsTabsFor(v *View, active string) []ui.Tab {
	role := ""
	if v.User != nil {
		role = v.User.Role
	}
	return a.formsTabs(role, active)
}

func (a *Admin) formStore(w http.ResponseWriter, r *http.Request) (FormStore, bool) {
	if a.deps.Growth.Forms == nil {
		a.unavailable(w, r, "Forms")
		return nil, false
	}
	return a.deps.Growth.Forms, true
}

func (a *Admin) loadForm(w http.ResponseWriter, r *http.Request) (*form.Form, bool) {
	return a.loadFormID(w, r, r.PathValue("id"))
}

// loadFormQuery loads the form named by ?form=, for the submission screens.
func (a *Admin) loadFormQuery(w http.ResponseWriter, r *http.Request) (*form.Form, bool) {
	return a.loadFormID(w, r, r.URL.Query().Get("form"))
}

func (a *Admin) loadFormID(w http.ResponseWriter, r *http.Request, raw string) (*form.Form, bool) {
	fs, ok := a.formStore(w, r)
	if !ok {
		return nil, false
	}
	id, err := uuid.Parse(raw)
	if err == nil {
		var f *form.Form
		if f, err = fs.GetForm(r.Context(), id); err == nil {
			return f, true
		}
		if !errors.Is(err, form.ErrFormNotFound) {
			a.logger().Error("load form failed", "err", err)
			a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The form could not be loaded.")
			return nil, false
		}
	}
	a.ErrorBack(w, r, http.StatusNotFound, "Form not found", "That form does not exist.", a.adminPath+"/forms", "Back to forms")
	return nil, false
}

// ---- list, create, delete ----

func formStatusBadge(status string) *ui.Badge {
	switch status {
	case form.StatusActive:
		return &ui.Badge{Label: "Active", Kind: "success"}
	case form.StatusInactive:
		return &ui.Badge{Label: "Paused", Kind: "warning"}
	}
	return &ui.Badge{Label: "Archived", Kind: "neutral"}
}

const formSparkDays = 14 // days of the list's trend card

func (a *Admin) handleForms(w http.ResponseWriter, r *http.Request) {
	fs, ok := a.formStore(w, r)
	if !ok {
		return
	}
	forms, err := fs.ListForms(r.Context(), "")
	if err != nil {
		a.logger().Error("list forms failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The forms could not be loaded.")
		return
	}
	d := a.buildFormsList(r.Context(), fs, forms)
	d.Table.Empty = ui.Empty{Icon: "inbox", Title: "No forms yet", Body: "A form collects enquiries from your pages.", Actions: []ui.Button{{Label: "New form", Href: a.adminPath + "/forms/new", Variant: "primary", Attrs: map[string]string{"data-hm-open": "new-form"}}}}
	v := a.NewView(w, r, "Forms")
	actions := []ui.Button{}
	if v.User != nil && v.User.Role == "owner" {
		actions = append(actions, ui.Button{Label: "Spam protection", Href: a.adminPath + "/forms/spam", Icon: "shield-check"})
	}
	actions = append(actions, ui.Button{Label: "New form", Href: a.adminPath + "/forms/new", Variant: "primary", Icon: "plus", Attrs: map[string]string{"data-hm-open": "new-form"}})
	choices := templateChoices("")
	v.Data = struct {
		formsListData
		Actions       []ui.Button
		TemplateItems []ui.RadioCardItem
	}{d, actions, radioItems(choices)}
	a.Render(w, r, http.StatusOK, "forms/list", v)
}

type newFormData struct {
	Name, Slug, Description string
	Template                string
	Templates               []tplChoice
	TemplateItems           []ui.RadioCardItem
	Errors                  map[string]string
	Summary                 []ui.FieldError
}

// tplChoice is one template radio card on the new-form page.
type tplChoice struct {
	Key, Name, Blurb string
	Checked          bool
}

// radioItems turns the template choices into hm/radiocard items.
func radioItems(cs []tplChoice) []ui.RadioCardItem {
	out := make([]ui.RadioCardItem, 0, len(cs))
	for _, c := range cs {
		out = append(out, ui.RadioCardItem{Value: c.Key, Title: c.Name, Description: c.Blurb, Checked: c.Checked})
	}
	return out
}

// templateChoices lists the form templates with the one named by selected checked
// (blank when none is named).
func templateChoices(selected string) []tplChoice {
	if selected == "" {
		selected = "blank"
	}
	var out []tplChoice
	for _, t := range form.Templates() {
		out = append(out, tplChoice{Key: t.Key, Name: t.Name, Blurb: t.Blurb, Checked: t.Key == selected})
	}
	return out
}

func (a *Admin) renderNewForm(w http.ResponseWriter, r *http.Request, status int, d newFormData) {
	for _, k := range []struct{ id, label string }{{"name", "Name"}, {"slug", "Identifier"}, {"template", "Template"}} {
		if m := d.Errors[k.id]; m != "" {
			d.Summary = append(d.Summary, ui.FieldError{ID: k.id, Label: k.label, Message: m})
		}
	}
	d.Templates = templateChoices(d.Template)
	d.TemplateItems = radioItems(d.Templates)
	v := a.NewView(w, r, "New form")
	v.Crumbs = []Crumb{{Label: "Forms", Href: a.adminPath + "/forms"}, {Label: "New form"}}
	v.Data = d
	v.Width = "narrow"
	a.Render(w, r, status, "forms/new", v)
}

func (a *Admin) handleFormNew(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.formStore(w, r); !ok {
		return
	}
	a.renderNewForm(w, r, http.StatusOK, newFormData{})
}

func (a *Admin) handleFormCreate(w http.ResponseWriter, r *http.Request) {
	fs, ok := a.formStore(w, r)
	if !ok {
		return
	}
	d := newFormData{Name: strings.TrimSpace(r.PostFormValue("name")), Slug: strings.TrimSpace(r.PostFormValue("slug")), Description: strings.TrimSpace(r.PostFormValue("description")), Template: strings.TrimSpace(r.PostFormValue("template")), Errors: map[string]string{}}
	if d.Template == "" {
		d.Template = "blank"
	}
	tpl, tplOK := form.TemplateByKey(d.Template)
	if !tplOK {
		d.Errors["template"] = "Choose one of the templates"
	}
	if d.Name == "" && tplOK && tpl.Key != "blank" {
		d.Name = tpl.Name
	}
	if d.Name == "" {
		d.Errors["name"] = "Enter a name"
	} else if utf8.RuneCountInString(d.Name) > 120 {
		d.Errors["name"] = "Use at most 120 characters"
	}
	if d.Slug != "" && !slugRe.MatchString(d.Slug) {
		d.Errors["slug"] = "Use lowercase letters, numbers and dashes"
	}
	if utf8.RuneCountInString(d.Description) > 500 {
		d.Errors["description"] = "Use at most 500 characters"
	}
	if len(d.Errors) > 0 {
		a.renderNewForm(w, r, http.StatusUnprocessableEntity, d)
		return
	}
	built := tpl.Build()
	in := &built
	in.Name, in.Slug, in.Status = d.Name, d.Slug, form.StatusInactive
	if d.Description != "" {
		in.Description = &d.Description
	}
	f, err := fs.CreateForm(r.Context(), in)
	switch {
	case errors.Is(err, form.ErrSlugAlreadyExists):
		d.Errors["slug"] = "A form with this identifier already exists"
		a.renderNewForm(w, r, http.StatusUnprocessableEntity, d)
		return
	case err != nil:
		a.logger().Error("create form failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The form could not be created.")
		return
	}
	a.invalidate(r.Context())
	a.SetFlash(w, r, "success", "Form created. It goes live when you set it to Active.")
	Redirect(w, r, a.adminPath+"/forms/design/"+f.ID.String())
}

var slugRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

func (a *Admin) handleFormDeleteForm(w http.ResponseWriter, r *http.Request) {
	f, ok := a.loadForm(w, r)
	if !ok {
		return
	}
	a.renderConfirm(w, r, confirmData{
		Title: "Delete the form " + f.Name + "?", Message: "The form and all of its submissions are deleted. Pages that embed it stop showing it.",
		Post: a.adminPath + "/forms/delete/" + f.ID.String(), Label: "Delete form", Back: a.adminPath + "/forms", Danger: true,
	})
}

func (a *Admin) handleFormDelete(w http.ResponseWriter, r *http.Request) {
	f, ok := a.loadForm(w, r)
	if !ok {
		return
	}
	if err := a.deps.Growth.Forms.DeleteForm(r.Context(), f.ID); err != nil && !errors.Is(err, form.ErrFormNotFound) {
		a.logger().Error("delete form failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The form could not be deleted.")
		return
	}
	a.invalidate(r.Context())
	a.SetFlash(w, r, "success", "Form deleted.")
	Redirect(w, r, a.adminPath+"/forms")
}

// ---- the builder ----

var fieldTypeLabels = map[string]string{
	form.FieldTypeText: "Single-line text", form.FieldTypeEmail: "Email address", form.FieldTypePhone: "Phone number",
	form.FieldTypeTextarea: "Multi-line text", form.FieldTypeSelect: "Drop-down list", form.FieldTypeRadio: "Choose one (radio buttons)",
	form.FieldTypeCheckbox: "Choose several (checkboxes)", form.FieldTypeDate: "Date", form.FieldTypeNumber: "Number", form.FieldTypeURL: "Web address",
	form.FieldTypeHeading: "Heading", form.FieldTypeParagraph: "Paragraph of text", form.FieldTypeDivider: "Divider",
	form.FieldTypeHidden: "Hidden value", form.FieldTypeConsent: "Consent checkbox", form.FieldTypeFile: "File upload",
}

// fieldTypeIcon is the icon shown for a field type in the builder's row summary.
func fieldTypeIcon(t string) string {
	switch t {
	case form.FieldTypeEmail:
		return "mail"
	case form.FieldTypePhone:
		return "smartphone"
	case form.FieldTypeTextarea:
		return "file-text"
	case form.FieldTypeSelect:
		return "chevron-down"
	case form.FieldTypeRadio:
		return "list"
	case form.FieldTypeCheckbox:
		return "check-circle"
	case form.FieldTypeDate:
		return "calendar"
	case form.FieldTypeNumber:
		return "list-ordered"
	case form.FieldTypeURL:
		return "link"
	case form.FieldTypeHeading:
		return "heading"
	case form.FieldTypeParagraph:
		return "align-left"
	case form.FieldTypeDivider:
		return "minus"
	case form.FieldTypeHidden:
		return "eye-off"
	case form.FieldTypeConsent:
		return "shield-check"
	case form.FieldTypeFile:
		return "upload"
	}
	return "pen"
}

// formEndpoint is where visitors send the form: under the configured public
// API prefix when there is one, else under /api/v1/public.
func (a *Admin) formEndpoint(slug string) string {
	prefix := a.deps.PublicAPIPrefix
	if prefix == "" {
		prefix = "/api/v1/public"
	}
	return prefix + "/forms/" + slug + "/submit"
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

func validEmailList(s string) bool {
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if addr, err := mail.ParseAddress(p); err != nil || addr.Address != p {
			return false
		}
	}
	return true
}

// ---- submissions ----

// formatAnswer renders a stored answer as text.
func formatAnswer(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		if x {
			return "Yes"
		}
		return "No"
	case []any:
		parts := make([]string, len(x))
		for i, it := range x {
			parts[i] = fmt.Sprint(it)
		}
		return strings.Join(parts, ", ")
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	}
	return fmt.Sprint(v)
}

func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "…"
}

// submissionsForm picks the form whose submissions the page lists: ?form=, or
// the first form when none is named. ok is false when a response was written;
// a nil form with ok true means the site has no forms yet.
func (a *Admin) submissionsForm(w http.ResponseWriter, r *http.Request, forms []*form.Form) (*form.Form, bool) {
	if r.URL.Query().Get("form") != "" {
		return a.loadFormQuery(w, r)
	}
	if len(forms) == 0 {
		return nil, true
	}
	return forms[0], true
}

const (
	submissionDaysShown  = 30
	submissionScanPage   = 200
	submissionTrackerCap = 5000
)

// submissionDays counts submissions per site-timezone day for the last n days,
// oldest first; the last element is the day of now. Submissions outside the
// window are ignored.
func submissionDays(subs []*form.FormSubmission, now time.Time, n int, loc *time.Location) []int {
	counts := make([]int, n)
	day := func(t time.Time) time.Time {
		y, m, d := t.In(loc).Date()
		return time.Date(y, m, d, 12, 0, 0, 0, time.UTC) // noon UTC: whole-day arithmetic without DST edges
	}
	today := day(now)
	for _, s := range subs {
		ago := int(math.Round(today.Sub(day(s.CreatedAt)).Hours() / 24))
		if ago >= 0 && ago < n {
			counts[n-1-ago]++
		}
	}
	return counts
}

// loadRecentSubmissions pages through a form's submissions, newest first, until
// it passes since or has read limit rows; capped is true when it stopped on the limit.
// Spam is left out of the returned activity but counts toward limit. total is the
// form's submission count, taken from the first page so callers need no second query.
func loadRecentSubmissions(ctx context.Context, fs FormStore, formID uuid.UUID, since time.Time, limit int) (subs []*form.FormSubmission, total int, capped bool, err error) {
	subs, _, total, capped, err = scanRecentSubmissions(ctx, fs, formID, since, limit)
	return subs, total, capped, err
}

// scanRecentSubmissions is loadRecentSubmissions that also returns the spam it
// passed, newest first, for callers that count what the filter caught.
func scanRecentSubmissions(ctx context.Context, fs FormStore, formID uuid.UUID, since time.Time, limit int) (subs, spam []*form.FormSubmission, total int, capped bool, err error) {
	var after *form.SubmissionCursor
	read := 0
	for first := true; read < limit; first = false {
		page, n, err := fs.ListSubmissions(ctx, formID, &form.ListSubmissionsParams{Limit: submissionScanPage, After: after})
		if err != nil {
			return nil, nil, 0, false, err
		}
		if first {
			total = n
		}
		for _, s := range page {
			if s.CreatedAt.Before(since) {
				return subs, spam, total, false, nil
			}
			read++
			if s.Status == form.SubmissionStatusSpam {
				spam = append(spam, s)
			} else {
				subs = append(subs, s)
			}
		}
		if len(page) < submissionScanPage {
			return subs, spam, total, false, nil
		}
		last := page[len(page)-1]
		after = &form.SubmissionCursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}
	return subs, spam, total, true, nil
}

// submissionOverview builds the stat cards and the 30-day tracker of one form.
func (a *Admin) submissionOverview(ctx context.Context, fs FormStore, f *form.Form) ([]ui.Stat, ui.Tracker, error) {
	loc := a.siteLocation(ctx)
	now := time.Now().In(loc)
	since := now.AddDate(0, 0, -(submissionDaysShown - 1))
	since = time.Date(since.Year(), since.Month(), since.Day(), 0, 0, 0, 0, loc)
	recent, all, capped, err := loadRecentSubmissions(ctx, fs, f.ID, since, submissionTrackerCap)
	if err != nil {
		return nil, ui.Tracker{}, err
	}
	_, fresh, err := fs.ListSubmissions(ctx, f.ID, &form.ListSubmissionsParams{Status: form.SubmissionStatusNew, Limit: 1})
	if err != nil {
		return nil, ui.Tracker{}, err
	}
	days := submissionDays(recent, now, submissionDaysShown, loc)
	var last7, prev7 int64
	for i, n := range days {
		switch {
		case i >= submissionDaysShown-7:
			last7 += int64(n)
		case i >= submissionDaysShown-14:
			prev7 += int64(n)
		}
	}
	stats := []ui.Stat{
		{Label: "Total", Value: fmtNum(int64(all))},
		{Label: "New", Value: fmtNum(int64(fresh))},
		{Label: "Last 7 days", Value: fmtNum(last7), Change: ui.StatChangeOf(last7, prev7, fmtNum)},
	}
	if capped {
		stats[2].Change = nil // the older week is cut off, so a comparison would overstate the rise
	}
	label := "Submissions per day, last 30 days"
	if capped {
		label += " (counted from the newest " + fmtNum(submissionTrackerCap) + "+)"
	}
	busiest := 0
	for _, n := range days {
		busiest = max(busiest, n)
	}
	t := ui.Tracker{Label: label}
	for i, n := range days {
		d := now.AddDate(0, 0, -(submissionDaysShown - 1 - i))
		blk := ui.TrackerBlock{Kind: "neutral", Tip: d.Format("2 Jan") + ": no submissions"}
		if n > 0 {
			kind, tip := "success", d.Format("2 Jan")+": "+fmtNum(int64(n))+" "+plural(n, "submission", "submissions")
			if n*5 >= busiest*4 { // the busiest days (within a fifth of the peak) stand out in the accent, and say so
				kind, tip = "info", tip+", busiest"
			}
			blk = ui.TrackerBlock{Kind: kind, Tip: tip}
		}
		t.Blocks = append(t.Blocks, blk)
	}
	return stats, t, nil
}

// handleSubmissions is the old submissions list: it sends the browser to the
// Submissions tab of the form ?form= names (the first form when none is), keeping
// the search, status and page; a site with no forms goes to the Forms list.
func (a *Admin) handleSubmissions(w http.ResponseWriter, r *http.Request) {
	fs, ok := a.formStore(w, r)
	if !ok {
		return
	}
	forms, err := fs.ListForms(r.Context(), "")
	if err != nil {
		a.logger().Error("list forms failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The forms could not be loaded.")
		return
	}
	f, ok := a.submissionsForm(w, r, forms)
	if !ok {
		return
	}
	if f == nil {
		http.Redirect(w, r, a.adminPath+"/forms", http.StatusFound)
		return
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	http.Redirect(w, r, a.tabURL(f.ID, submissionSearch(r), submissionStatusParam(r), page, ""), http.StatusFound)
}

// handleSubmissionsExport streams every submission (of the chosen status and
// search, as the Submissions tab shows them) as CSV, a page at a time, so a large form never sits in memory. Each field of the
// form is a column; answers the form no longer has follow in one "other" column.
func (a *Admin) handleSubmissionsExport(w http.ResponseWriter, r *http.Request) {
	f, ok := a.loadFormQuery(w, r)
	if !ok {
		return
	}
	status, q := submissionStatusParam(r), submissionSearch(r)
	known := map[string]bool{}
	header := []string{"Received (UTC)", "Status", "Submission ID"}
	for _, ff := range f.Fields {
		if ff.IsLayout() {
			continue
		}
		known[ff.Name] = true
		header = append(header, csvx.Safe(ff.Label))
	}
	header = append(header, "Other answers", "Page", "Referrer",
		"First channel", "First source", "First landing", "Last channel", "Last source", "Last landing")
	h := w.Header()
	h.Set("Content-Type", "text/csv; charset=utf-8")
	h.Set("Content-Disposition", `attachment; filename="`+f.Slug+"-submissions-"+time.Now().UTC().Format("20060102")+`.csv"`)
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	cw := csv.NewWriter(w)
	_, _ = w.Write([]byte(csvx.BOM)) // BOM: spreadsheets read UTF-8 correctly
	_ = cw.Write(header)
	rc := http.NewResponseController(w)
	var after *form.SubmissionCursor
	for written := 0; ; {
		subs, _, err := a.deps.Growth.Forms.ListSubmissions(r.Context(), f.ID, &form.ListSubmissionsParams{Status: status, Search: q, Limit: exportPageSize, After: after})
		if err != nil {
			// The status line is gone; end the file visibly rather than silently short.
			a.logger().Error("export submissions failed", "err", err)
			_ = cw.Write([]string{"# export stopped: the database could not be read"})
			break
		}
		for _, s := range subs {
			if written >= exportMaxRows {
				_ = cw.Write([]string{"# export truncated at " + strconv.Itoa(exportMaxRows) + " submissions; narrow the search or status filter to export the rest"})
				cw.Flush()
				return
			}
			written++
			row := []string{s.CreatedAt.UTC().Format(time.RFC3339), s.Status, s.ID.String()}
			for _, ff := range f.Fields {
				if ff.IsLayout() {
					continue
				}
				row = append(row, csvx.Safe(formatAnswer(s.Data[ff.Name])))
			}
			var other []string
			keys := make([]string, 0, len(s.Data))
			for k := range s.Data {
				if !known[k] {
					keys = append(keys, k)
				}
			}
			sort.Strings(keys)
			for _, k := range keys {
				other = append(other, k+": "+formatAnswer(s.Data[k]))
			}
			row = append(row, csvx.Safe(strings.Join(other, "; ")), csvx.Safe(s.Metadata.PageURL), csvx.Safe(s.Metadata.Referrer))
			at := s.Attribution
			if at == nil {
				at = &form.Attribution{}
			}
			for _, t := range []*form.Touch{at.First, at.Last} {
				ch, src, landing := touchParts(t)
				row = append(row, csvx.Safe(ch), csvx.Safe(src), csvx.Safe(landing))
			}
			_ = cw.Write(row)
		}
		cw.Flush()
		_ = rc.Flush()
		if len(subs) > 0 {
			last := subs[len(subs)-1]
			after = &form.SubmissionCursor{CreatedAt: last.CreatedAt, ID: last.ID}
		}
		if len(subs) < exportPageSize || r.Context().Err() != nil {
			break
		}
	}
	cw.Flush()
}

// loadSubmission loads the submission in the path and the form it belongs to.
func (a *Admin) loadSubmission(w http.ResponseWriter, r *http.Request) (*form.Form, *form.FormSubmission, bool) {
	fs, ok := a.formStore(w, r)
	if !ok {
		return nil, nil, false
	}
	sid, err := uuid.Parse(r.PathValue("sid"))
	if err == nil {
		var s *form.FormSubmission
		if s, err = fs.GetSubmission(r.Context(), sid); err == nil {
			var f *form.Form
			if f, err = fs.GetForm(r.Context(), s.FormID); err == nil {
				return f, s, true
			}
		}
		if err != nil && !errors.Is(err, form.ErrSubmissionNotFound) && !errors.Is(err, form.ErrFormNotFound) {
			a.logger().Error("load submission failed", "err", err)
			a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The submission could not be loaded.")
			return nil, nil, false
		}
	}
	a.ErrorBack(w, r, http.StatusNotFound, "Submission not found", "That submission does not exist.", a.adminPath+"/forms/submissions", "Back to submissions")
	return nil, nil, false
}

type answerRow struct{ Label, Value string }

// touchParts is a stored touch as its channel name, source and landing page, in that order.
// A submission stored without attribution has no touch: the channel reads Unknown, the rest is empty.
func touchParts(t *form.Touch) (channel, source, landing string) {
	if t == nil {
		return unknownName, "", ""
	}
	channel = unknownName
	if t.Channel != "" {
		channel = channelName(t.Channel)
	}
	if t.Source != "" {
		source = sourceName(t.Source)
	}
	return channel, source, t.Landing
}

// cameFrom is the Came from card: one row per touch, "channel / source / campaign / landing".
func cameFrom(at *form.Attribution) []answerRow {
	if at == nil {
		at = &form.Attribution{}
	}
	row := func(label string, t *form.Touch) answerRow {
		ch, src, landing := touchParts(t)
		parts := []string{ch}
		if src != "" {
			parts = append(parts, src)
		}
		if t != nil && t.Campaign != "" {
			parts = append(parts, t.Campaign)
		}
		if landing != "" {
			parts = append(parts, landing)
		}
		return answerRow{label, strings.Join(parts, " / ")}
	}
	return []answerRow{row("First touch", at.First), row("Last touch", at.Last)}
}

// handleSubmission is the address of one submission, which the notification
// email links to: it sends the browser to the form's Submissions tab with that
// submission open. It never marks anything read; opening it on the tab does.
func (a *Admin) handleSubmission(w http.ResponseWriter, r *http.Request) {
	f, s, ok := a.loadSubmission(w, r)
	if !ok {
		return
	}
	http.Redirect(w, r, a.tabURL(f.ID, "", "", 1, s.ID.String()), http.StatusSeeOther)
}

func (a *Admin) handleSubmissionStatus(w http.ResponseWriter, r *http.Request) {
	f, s, ok := a.loadSubmission(w, r)
	if !ok {
		return
	}
	st := r.PostFormValue("status")
	if !form.ValidSubmissionStatus(st) {
		a.Error(w, r, http.StatusBadRequest, "Bad request", "That is not a submission status.")
		return
	}
	if _, err := a.deps.Growth.Forms.UpdateSubmission(r.Context(), s.ID, &form.UpdateSubmissionInput{Status: &st}); err != nil {
		a.logger().Error("update submission failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The submission could not be updated.")
		return
	}
	a.SetFlash(w, r, "success", "Marked as "+st+".")
	Redirect(w, r, a.backToTab(f, r.PostFormValue("back")))
}

// handleSubmissionEmail forwards one submission by email; the result is a flash
// on the submission's tab.
func (a *Admin) handleSubmissionEmail(w http.ResponseWriter, r *http.Request) {
	f, s, ok := a.loadSubmission(w, r)
	if !ok {
		return
	}
	back := a.backToTab(f, r.PostFormValue("back"))
	if strings.Contains(back, "?") {
		back += "&s=" + s.ID.String()
	} else {
		back += "?s=" + s.ID.String()
	}
	m := a.deps.Growth.FormMail
	if m == nil || !m.Configured() {
		a.SetFlash(w, r, "error", "Email is not set up on this server, so nothing was sent.")
		Redirect(w, r, back)
		return
	}
	to := strings.TrimSpace(r.PostFormValue("to"))
	var rcpt []string
	for _, p := range strings.Split(to, ",") {
		if p = strings.TrimSpace(p); p != "" {
			rcpt = append(rcpt, p)
		}
	}
	if len(rcpt) == 0 || len(rcpt) > 10 || !validEmailList(to) {
		a.SetFlash(w, r, "error", "Enter up to 10 email addresses separated by commas.")
		Redirect(w, r, back)
		return
	}
	by := ""
	if u := userFrom(r.Context()); u != nil {
		by = u.Email
	}
	if err := m.Forward(r.Context(), f, s, rcpt, by); err != nil {
		a.logger().Error("forward submission failed", "err", err)
		a.SetFlash(w, r, "error", "The email could not be sent. Check the SMTP settings of the server.")
		Redirect(w, r, back)
		return
	}
	a.SetFlash(w, r, "success", "Submission emailed to "+strings.Join(rcpt, ", ")+".")
	Redirect(w, r, back)
}

func (a *Admin) handleSubmissionDeleteForm(w http.ResponseWriter, r *http.Request) {
	f, s, ok := a.loadSubmission(w, r)
	if !ok {
		return
	}
	back := a.backToTab(f, r.URL.Query().Get("back"))
	a.renderConfirm(w, r, confirmData{
		Title: "Delete this submission?", Message: "The submission of " + fmtDateTime(s.CreatedAt) + " is deleted for good.",
		Post: a.adminPath + "/forms/submissions/" + s.ID.String() + "/delete?back=" + url.QueryEscape(back), Label: "Delete submission",
		Back: back, Danger: true,
	})
}

func (a *Admin) handleSubmissionDelete(w http.ResponseWriter, r *http.Request) {
	f, s, ok := a.loadSubmission(w, r)
	if !ok {
		return
	}
	if err := a.deps.Growth.Forms.DeleteSubmission(r.Context(), s.ID); err != nil && !errors.Is(err, form.ErrSubmissionNotFound) {
		a.logger().Error("delete submission failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The submission could not be deleted.")
		return
	}
	a.SetFlash(w, r, "success", "Submission deleted.")
	Redirect(w, r, a.backToTab(f, r.FormValue("back")))
}

// handleSubmissionsBulk applies one action to the ticked submissions. Delete
// asks first: the first POST shows the confirmation page, which posts again with
// confirm=1.
func (a *Admin) handleSubmissionsBulk(w http.ResponseWriter, r *http.Request) {
	f, ok := a.loadFormQuery(w, r)
	if !ok {
		return
	}
	back := a.backToTab(f, r.FormValue("back"))
	action := r.PostFormValue("action")
	var ids []uuid.UUID
	for _, s := range r.PostForm["id"] {
		if id, err := uuid.Parse(s); err == nil {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		a.SetFlash(w, r, "error", "Tick at least one submission first.")
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	if len(ids) > 500 {
		ids = ids[:500]
	}
	status := map[string]string{"read": form.SubmissionStatusRead, "archive": form.SubmissionStatusArchived, "spam": form.SubmissionStatusSpam, "new": form.SubmissionStatusNew}[action]
	switch {
	case action == "delete" && r.PostFormValue("confirm") == "" && r.PostFormValue("confirmed") != "1":
		a.renderBulkDeleteConfirm(w, r, f, ids, back)
		return
	case action != "delete" && status == "":
		a.Error(w, r, http.StatusBadRequest, "Bad request", "That is not a bulk action.")
		return
	}
	done := 0
	for _, id := range ids {
		// Only submissions of this form.
		s, err := a.deps.Growth.Forms.GetSubmission(r.Context(), id)
		if err != nil || s.FormID != f.ID {
			continue
		}
		if action == "delete" {
			err = a.deps.Growth.Forms.DeleteSubmission(r.Context(), id)
		} else {
			_, err = a.deps.Growth.Forms.UpdateSubmission(r.Context(), id, &form.UpdateSubmissionInput{Status: &status})
		}
		if err != nil {
			a.logger().Error("bulk submission action failed", "action", action, "err", err)
			continue
		}
		done++
	}
	verb := map[string]string{"read": "marked as read", "archive": "archived", "spam": "marked as spam", "new": "marked as new", "delete": "deleted"}[action]
	a.SetFlash(w, r, "success", fmt.Sprintf("%d %s %s.", done, plural(done, "submission", "submissions"), verb))
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// backToTab is where an action on a form's submissions returns to: the form's
// Submissions tab with the search, status and page of the address back names.
// Only those three values are taken from back, and only when it is an address
// on this admin (a path under the admin prefix, no scheme or host), so a
// forged value can never send the browser elsewhere.
func (a *Admin) backToTab(f *form.Form, back string) string {
	u, err := url.Parse(back)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil || strings.Contains(back, `\`) || !strings.HasPrefix(u.Path, a.adminPath+"/") {
		return a.tabURL(f.ID, "", "", 1, "")
	}
	q := u.Query()
	status := q.Get("status")
	if !form.ValidSubmissionStatus(status) {
		status = ""
	}
	page, _ := strconv.Atoi(q.Get("page"))
	search := strings.TrimSpace(q.Get("q"))
	if rs := []rune(search); len(rs) > submissionSearchMax {
		search = string(rs[:submissionSearchMax])
	}
	return a.tabURL(f.ID, search, status, page, "")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func (a *Admin) renderBulkDeleteConfirm(w http.ResponseWriter, r *http.Request, f *form.Form, ids []uuid.UUID, back string) {
	var hidden []string
	for _, id := range ids {
		hidden = append(hidden, id.String())
	}
	v := a.NewView(w, r, "Delete submissions")
	v.Data = bulkDelete{len(ids), a.adminPath + "/forms/submissions/bulk?form=" + f.ID.String() + "&back=" + url.QueryEscape(back), back, hidden, "submission", "delete"}
	v.Width = "narrow"
	a.Render(w, r, http.StatusOK, "bulkdelete", v)
}
