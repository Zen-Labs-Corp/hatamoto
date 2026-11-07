package admin

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/form"
	"hatamoto.dev/core/internal/httpx"
)

// formShareData is the Share tab of the form designer.
type formShareData struct {
	Name, Endpoint, EmbedCode string
	Tabs                      []ui.Tab
}

// formSettingsData is the Settings tab: the Build editor bar's data, plus
// what the server renders around the controls the script binds. The values
// themselves come from the boot JSON, so one store and one saver serve both tabs.
type formSettingsData struct {
	formDesignData
	// ReplyTo are the form's email fields; Tokens the fields a subject can name.
	ReplyTo []ui.Option
	Tokens  []string
	// CaptchaUnconfigured: no captcha provider keys are usable. SpamLink is
	// true when the viewer may open the page that fixes it (owners).
	// CaptchaAttrs mark the warning for the script and hide it while the switch is off.
	CaptchaAttrs                  map[string]string
	CaptchaUnconfigured, SpamLink bool
	// Owner: the viewer may open Settings > Webhooks, so the page offers it.
	Owner bool
}

func (a *Admin) mountFormTabs(authed *httpx.Group) {
	authed.HandleFunc("GET /forms/design/{id}/{tab}", a.handleFormTab)
	authed.HandleFunc("POST /forms/design/{id}/duplicate", a.handleFormDuplicate)
}

func (a *Admin) handleFormTab(w http.ResponseWriter, r *http.Request) {
	tab := r.PathValue("tab")
	if tab != "settings" && tab != "share" && tab != "submissions" {
		a.ErrorBack(w, r, http.StatusNotFound, "Page not found", "That form page does not exist.", a.adminPath+"/forms", "Back to forms")
		return
	}
	f, ok := a.loadForm(w, r)
	if !ok {
		return
	}
	switch tab {
	case "settings":
		a.renderFormSettings(w, r, f)
		return
	case "submissions":
		a.renderSubmissionsTab(w, r, f)
		return
	}
	endpoint := a.formEndpoint(f.Slug)
	v := a.NewView(w, r, "Share and embed: "+f.Name)
	v.Crumbs = []Crumb{{Label: "Forms", Href: a.adminPath + "/forms"}, {Label: f.Name, Href: a.adminPath + "/forms/design/" + f.ID.String()}, {Label: "Share and embed"}}
	v.Data = formShareData{
		Name: f.Name, Endpoint: endpoint, Tabs: a.formDesignTabs(r.Context(), f.ID.String(), "share"),
		EmbedCode: fmt.Sprintf("<form action=\"%s\" method=\"POST\">\n  <!-- Add the fields here -->\n  <button type=\"submit\">%s</button>\n</form>", endpoint, orDefault(f.Settings.SubmitButtonText, "Submit")),
	}
	v.Width = "narrow"
	a.Render(w, r, http.StatusOK, "forms/share", v)
}

func (a *Admin) renderFormSettings(w http.ResponseWriter, r *http.Request, f *form.Form) {
	owner := userFrom(r.Context()).Role == "owner"
	boot, _, err := a.formBoot(r.Context(), f, formModeSettings, owner)
	if err != nil {
		a.logger().Error("form settings boot failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The form could not be loaded.")
		return
	}
	id := f.ID.String()
	d := formSettingsData{
		formDesignData: formDesignData{
			ID: id, Name: f.Name, Slug: f.Slug, Status: f.Status,
			Tabs: a.formDesignTabs(r.Context(), id, "settings"), Statuses: formStatusPills(f.Status), Boot: boot,
		},
		ReplyTo:      []ui.Option{{Value: "", Label: "The site address", Selected: f.Settings.NotifyReplyTo == ""}},
		CaptchaAttrs: map[string]string{"data-hm-fs-when": "enable_captcha"}, CaptchaUnconfigured: a.captchaUnconfigured(r.Context()), SpamLink: owner, Owner: owner,
	}
	if !f.Settings.EnableCaptcha {
		d.CaptchaAttrs["hidden"] = "hidden"
	}
	for _, fld := range f.Fields {
		if fld.Type == form.FieldTypeEmail {
			d.ReplyTo = append(d.ReplyTo, ui.Option{Value: fld.Name, Label: fld.Label + " (the visitor)", Selected: f.Settings.NotifyReplyTo == fld.Name})
		}
		if fld.IsInput() && fld.Type != form.FieldTypeHidden && fld.Type != form.FieldTypeConsent && len(d.Tokens) < 9 {
			d.Tokens = append(d.Tokens, fld.Name)
		}
	}
	v := a.NewView(w, r, "Settings: "+f.Name)
	v.Crumbs = []Crumb{{Label: "Forms", Href: a.adminPath + "/forms"}, {Label: f.Name, Href: a.adminPath + "/forms/design/" + id}, {Label: "Settings"}}
	v.Data = d
	a.Render(w, r, http.StatusOK, "forms/settings", v)
}

