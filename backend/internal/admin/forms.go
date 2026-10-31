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
	"hatamoto.dev/core/internal/form"
	"hatamoto.dev/core/internal/httpx"
)

const (
	maxFormFields      = 60
	submissionsPerPage = 25
	exportPageSize     = 500
	exportMaxRows      = 200000
)

var fieldNameRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// FormMailer forwards a submission by email (the "Email this" action).
type FormMailer interface {
	Configured() bool
	Forward(ctx context.Context, f *form.Form, sub *form.FormSubmission, to []string, by string) error
}

func (a *Admin) mountForms(authed *httpx.Group) {
	authed.HandleFunc("GET /settings/forms", a.handleForms)
	authed.HandleFunc("GET /settings/forms/new", a.handleFormNew)
	authed.HandleFunc("POST /settings/forms", a.handleFormCreate)
	authed.HandleFunc("GET /settings/forms/{id}/edit", a.handleFormEdit)
	authed.HandleFunc("POST /settings/forms/{id}/edit", a.handleFormSave)
	authed.HandleFunc("GET /settings/forms/{id}/delete", a.handleFormDeleteForm)
	authed.HandleFunc("POST /settings/forms/{id}/delete", a.handleFormDelete)
	// The list, export and bulk routes take the form as ?form={id}; a single
	// submission knows its form.
	authed.HandleFunc("GET /settings/submissions", a.handleSubmissions)
	authed.HandleFunc("GET /settings/submissions/export.csv", a.handleSubmissionsExport)
	authed.HandleFunc("POST /settings/submissions/bulk", a.handleSubmissionsBulk)
	authed.HandleFunc("GET /settings/submissions/{sid}", a.handleSubmission)
	authed.HandleFunc("POST /settings/submissions/{sid}/status", a.handleSubmissionStatus)
	authed.HandleFunc("POST /settings/submissions/{sid}/email", a.handleSubmissionEmail)
	authed.HandleFunc("GET /settings/submissions/{sid}/delete", a.handleSubmissionDeleteForm)
	authed.HandleFunc("POST /settings/submissions/{sid}/delete", a.handleSubmissionDelete)
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
	a.ErrorBack(w, r, http.StatusNotFound, "Form not found", "That form does not exist.", a.adminPath+"/settings/forms", "Back to forms")
	return nil, false
}

// ---- list, create, delete ----

func formStatusBadge(status string) *ui.Badge {
	switch status {
	case form.StatusActive:
		return &ui.Badge{Label: "Active", Kind: "success"}
	case form.StatusInactive:
		return &ui.Badge{Label: "Inactive", Kind: "warning"}
	}
	return &ui.Badge{Label: "Archived", Kind: "neutral"}
}

const (
	formSparkDays    = 14
	formSparkScanCap = 400 // rows read per form for its spark: two pages, to bound the cost of the list
)

// newCountCell shows unread submissions as an accent badge ("2 New"), and plain 0 when there are none.
func newCountCell(n int) ui.Cell {
	if n == 0 {
		return ui.Cell{Text: "0"}
	}
	return ui.Cell{Badge: &ui.Badge{Label: strconv.Itoa(n) + " New", Kind: "info"}}
}

// formActivityCells builds the "All submissions" and "Last 14 days" cells of a forms-list row.
// The spark stands alone (the cell holds no number beside it); with no activity the cell reads 0.
// A failed scan shows a dash in both instead of a 0, and a capped scan shows a lower bound
// without a spark, because days beyond the cap would read as a false drop-off.
func formActivityCells(recent []*form.FormSubmission, all int, capped bool, err error, now time.Time, loc *time.Location) (total, last14 ui.Cell) {
	if err != nil {
		return ui.Cell{Text: "—"}, ui.Cell{Text: "—"}
	}
	days := submissionDays(recent, now, formSparkDays, loc)
	vals := make([]int64, len(days))
	var sum int64
	for i, n := range days {
		vals[i], sum = int64(n), sum+int64(n)
	}
	total = ui.Cell{Text: strconv.Itoa(all)}
	if capped {
		return total, ui.Cell{Text: strconv.FormatInt(sum, 10) + "+"}
	}
	if sp := ui.SparkOf(vals); sp != nil {
		return total, ui.Cell{Spark: sp}
	}
	return total, ui.Cell{Text: strconv.FormatInt(sum, 10)}
}

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
	t := ui.Table{
		ID: "forms", Caption: "Forms", BaseURL: a.adminPath + "/settings/forms", CSRF: csrfToken(r.Context()),
		Columns: []ui.Col{{Key: "name", Label: "Name"}, {Key: "status", Label: "Status"}, {Key: "fields", Label: "Fields", Align: "right", WideOnly: true}, {Key: "new", Label: "New submissions", Align: "right"}, {Key: "total", Label: "All submissions", Align: "right", WideOnly: true}, {Key: "recent", Label: "Last 14 days"}},
		Empty:   ui.Empty{Icon: "inbox", Title: "No forms yet", Body: "A form collects enquiries from your pages.", Actions: []ui.Button{{Label: "New form", Href: a.adminPath + "/settings/forms/new", Variant: "primary"}}},
	}
	loc := a.siteLocation(r.Context())
	now := time.Now().In(loc)
	since := now.AddDate(0, 0, -(formSparkDays - 1))
	since = time.Date(since.Year(), since.Month(), since.Day(), 0, 0, 0, 0, loc)
	for _, f := range forms {
		id := f.ID.String()
		recent, all, capped, err := loadRecentSubmissions(r.Context(), fs, f.ID, since, formSparkScanCap)
		if err != nil {
			a.logger().Error("form activity failed", "form", id, "err", err)
		}
		totalCell, recentCell := formActivityCells(recent, all, capped, err, now, loc)
		_, fresh, err := fs.ListSubmissions(r.Context(), f.ID, &form.ListSubmissionsParams{Limit: 1, Status: form.SubmissionStatusNew})
		if err != nil {
			a.logger().Error("form new count failed", "form", id, "err", err)
		}
		t.Rows = append(t.Rows, ui.Row{ID: id, Label: f.Name, Cells: []ui.Cell{
			{Text: f.Name, Link: a.adminPath + "/settings/forms/" + id + "/edit"},
			{Badge: formStatusBadge(f.Status)},
			{Text: strconv.Itoa(len(f.Fields))}, newCountCell(fresh), totalCell,
			recentCell,
		}, Actions: []ui.Action{
			{Label: "Submissions", Href: a.adminPath + "/settings/submissions?form=" + id},
			{Label: "Edit", Href: a.adminPath + "/settings/forms/" + id + "/edit"},
			{Label: "Delete", Href: a.adminPath + "/settings/forms/" + id + "/delete", Post: a.adminPath + "/settings/forms/" + id + "/delete", Danger: true, ConfirmLabel: "Delete form",
				Confirm: "The form " + f.Name + " and all of its submissions are deleted. Pages that embed it stop showing it."},
		}})
	}
	v := a.NewView(w, r, "Forms")
	v.Data = struct {
		Table   ui.Table
		Actions []ui.Button
	}{t, []ui.Button{{Label: "New form", Href: a.adminPath + "/settings/forms/new", Variant: "primary", Icon: "plus"}}}
	a.Render(w, r, http.StatusOK, "forms/list", v)
}

type newFormData struct {
	Name, Slug, Description string
	Errors                  map[string]string
	Summary                 []ui.FieldError
}

func (a *Admin) renderNewForm(w http.ResponseWriter, r *http.Request, status int, d newFormData) {
	for _, k := range []struct{ id, label string }{{"name", "Name"}, {"slug", "Identifier"}} {
		if m := d.Errors[k.id]; m != "" {
			d.Summary = append(d.Summary, ui.FieldError{ID: k.id, Label: k.label, Message: m})
		}
	}
	v := a.NewView(w, r, "New form")
	v.Crumbs = a.areaCrumbs("forms", Crumb{Label: "New form"})
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
	d := newFormData{Name: strings.TrimSpace(r.PostFormValue("name")), Slug: strings.TrimSpace(r.PostFormValue("slug")), Description: strings.TrimSpace(r.PostFormValue("description")), Errors: map[string]string{}}
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
	in := &form.CreateFormInput{Name: d.Name, Slug: d.Slug, Fields: []form.FormField{}, Settings: form.FormSettings{StoreSubmissions: true}}
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
	a.SetFlash(w, r, "success", "Form created. Add its fields below.")
	Redirect(w, r, a.adminPath+"/settings/forms/"+f.ID.String()+"/edit")
}

var slugRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