// submissionSearchMax is the longest search text the Submissions tab and its
// export use, in characters.
const submissionSearchMax = 100

// submissionSearch is the trimmed, length-capped ?q= of a request.
func submissionSearch(r *http.Request) string {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if rs := []rune(q); len(rs) > submissionSearchMax {
		q = string(rs[:submissionSearchMax])
	}
	return q
}

// submissionStatusParam is the ?status= of a request; anything that is not a
// submission status means All.
func submissionStatusParam(r *http.Request) string {
	st := r.URL.Query().Get("status")
	if !form.ValidSubmissionStatus(st) {
		return ""
	}
	return st
}

// tabURL is the Submissions tab of a form with its filters, page and open
// submission; empty values stay out of the address.
func (a *Admin) tabURL(formID uuid.UUID, q, status string, page int, open string) string {
	v := url.Values{}
	if q != "" {
		v.Set("q", q)
	}
	if status != "" {
		v.Set("status", status)
	}
	if page > 1 {
		v.Set("page", strconv.Itoa(page))
	}
	if open != "" {
		v.Set("s", open)
	}
	u := a.adminPath + "/forms/design/" + formID.String() + "/submissions"
	if len(v) > 0 {
		u += "?" + v.Encode()
	}
	return u
}

// openSubmission loads the submission ?s= names. A missing or malformed id
// opens nothing; a submission of another form is a 404 (ok false, response
// written). Opening a new submission marks it read, except on a prefetch.
func (a *Admin) openSubmission(w http.ResponseWriter, r *http.Request, fs FormStore, f *form.Form) (*form.FormSubmission, bool) {
	raw := r.URL.Query().Get("s")
	if raw == "" {
		return nil, true
	}
	sid, err := uuid.Parse(raw)
	if err != nil {
		return nil, true
	}
	s, err := fs.GetSubmission(r.Context(), sid)
	if errors.Is(err, form.ErrSubmissionNotFound) {
		return nil, true
	}
	if err != nil {
		a.logger().Error("get submission failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The submission could not be loaded.")
		return nil, false
	}
	if s.FormID != f.ID {
		a.ErrorBack(w, r, http.StatusNotFound, "Submission not found", "That submission does not belong to this form.", a.tabURL(f.ID, "", "", 1, ""), "Back to submissions")
		return nil, false
	}
	// A prerender or prefetch is a GET nobody made on purpose: it must not
	// mark the enquiry read (Chrome sends "Sec-Purpose: prefetch;prerender").
	if s.Status == form.SubmissionStatusNew && !strings.Contains(r.Header.Get("Sec-Purpose"), "prefetch") {
		read := form.SubmissionStatusRead
		if u, err := fs.UpdateSubmission(r.Context(), s.ID, &form.UpdateSubmissionInput{Status: &read}); err == nil && u != nil {
			s.Status = u.Status
		} else if err == nil {
			s.Status = read
		}
	}
	return s, true
}