func (a *Admin) handleFormDeleteForm(w http.ResponseWriter, r *http.Request) {
	f, ok := a.loadForm(w, r)
	if !ok {
		return
	}
	a.renderConfirm(w, r, confirmData{
		Title: "Delete the form " + f.Name + "?", Message: "The form and all of its submissions are deleted. Pages that embed it stop showing it.",
		Post: a.adminPath + "/settings/forms/" + f.ID.String() + "/delete", Label: "Delete form", Back: a.adminPath + "/settings/forms", Danger: true,
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
	Redirect(w, r, a.adminPath+"/settings/forms")
}

// ---- the builder ----

type formFieldRow struct {
	N                                 int
	ID, Type, Label, Name             string
	Placeholder, Help, Width, Options string
	MinLen, MaxLen, Pattern, Message  string
	Required                          bool
	Types, Widths                     []ui.Option
	Err                               map[string]string
	TypeIcon                          string // icon in the collapsed row summary
	Open                              bool   // the row renders expanded: it has an error or was just added
}

type formEdit struct {
	ID, Name, Slug, Description, Status string
	SubmitText, SuccessMsg, RedirectURL string
	NotifyEmail, ConfirmMsg             string
	Captcha, Confirmation, Store        bool
	Fields                              []formFieldRow
	Statuses, AddTypes                  []ui.Option
	Errors                              map[string]string
	Summary                             []ui.FieldError
	Notice, EmbedCode, Endpoint         string
	Actions                             []ui.Button
	AddType                             string
	Submissions                         int
	// CaptchaUnconfigured: the switch is on but no captcha provider keys are
	// usable, so submissions are accepted unchecked. SpamLink is true when the
	// viewer may open the page that fixes it (owners).
	CaptchaUnconfigured, SpamLink bool
}

var fieldTypeLabels = map[string]string{
	form.FieldTypeText: "Single-line text", form.FieldTypeEmail: "Email address", form.FieldTypePhone: "Phone number",
	form.FieldTypeTextarea: "Multi-line text", form.FieldTypeSelect: "Drop-down list", form.FieldTypeRadio: "Choose one (radio buttons)",
	form.FieldTypeCheckbox: "Choose several (checkboxes)", form.FieldTypeDate: "Date", form.FieldTypeNumber: "Number", form.FieldTypeURL: "Web address",
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
	}
	return "pen"
}

func fieldTypeOptions(selected string) []ui.Option {
	var out []ui.Option
	for _, t := range form.GetFieldTypes() {
		out = append(out, ui.Option{Value: t, Label: fieldTypeLabels[t], Selected: t == selected})
	}
	return out
}

func widthOptions(selected string) []ui.Option {
	if selected == "" {
		selected = "full"
	}
	return []ui.Option{
		{Value: "full", Label: "Full width", Selected: selected == "full"},
		{Value: "half", Label: "Half width", Selected: selected == "half"},
		{Value: "third", Label: "One third", Selected: selected == "third"},
	}
}

func statusOptions(selected string) []ui.Option {
	return []ui.Option{
		{Value: form.StatusActive, Label: "Active: accepts submissions", Selected: selected == form.StatusActive},
		{Value: form.StatusInactive, Label: "Inactive: shows nothing and accepts nothing", Selected: selected == form.StatusInactive},
		{Value: form.StatusArchived, Label: "Archived", Selected: selected == form.StatusArchived},
	}
}

func hasOptions(t string) bool {
	return t == form.FieldTypeSelect || t == form.FieldTypeRadio || t == form.FieldTypeCheckbox
}

func formToEdit(f *form.Form) formEdit {
	e := formEdit{ID: f.ID.String(), Name: f.Name, Slug: f.Slug, Status: f.Status,
		SubmitText: f.Settings.SubmitButtonText, SuccessMsg: f.Settings.SuccessMessage, RedirectURL: f.Settings.SuccessRedirectURL,
		ConfirmMsg: f.Settings.ConfirmationMessage, Captcha: f.Settings.EnableCaptcha, Confirmation: f.Settings.ConfirmationEmail, Store: f.Settings.StoreSubmissions}
	if f.Description != nil {
		e.Description = *f.Description
	}
	if f.Settings.NotifyEmail != nil {
		e.NotifyEmail = *f.Settings.NotifyEmail
	}
	fields := append([]form.FormField(nil), f.Fields...)
	sort.SliceStable(fields, func(i, j int) bool { return fields[i].Position < fields[j].Position })
	for _, ff := range fields {
		row := formFieldRow{ID: ff.ID, Type: ff.Type, Label: ff.Label, Name: ff.Name, Placeholder: ff.Placeholder, Help: ff.HelpText, Width: ff.Width, Required: ff.Required}
		var opts []string
		for _, o := range ff.Options {
			if o.Value == "" || o.Value == o.Label {
				opts = append(opts, o.Label)
			} else {
				opts = append(opts, o.Label+" | "+o.Value)
			}
		}
		row.Options = strings.Join(opts, "\n")
		if v := ff.Validation; v != nil {
			if v.MinLength != nil {
				row.MinLen = strconv.Itoa(*v.MinLength)
			}
			if v.MaxLength != nil {
				row.MaxLen = strconv.Itoa(*v.MaxLength)
			}
			if v.Pattern != nil {
				row.Pattern = *v.Pattern
			}
			if v.Message != nil {
				row.Message = *v.Message
			}
		}
		e.Fields = append(e.Fields, row)
	}
	return e
}

func (a *Admin) handleFormEdit(w http.ResponseWriter, r *http.Request) {
	f, ok := a.loadForm(w, r)
	if !ok {
		return
	}
	a.renderFormEdit(w, r, http.StatusOK, formToEdit(f))
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

func (a *Admin) renderFormEdit(w http.ResponseWriter, r *http.Request, status int, e formEdit) {
	e.Statuses = statusOptions(e.Status)
	e.AddTypes = fieldTypeOptions(e.AddType)
	for i := range e.Fields {
		e.Fields[i].N = i
		e.Fields[i].Types = fieldTypeOptions(e.Fields[i].Type)
		e.Fields[i].Widths = widthOptions(e.Fields[i].Width)
		e.Fields[i].TypeIcon = fieldTypeIcon(e.Fields[i].Type)
		e.Fields[i].Open = len(e.Fields[i].Err) > 0 || (e.Fields[i].ID == "" && e.Fields[i].Label == "")
	}
	if e.Captcha {
		e.CaptchaUnconfigured = a.captchaUnconfigured(r.Context())
		e.SpamLink = userFrom(r.Context()).Role == "owner"
	}
	e.Actions = []ui.Button{{Label: "Submissions", Href: a.adminPath + "/settings/submissions?form=" + e.ID, Icon: "inbox"}}
	e.Endpoint = a.formEndpoint(e.Slug)
	e.EmbedCode = fmt.Sprintf("<form action=\"%s\" method=\"POST\">\n  <!-- Add the fields here -->\n  <button type=\"submit\">%s</button>\n</form>", e.Endpoint, orDefault(e.SubmitText, "Submit"))
	e.Summary = nil
	for _, k := range []struct{ id, label string }{{"name", "Name"}, {"status", "Status"}, {"notify", "Notification emails"}, {"redirect", "Redirect address"}, {"fields", "Fields"}} {
		if m := e.Errors[k.id]; m != "" {
			e.Summary = append(e.Summary, ui.FieldError{ID: k.id, Label: k.label, Message: m})
		}
	}
	for _, fr := range e.Fields {
		for _, k := range []string{"label", "name", "options", "minlen", "maxlen", "pattern"} {
			if m := fr.Err[k]; m != "" {
				e.Summary = append(e.Summary, ui.FieldError{ID: "f-" + strconv.Itoa(fr.N) + "-" + k, Label: "Field " + strconv.Itoa(fr.N+1), Message: m})
			}
		}
	}
	v := a.NewView(w, r, "Edit form: "+e.Name)
	v.Crumbs = a.areaCrumbs("forms", Crumb{Label: e.Name})
	v.Data = e
	v.Width = "narrow"
	a.Render(w, r, status, "forms/edit", v)
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

func parseFormFields(vals url.Values) []formFieldRow {
	seen := map[int]bool{}
	for k := range vals {
		if rest, ok := strings.CutPrefix(k, "f."); ok {
			if n, _, ok := strings.Cut(rest, "."); ok {
				if i, err := strconv.Atoi(n); err == nil && i >= 0 && i < 10000 {
					seen[i] = true
				}
			}
		}
	}
	ns := make([]int, 0, len(seen))
	for n := range seen {
		ns = append(ns, n)
	}
	sort.Ints(ns)
	rows := make([]formFieldRow, 0, len(ns))
	for _, n := range ns {
		p := "f." + strconv.Itoa(n) + "."
		g := func(k string) string { return strings.TrimSpace(vals.Get(p + k)) }
		rows = append(rows, formFieldRow{
			ID: g("id"), Type: g("type"), Label: g("label"), Name: g("name"), Placeholder: g("placeholder"), Help: g("help"), Width: g("width"),
			Options: strings.ReplaceAll(vals.Get(p+"options"), "\r\n", "\n"), MinLen: g("minlen"), MaxLen: g("maxlen"), Pattern: g("pattern"), Message: g("message"),
			Required: g("required") != "",
		})
	}
	return rows
}

func applyFieldOp(rows []formFieldRow, op, addType string) ([]formFieldRow, string) {
	if op == "add" {
		if !form.ValidFieldType(addType) {
			addType = form.FieldTypeText
		}
		return append(rows, formFieldRow{Type: addType, Width: "full"}), "Field added. It is not saved yet."
	}
	name, idx, ok := strings.Cut(op, ".")
	i, err := strconv.Atoi(idx)
	if !ok || err != nil || i < 0 || i >= len(rows) {
		return rows, ""
	}
	switch name {
	case "remove":
		return append(rows[:i:i], rows[i+1:]...), "Field removed. It is not saved yet. Existing submissions keep their answers."
	case "up":
		if i == 0 {
			return rows, "This field is already first."
		}
		rows[i-1], rows[i] = rows[i], rows[i-1]
		return rows, "Field moved up. It is not saved yet."
	case "down":
		if i == len(rows)-1 {
			return rows, "This field is already last."
		}
		rows[i+1], rows[i] = rows[i], rows[i+1]
		return rows, "Field moved down. It is not saved yet."
	}
	return rows, ""
}

// parseOptions reads "Label" or "Label | value" per line.
func parseOptions(s string) []form.FieldOption {
	var out []form.FieldOption
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		label, value, _ := strings.Cut(line, "|")
		label, value = strings.TrimSpace(label), strings.TrimSpace(value)
		if value == "" {
			value = strings.Trim(regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(strings.ToLower(label), "_"), "_")
			if value == "" {
				value = label
			}
		}
		out = append(out, form.FieldOption{Label: label, Value: value})
	}
	return out
}

// buildFormFields validates the rows and returns the fields to store; per-row
// problems go into the row's Err.
func buildFormFields(rows []formFieldRow) []form.FormField {
	names := map[string]int{}
	out := make([]form.FormField, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		r.Err = map[string]string{}
		if !form.ValidFieldType(r.Type) {
			r.Type = form.FieldTypeText
		}
		if r.Label == "" {
			r.Err["label"] = "Enter a label"
		} else if utf8.RuneCountInString(r.Label) > 200 {
			r.Err["label"] = "Use at most 200 characters"
		}
		if r.Name == "" && r.Label != "" {
			r.Name = fieldNameFromLabel(r.Label)
		}
		switch {
		case r.Name == "":
		case !fieldNameRe.MatchString(r.Name):
			r.Err["name"] = "Start with a letter, then lowercase letters, numbers and underscores"
		case names[r.Name] > 0:
			r.Err["name"] = "Another field already uses this name"
		}
		names[r.Name]++
		ff := form.FormField{ID: r.ID, Type: r.Type, Label: r.Label, Name: r.Name, Placeholder: r.Placeholder, HelpText: r.Help, Required: r.Required, Position: i}
		if r.Width == "half" || r.Width == "third" {
			ff.Width = r.Width
		} else {
			ff.Width = "full"
		}
		if ff.ID == "" {
			ff.ID = uuid.NewString()
			r.ID = ff.ID
		}
		if hasOptions(r.Type) {
			ff.Options = parseOptions(r.Options)
			if len(ff.Options) == 0 {
				r.Err["options"] = "Add at least one choice, one per line"
			}
		}
		v := &form.FieldValidation{}
		has := false
		for _, n := range []struct {
			s   string
			dst **int
			key string
		}{{r.MinLen, &v.MinLength, "minlen"}, {r.MaxLen, &v.MaxLength, "maxlen"}} {
			if n.s == "" {
				continue
			}
			x, err := strconv.Atoi(n.s)
			if err != nil || x < 0 || x > 100000 {
				r.Err[n.key] = "Enter a whole number"
				continue
			}
			*n.dst, has = &x, true
		}
		if r.Pattern != "" {
			if _, err := regexp.Compile(r.Pattern); err != nil || len(r.Pattern) > 500 {
				r.Err["pattern"] = "This is not a valid pattern"
			} else {
				p := r.Pattern
				v.Pattern, has = &p, true
			}
		}
		if r.Message != "" {
			m := r.Message
			v.Message, has = &m, true
		}
		if has {
			ff.Validation = v
		}
		out = append(out, ff)
	}
	return out
}

// carryFieldData puts back what the builder has no input for (default value,
// attributes, numeric min/max) from the stored field with the same ID, so a
// save never drops data set through the JSON API or MCP.
func carryFieldData(fields, stored []form.FormField) []form.FormField {
	byID := make(map[string]*form.FormField, len(stored))
	for i := range stored {
		byID[stored[i].ID] = &stored[i]
	}
	for i := range fields {
		old := byID[fields[i].ID]
		if old == nil {
			continue
		}
		fields[i].DefaultValue, fields[i].Attributes = old.DefaultValue, old.Attributes
		if old.Validation != nil && (old.Validation.Min != nil || old.Validation.Max != nil) {
			if fields[i].Validation == nil {
				fields[i].Validation = &form.FieldValidation{}
			}
			fields[i].Validation.Min, fields[i].Validation.Max = old.Validation.Min, old.Validation.Max
		}
	}
	return fields
}

func fieldNameFromLabel(label string) string {
	n := strings.Trim(regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(strings.ToLower(label), "_"), "_")
	if n == "" || n[0] < 'a' || n[0] > 'z' {
		n = "field_" + n
	}
	if len(n) > 64 {
		n = n[:64]
	}
	return strings.TrimRight(n, "_")
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

func safeRedirectTarget(u string) bool {
	if u == "" {
		return true
	}
	if strings.HasPrefix(u, "/") && !strings.HasPrefix(u, "//") && !strings.ContainsAny(u, "\\ \t\r\n") {
		return true
	}
	p, err := url.Parse(u)
	return err == nil && (p.Scheme == "https" || p.Scheme == "http") && p.Host != "" && !strings.ContainsAny(u, " \t\r\n\\")
}

func (a *Admin) handleFormSave(w http.ResponseWriter, r *http.Request) {
	cur, ok := a.loadForm(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		a.Error(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}
	p := r.PostForm
	t := func(k string) string { return strings.TrimSpace(p.Get(k)) }
	e := formEdit{ID: cur.ID.String(), Slug: cur.Slug, Name: t("name"), Description: t("description"), Status: t("status"),
		SubmitText: t("submit_text"), SuccessMsg: t("success_message"), RedirectURL: t("redirect_url"), NotifyEmail: t("notify_email"), ConfirmMsg: t("confirmation_message"),
		Captcha: t("captcha") != "", Confirmation: t("confirmation") != "", Store: t("store") != "",
		Fields: parseFormFields(p), AddType: t("add_type"), Errors: map[string]string{}}
	if op := p.Get("_op"); op != "" && op != "save" {
		e.Fields, e.Notice = applyFieldOp(e.Fields, op, e.AddType)
		e.Errors = nil
		a.renderFormEdit(w, r, http.StatusOK, e)
		return
	}
	if e.Name == "" {
		e.Errors["name"] = "Enter a name"
	} else if utf8.RuneCountInString(e.Name) > 120 {
		e.Errors["name"] = "Use at most 120 characters"
	}
	if !form.ValidStatus(e.Status) {
		e.Status = cur.Status
	}
	if !validEmailList(e.NotifyEmail) {
		e.Errors["notify"] = "Enter email addresses separated by commas"
	}
	if !safeRedirectTarget(e.RedirectURL) {
		e.Errors["redirect"] = "Enter a path like /thank-you or a full address starting with https://"
	}
	if len(e.Fields) > maxFormFields {
		e.Errors["fields"] = "A form holds at most 60 fields"
	}
	fields := carryFieldData(buildFormFields(e.Fields), cur.Fields)
	for _, fr := range e.Fields {
		if len(fr.Err) > 0 {
			e.Errors["fields"] = "Some fields need attention."
		}
	}
	if len(e.Errors) > 0 {
		a.renderFormEdit(w, r, http.StatusUnprocessableEntity, e)
		return
	}
	settings := form.FormSettings{
		SubmitButtonText: orDefault(e.SubmitText, "Submit"), SuccessMessage: orDefault(e.SuccessMsg, "Thank you for your submission!"), SuccessRedirectURL: e.RedirectURL,
		EnableCaptcha: e.Captcha, ConfirmationEmail: e.Confirmation, ConfirmationMessage: e.ConfirmMsg, StoreSubmissions: e.Store,
	}
	if e.NotifyEmail != "" {
		settings.NotifyEmail = &e.NotifyEmail
	}
	desc := e.Description
	in := &form.UpdateFormInput{Name: &e.Name, Description: &desc, Fields: fields, Settings: &settings, Status: &e.Status}
	if in.Fields == nil {
		in.Fields = []form.FormField{}
	}
	if _, err := a.deps.Growth.Forms.UpdateForm(r.Context(), cur.ID, in); err != nil {
		a.logger().Error("save form failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The form could not be saved.")
		return
	}
	a.invalidate(r.Context())
	a.SetFlash(w, r, "success", "Form saved.")
	http.Redirect(w, r, a.adminPath+"/settings/forms/"+cur.ID.String()+"/edit", http.StatusSeeOther)
}

// ---- submissions ----

var submissionStatuses = []ui.Option{
	{Value: "", Label: "All submissions"},
	{Value: form.SubmissionStatusNew, Label: "New"},
	{Value: form.SubmissionStatusRead, Label: "Read"},
	{Value: form.SubmissionStatusArchived, Label: "Archived"},
	{Value: form.SubmissionStatusSpam, Label: "Spam"},
}

func submissionBadge(s string) *ui.Badge {
	switch s {
	case form.SubmissionStatusNew:
		return &ui.Badge{Label: "New", Kind: "info"}
	case form.SubmissionStatusSpam:
		return &ui.Badge{Label: "Spam", Kind: "danger"}
	case form.SubmissionStatusArchived:
		return &ui.Badge{Label: "Archived", Kind: "neutral"}
	}
	return &ui.Badge{Label: "Read", Kind: "success"}
}

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

func summarise(f *form.Form, sub *form.FormSubmission) string {
	var parts []string
	for _, ff := range f.Fields {
		if v := formatAnswer(sub.Data[ff.Name]); v != "" {
			parts = append(parts, ff.Label+": "+truncate(v, 60))
		}
		if len(parts) == 2 {
			break
		}
	}
	return strings.Join(parts, " · ")
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

type submissionsData struct {
	Form     *form.Form
	Switch   []ui.Option // every form, the open one selected; empty hides the picker
	Table    ui.Table
	Actions  []ui.Button
	Total    int
	Statuses []ui.Option // the status filter, shown beside the form switcher
	Stats    []ui.Stat
	Tracker  ui.Tracker
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
			kind := "success"
			if n*5 >= busiest*4 { // the busiest days (within a fifth of the peak) stand out in the accent
				kind = "info"
			}
			blk = ui.TrackerBlock{Kind: kind, Tip: d.Format("2 Jan") + ": " + fmtNum(int64(n)) + " " + plural(n, "submission", "submissions")}
		}
		t.Blocks = append(t.Blocks, blk)
	}
	return stats, t, nil
}

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
	base := a.adminPath + "/settings/submissions"
	v := a.NewView(w, r, "Submissions")
	if f == nil {
		v.Data = submissionsData{Table: ui.Table{ID: "submissions", Caption: "Submissions", CSRF: csrfToken(r.Context()), BaseURL: base,
			Empty: ui.Empty{Icon: "inbox", Title: "No forms yet", Body: "Submissions appear here once a form exists and visitors send it.",
				Actions: []ui.Button{{Label: "New form", Href: a.adminPath + "/settings/forms/new", Variant: "primary"}}}}}
		a.Render(w, r, http.StatusOK, "forms/submissions", v)
		return
	}
	status := r.URL.Query().Get("status")
	if status != "" && !form.ValidSubmissionStatus(status) {
		status = ""
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	subs, total, err := fs.ListSubmissions(r.Context(), f.ID, &form.ListSubmissionsParams{Status: status, Limit: submissionsPerPage, Offset: (page - 1) * submissionsPerPage})
	if err != nil {
		a.logger().Error("list submissions failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The submissions could not be loaded.")
		return
	}
	formID := f.ID.String()
	formQ := "?form=" + formID
	query := url.Values{"form": {formID}}
	if status != "" {
		query.Set("status", status)
	}
	opts := make([]ui.Option, len(submissionStatuses))
	for i, o := range submissionStatuses {
		o.Selected = o.Value == status
		opts[i] = o
	}
	t := ui.Table{
		ID: "submissions", Caption: "Submissions of " + f.Name, BaseURL: base, CSRF: csrfToken(r.Context()),
		Hidden:  map[string]string{"form": formID},
		Columns: []ui.Col{{Key: "received", Label: "Received"}, {Key: "answers", Label: "Answers"}, {Key: "status", Label: "Status"}},
		Bulk: []ui.BulkAction{{Label: "Mark read", Action: base + "/bulk" + formQ, Value: "read"}, {Label: "Archive", Action: base + "/bulk" + formQ, Value: "archive"},
			{Label: "Mark as spam", Action: base + "/bulk" + formQ, Value: "spam"}, {Label: "Delete", Action: base + "/bulk" + formQ, Value: "delete", Danger: true}},
		BulkURL: base + "/bulk" + formQ,
		Page:    ui.Pagination{Page: page, PerPage: submissionsPerPage, Total: total, BaseURL: base, Query: query},
		Empty:   ui.Empty{Icon: "inbox", Title: "No submissions", Body: "Submissions appear here when visitors send this form."},
	}
	for _, s := range subs {
		id := s.ID.String()
		t.Rows = append(t.Rows, ui.Row{ID: id, Label: "submission of " + fmtDateTime(s.CreatedAt), Cells: []ui.Cell{
			{Text: fmtDateTime(s.CreatedAt), Link: base + "/" + id},
			{Text: summarise(f, s)},
			{Badge: submissionBadge(s.Status)},
		}, Actions: []ui.Action{
			{Label: "Open", Href: base + "/" + id},
			{Label: "Delete", Href: base + "/" + id + "/delete", Post: base + "/" + id + "/delete", Danger: true, ConfirmLabel: "Delete submission", Confirm: "This submission is deleted for good."},
		}})
	}
	exportQ := formQ
	if status != "" {
		exportQ += "&status=" + url.QueryEscape(status)
	}
	sw := make([]ui.Option, len(forms))
	for i, o := range forms {
		sw[i] = ui.Option{Value: o.ID.String(), Label: o.Name, Selected: o.ID == f.ID}
	}
	v.Title = "Submissions of " + f.Name
	stats, tracker, err := a.submissionOverview(r.Context(), fs, f)
	if err != nil {
		a.logger().Error("submission overview failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The submissions could not be loaded.")
		return
	}
	v.Data = submissionsData{Form: f, Switch: sw, Table: t, Actions: []ui.Button{{Label: "Export CSV", Href: base + "/export.csv" + exportQ, Icon: "copy"}}, Total: total,
		Statuses: opts, Stats: stats, Tracker: tracker}
	a.Render(w, r, http.StatusOK, "forms/submissions", v)
}

// csvSafe stops a spreadsheet from running a cell as a formula.
func csvSafe(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

// handleSubmissionsExport streams every submission (of the chosen status) as
// CSV, a page at a time, so a large form never sits in memory. Each field of the
// form is a column; answers the form no longer has follow in one "other" column.
func (a *Admin) handleSubmissionsExport(w http.ResponseWriter, r *http.Request) {
	f, ok := a.loadFormQuery(w, r)
	if !ok {
		return
	}
	status := r.URL.Query().Get("status")
	if status != "" && !form.ValidSubmissionStatus(status) {
		status = ""
	}
	known := map[string]bool{}
	header := []string{"Received (UTC)", "Status", "Submission ID"}
	for _, ff := range f.Fields {
		known[ff.Name] = true
		header = append(header, csvSafe(ff.Label))
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
	_, _ = w.Write([]byte("\xef\xbb\xbf")) // BOM: spreadsheets read UTF-8 correctly
	_ = cw.Write(header)
	rc := http.NewResponseController(w)
	var after *form.SubmissionCursor
	for written := 0; ; {
		subs, _, err := a.deps.Growth.Forms.ListSubmissions(r.Context(), f.ID, &form.ListSubmissionsParams{Status: status, Limit: exportPageSize, After: after})
		if err != nil {
			// The status line is gone; end the file visibly rather than silently short.
			a.logger().Error("export submissions failed", "err", err)
			_ = cw.Write([]string{"# export stopped: the database could not be read"})
			break
		}
		for _, s := range subs {
			if written >= exportMaxRows {
				_ = cw.Write([]string{"# export truncated at " + strconv.Itoa(exportMaxRows) + " submissions; filter by status to export the rest"})
				cw.Flush()
				return
			}
			written++
			row := []string{s.CreatedAt.UTC().Format(time.RFC3339), s.Status, s.ID.String()}
			for _, ff := range f.Fields {
				row = append(row, csvSafe(formatAnswer(s.Data[ff.Name])))
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
			row = append(row, csvSafe(strings.Join(other, "; ")), csvSafe(s.Metadata.PageURL), csvSafe(s.Metadata.Referrer))
			at := s.Attribution
			if at == nil {
				at = &form.Attribution{}
			}
			for _, t := range []*form.Touch{at.First, at.Last} {
				ch, src, landing := touchParts(t)
				row = append(row, csvSafe(ch), csvSafe(src), csvSafe(landing))
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
	a.ErrorBack(w, r, http.StatusNotFound, "Submission not found", "That submission does not exist.", a.adminPath+"/settings/submissions", "Back to submissions")
	return nil, nil, false
}

type answerRow struct{ Label, Value string }

type submissionData struct {
	Form       *form.Form
	Sub        *form.FormSubmission
	Answers    []answerRow
	Status     *ui.Badge
	Base       string // /admin/settings/submissions
	ListURL    string // the form's own list
	Actions    []ui.Button
	MailOn     bool
	MailTo     string
	MailErr    string
	Received   string
	StatusMove []ui.Action
	// CameFrom is the First touch and Last touch rows of the Came from card.
	CameFrom []answerRow
}

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

func (a *Admin) submissionView(w http.ResponseWriter, r *http.Request, status int, f *form.Form, s *form.FormSubmission, mailTo, mailErr string) {
	d := submissionData{Form: f, Sub: s, Status: submissionBadge(s.Status), Base: a.adminPath + "/settings/submissions", ListURL: a.adminPath + "/settings/submissions?form=" + f.ID.String(),
		MailOn: a.deps.Growth.FormMail != nil && a.deps.Growth.FormMail.Configured(), MailTo: mailTo, MailErr: mailErr, Received: fmtDateTime(s.CreatedAt), CameFrom: cameFrom(s.Attribution)}
	d.Actions = []ui.Button{{Label: "Back to submissions", Href: d.ListURL}, {Label: "Delete submission", Variant: "ghost", Href: d.Base + "/" + s.ID.String() + "/delete"}}
	used := map[string]bool{}
	for _, ff := range f.Fields {
		used[ff.Name] = true
		if v, ok := s.Data[ff.Name]; ok {
			d.Answers = append(d.Answers, answerRow{ff.Label, formatAnswer(v)})
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
		d.Answers = append(d.Answers, answerRow{k, formatAnswer(s.Data[k])})
	}
	v := a.NewView(w, r, "Submission of "+fmtDateTime(s.CreatedAt))
	v.Crumbs = a.areaCrumbs("forms", Crumb{Label: f.Name, Href: a.adminPath + "/settings/forms/" + f.ID.String() + "/edit"}, Crumb{Label: "Submissions", Href: d.ListURL}, Crumb{Label: "Submission"})
	v.Data = d
	v.Width = "narrow"
	a.Render(w, r, status, "forms/submission", v)
}

// handleSubmission shows one submission. Opening a new one marks it read, as the
// old admin did; the status buttons undo that.
func (a *Admin) handleSubmission(w http.ResponseWriter, r *http.Request) {
	f, s, ok := a.loadSubmission(w, r)
	if !ok {
		return
	}
	// A prerender or prefetch is a GET nobody made on purpose: it must not
	// mark the enquiry read (Chrome sends "Sec-Purpose: prefetch;prerender").
	if s.Status == form.SubmissionStatusNew && !strings.Contains(r.Header.Get("Sec-Purpose"), "prefetch") {
		read := form.SubmissionStatusRead
		if u, err := a.deps.Growth.Forms.UpdateSubmission(r.Context(), s.ID, &form.UpdateSubmissionInput{Status: &read}); err == nil && u != nil {
			s.Status = u.Status
		} else if err == nil {
			s.Status = read
		}
	}
	a.submissionView(w, r, http.StatusOK, f, s, "", "")
}

func (a *Admin) handleSubmissionStatus(w http.ResponseWriter, r *http.Request) {
	_, s, ok := a.loadSubmission(w, r)
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
	Redirect(w, r, a.adminPath+"/settings/submissions/"+s.ID.String())
}

func (a *Admin) handleSubmissionEmail(w http.ResponseWriter, r *http.Request) {
	f, s, ok := a.loadSubmission(w, r)
	if !ok {
		return
	}
	m := a.deps.Growth.FormMail
	if m == nil || !m.Configured() {
		a.SetFlash(w, r, "error", "Email is not set up on this server, so nothing was sent.")
		Redirect(w, r, a.adminPath+"/settings/submissions/"+s.ID.String())
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
		a.submissionView(w, r, http.StatusUnprocessableEntity, f, s, to, "Enter up to 10 email addresses separated by commas")
		return
	}
	by := ""
	if u := userFrom(r.Context()); u != nil {
		by = u.Email
	}
	if err := m.Forward(r.Context(), f, s, rcpt, by); err != nil {
		a.logger().Error("forward submission failed", "err", err)
		a.submissionView(w, r, http.StatusBadGateway, f, s, to, "The email could not be sent. Check the SMTP settings of the server.")
		return
	}
	a.SetFlash(w, r, "success", "Submission emailed to "+strings.Join(rcpt, ", ")+".")
	Redirect(w, r, a.adminPath+"/settings/submissions/"+s.ID.String())
}

func (a *Admin) handleSubmissionDeleteForm(w http.ResponseWriter, r *http.Request) {
	_, s, ok := a.loadSubmission(w, r)
	if !ok {
		return
	}
	a.renderConfirm(w, r, confirmData{
		Title: "Delete this submission?", Message: "The submission of " + fmtDateTime(s.CreatedAt) + " is deleted for good.",
		Post: a.adminPath + "/settings/submissions/" + s.ID.String() + "/delete", Label: "Delete submission",
		Back: a.adminPath + "/settings/submissions/" + s.ID.String(), Danger: true,
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
	Redirect(w, r, a.adminPath+"/settings/submissions?form="+f.ID.String())
}

// handleSubmissionsBulk applies one action to the ticked submissions. Delete
// asks first: the first POST shows the confirmation page, which posts again with
// confirm=1.
func (a *Admin) handleSubmissionsBulk(w http.ResponseWriter, r *http.Request) {
	f, ok := a.loadFormQuery(w, r)
	if !ok {
		return
	}
	back := a.adminPath + "/settings/submissions?form=" + f.ID.String()
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
	case action == "delete" && r.PostFormValue("confirm") == "":
		a.renderBulkDeleteConfirm(w, r, f, ids)
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

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func (a *Admin) renderBulkDeleteConfirm(w http.ResponseWriter, r *http.Request, f *form.Form, ids []uuid.UUID) {
	var hidden []string
	for _, id := range ids {
		hidden = append(hidden, id.String())
	}
	v := a.NewView(w, r, "Delete submissions")
	v.Data = bulkDelete{len(ids), a.adminPath + "/settings/submissions/bulk?form=" + f.ID.String(), a.adminPath + "/settings/submissions?form=" + f.ID.String(), hidden, "submission", "delete"}
	v.Width = "narrow"
	a.Render(w, r, http.StatusOK, "bulkdelete", v)
}