func (a *Admin) renderSubmissionsTab(w http.ResponseWriter, r *http.Request, f *form.Form) {
	ctx := r.Context()
	fs := a.deps.Growth.Forms
	q, status := submissionSearch(r), submissionStatusParam(r)
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	open, ok := a.openSubmission(w, r, fs, f)
	if !ok {
		return
	}
	subs, total, err := fs.ListSubmissions(ctx, f.ID, &form.ListSubmissionsParams{Status: status, Search: q, Limit: submissionsPerPage, Offset: (page - 1) * submissionsPerPage})
	if err != nil {
		a.logger().Error("list submissions failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The submissions could not be loaded.")
		return
	}
	id := f.ID.String()
	base := a.adminPath + "/forms/design/" + id + "/submissions"
	formQ := "?form=" + id
	bulk := a.adminPath + "/forms/submissions/bulk" + formQ
	query := url.Values{}
	if q != "" {
		query.Set("q", q)
	}
	if status != "" {
		query.Set("status", status)
	}
	now := time.Now()
	t := ui.Table{
		ID: "submissions", Caption: "Submissions of " + f.Name, BaseURL: base, CSRF: csrfToken(ctx),
		Columns: submissionsTabColumns(f),
		Bulk: []ui.BulkAction{{Label: "Mark read", Action: bulk, Value: "read"}, {Label: "Mark as spam", Action: bulk, Value: "spam"},
			{Label: "Delete", Action: bulk, Value: "delete", Danger: true}},
		BulkURL: bulk,
		Page:    ui.Pagination{Page: page, PerPage: submissionsPerPage, Total: total, BaseURL: base, Query: query},
		Empty:   ui.Empty{Icon: "inbox", Title: "No submissions", Body: "Submissions appear here when visitors send this form."},
	}
	if q != "" || status != "" {
		t.Empty = ui.Empty{Icon: "search", Title: "No submissions match", Body: "Try a different word or status.", Actions: []ui.Button{{Label: "Clear filters", Href: base}}}
	}
	ids := make([]string, len(subs))
	for i, s := range subs {
		ids[i] = s.ID.String()
		row := submissionsTabRow(f, s, now, a.tabURL(f.ID, q, status, page, ids[i]))
		if open != nil && s.ID == open.ID {
			row.Attrs["aria-selected"] = "true"
		}
		t.Rows = append(t.Rows, row)
	}
	var drawer *submissionDrawer
	if open != nil {
		drawer = a.buildSubmissionDrawer(ctx, f, open, now, csrfToken(ctx))
		drawer.Back = a.tabURL(f.ID, q, status, page, "")
		for i, sid := range ids {
			if sid != drawer.ID {
				continue
			}
			if i > 0 {
				drawer.PrevURL = a.tabURL(f.ID, q, status, page, ids[i-1])
			}
			if i+1 < len(ids) {
				drawer.NextURL = a.tabURL(f.ID, q, status, page, ids[i+1])
			}
		}
		if userFrom(ctx).Role == "owner" {
			drawer.ShowWebhooks = true
			var names []string
			for _, h := range a.formWebhooks(ctx) {
				names = append(names, h.Name)
			}
			drawer.Webhooks = strings.Join(names, ", ")
		}
	}
	// The drawer script asks for the open submission alone.
	if IsHTMX(r) && r.Header.Get("HX-Target") == "hm-ss-drawer" {
		if drawer == nil {
			http.NotFound(w, r)
			return
		}
		html, err := a.RenderPartial("hm/formssubdrawer", drawer)
		if err != nil {
			a.logger().Error("submission drawer failed", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte(html))
		return
	}
	stats, tracker, err := a.submissionsTabOverview(ctx, fs, f)
	if err != nil {
		a.logger().Error("submission overview failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The submissions could not be loaded.")
		return
	}
	filter := ui.Segmented{Mode: "radio", Label: "Status", Name: "status"}
	for _, o := range []ui.Option{{Value: "", Label: "All"}, {Value: form.SubmissionStatusNew, Label: "Unread"}, {Value: form.SubmissionStatusRead, Label: "Read"}, {Value: form.SubmissionStatusSpam, Label: "Spam"}} {
		filter.Items = append(filter.Items, ui.SegmentedItem{Label: o.Label, Value: o.Value, Pressed: o.Value == status})
	}
	exportQ := formQ
	if status != "" {
		exportQ += "&status=" + url.QueryEscape(status)
	}
	if q != "" {
		exportQ += "&q=" + url.QueryEscape(q)
	}
	exportURL := a.adminPath + "/forms/submissions/export.csv" + exportQ
	v := a.NewView(w, r, "Submissions: "+f.Name)
	v.Crumbs = []Crumb{{Label: "Forms", Href: a.adminPath + "/forms"}, {Label: f.Name, Href: a.adminPath + "/forms/design/" + id}, {Label: "Submissions"}}
	v.Width = "wide"
	v.Data = submissionsTabData{
		formDesignData: formDesignData{ID: id, Name: f.Name, Slug: f.Slug, Status: f.Status, Tabs: a.formDesignTabs(ctx, id, "submissions")},
		Stats:          stats, Tracker: tracker, Filter: filter, Q: q, Status: status, ExportURL: exportURL,
		Actions: []ui.Button{{Label: "Export CSV", Href: exportURL, Icon: "copy"}},
		Summary: fmtNum(int64(total)) + " " + plural(total, "submission", "submissions"),
		Table:   t, Drawer: drawer,
	}
	a.Render(w, r, http.StatusOK, "forms/submissionstab", v)
}
