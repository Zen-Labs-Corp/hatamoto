package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/email"
	"hatamoto.dev/core/internal/emaillog"
	"hatamoto.dev/core/internal/httpx"
)

// EmailLogService is the email log behind Settings > Email log.
type EmailLogService interface {
	List(ctx context.Context, status string, limit, offset int) ([]emaillog.Row, int, error)
	Get(ctx context.Context, id int64) (emaillog.Row, error)
	Retention(ctx context.Context) (bodyDays, entryDays int, err error)
	SetRetention(ctx context.Context, body, entry int) error
}

const (
	emailLogPath = "/settings/email/log"
	emailLogPer  = 25
)

func (a *Admin) mountEmailLog(owner *httpx.Group) {
	owner.HandleFunc("GET "+emailLogPath, a.handleEmailLog)
	owner.HandleFunc("GET "+emailLogPath+"/{id}", a.handleEmailLogEntry)
	owner.HandleFunc("POST "+emailLogPath+"/{id}/resend", a.handleEmailLogResend)
	owner.HandleFunc("POST /settings/email/retention", a.handleEmailRetention)
}

// emailKindLabel is the Kind column text. A plugin's mail names the plugin.
func emailKindLabel(kind, plugin string) string {
	switch email.Kind(kind) {
	case email.KindFormNotification:
		return "Form notification"
	case email.KindFormForward:
		return "Form forward"
	case email.KindFormConfirmation:
		return "Form confirmation"
	case email.KindPasswordReset:
		return "Password reset"
	case email.KindInvite:
		return "Invitation"
	case email.KindAdminAddressChanged:
		return "Admin address changed"
	case email.KindAdminAddressSetup:
		return "Admin address"
	case email.KindTest:
		return "Test email"
	case email.KindRunDigest:
		return "Run digest"
	case email.KindPlugin:
		return "Plugin: " + plugin
	}
	return "Other"
}

func emailStatusBadge(status string) ui.Badge {
	switch status {
	case email.StatusSent:
		return ui.Badge{Label: "Sent", Kind: "success"}
	case email.StatusFailed:
		return ui.Badge{Label: "Failed", Kind: "danger"}
	}
	return ui.Badge{Label: "Suppressed", Kind: "muted"}
}

func joinAddresses(to []string) string {
	out := ""
	for i, t := range to {
		if i > 0 {
			out += ", "
		}
		out += t
	}
	return out
}

func (a *Admin) emailLogService(w http.ResponseWriter, r *http.Request) (EmailLogService, bool) {
	if a.deps.EmailLog == nil {
		a.unavailable(w, r, "The email log")
		return nil, false
	}
	return a.deps.EmailLog, true
}

func (a *Admin) handleEmailLog(w http.ResponseWriter, r *http.Request) {
	svc, ok := a.emailLogService(w, r)
	if !ok {
		return
	}
	status := r.URL.Query().Get("status")
	switch status {
	case email.StatusSent, email.StatusFailed, email.StatusSuppressed:
	default:
		status = ""
	}
	page := pageParam(r)
	rows, total, err := svc.List(r.Context(), status, emailLogPer, (page-1)*emailLogPer)
	if err != nil {
		a.logger().Error("list email log failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The email log could not be loaded.")
		return
	}
	q := url.Values{}
	if status != "" {
		q.Set("status", status)
	}
	base := a.adminPath + emailLogPath
	t := ui.Table{
		ID: "email-log", Caption: "Email log", BaseURL: base, CSRF: csrfToken(r.Context()),
		Columns: []ui.Col{{Key: "when", Label: "When"}, {Key: "to", Label: "To"}, {Key: "subject", Label: "Subject", Role: "title"}, {Key: "kind", Label: "Kind"}, {Key: "status", Label: "Status"}},
		PrimaryFilters: []ui.Filter{{Name: "status", Label: "Status", Options: []ui.Option{
			{Value: "", Label: "All statuses", Selected: status == ""},
			{Value: email.StatusSent, Label: "Sent", Selected: status == email.StatusSent},
			{Value: email.StatusFailed, Label: "Failed", Selected: status == email.StatusFailed},
			{Value: email.StatusSuppressed, Label: "Suppressed", Selected: status == email.StatusSuppressed},
		}}},
		Page:  ui.Pagination{Page: page, PerPage: emailLogPer, Total: total, BaseURL: base, Query: q},
		Empty: ui.Empty{Icon: "mail", Title: "No emails yet", Body: "Every email the server sends, or drops because email is off, appears here."},
	}
	for _, e := range rows {
		badge := emailStatusBadge(e.Status)
		t.Rows = append(t.Rows, ui.Row{ID: strconv.FormatInt(e.ID, 10), Label: e.Subject + " to " + joinAddresses(e.Recipients), Cells: []ui.Cell{
			{Text: fmtDateTime(e.CreatedAt.UTC()), Nowrap: true},
			{Text: joinAddresses(e.Recipients), Clip: true},
			{Text: e.Subject, Link: base + "/" + strconv.FormatInt(e.ID, 10)},
			{Text: emailKindLabel(e.Kind, e.Plugin)},
			{Badge: &badge},
		}})
	}
	v := a.NewView(w, r, "Email log")
	v.Section, v.Tab = "email", "email-log"
	v.Data = struct{ Table ui.Table }{t}
	a.Render(w, r, http.StatusOK, "settings/emaillog", v)
}

// emailLogEntryData is the detail page of one logged mail.
type emailLogEntryData struct {
	ID        int64
	Subject   string
	To        string
	Kind      string
	Status    ui.Badge
	When      string
	Error     string
	ResentOf  string // link to the original entry when this mail is a resend
	BodyNote  string
	Body      string
	CanResend bool
	BackHref  string
	CSRF      string
}

func (a *Admin) handleEmailLogEntry(w http.ResponseWriter, r *http.Request) {
	svc, ok := a.emailLogService(w, r)
	if !ok {
		return
	}
	e, ok := a.loadEmailLogEntry(w, r, svc)
	if !ok {
		return
	}
	bodyDays, _, err := svc.Retention(r.Context())
	if err != nil {
		a.logger().Error("email log retention", "err", err)
		bodyDays = emaillog.DefaultBodyDays
	}
	base := a.adminPath + emailLogPath
	d := emailLogEntryData{
		ID: e.ID, Subject: e.Subject, To: joinAddresses(e.Recipients), Kind: emailKindLabel(e.Kind, e.Plugin),
		Status: emailStatusBadge(e.Status), When: fmtDateTime(e.CreatedAt.UTC()) + " UTC", Error: e.Error,
		BackHref: base, CSRF: csrfToken(r.Context()),
	}
	if e.ResentFrom > 0 {
		d.ResentOf = base + "/" + strconv.FormatInt(e.ResentFrom, 10)
	}
	switch {
	case e.HasBody:
		d.Body = e.BodyText
		if d.Body == "" {
			d.Body = e.BodyHTML
		}
		d.BodyNote = "Body kept until " + e.CreatedAt.UTC().AddDate(0, 0, bodyDays).Format("2 January 2006") + "."
		d.CanResend = true
	case !email.Kind(e.Kind).StoresBody():
		d.BodyNote = "Body not stored. This kind of email carries a private link, so it is never kept."
	default:
		d.BodyNote = "Body no longer kept. It was removed after the retention period."
	}
	if e.Status == email.StatusSuppressed {
		d.BodyNote = "Nothing was sent: email was switched off when this was logged. " + d.BodyNote
		d.CanResend = false
	}
	v := a.NewView(w, r, e.Subject)
	v.Section, v.Tab = "email", "email-log"
	v.Width = "compact"
	v.Data = d
	a.Render(w, r, http.StatusOK, "settings/emaillog-entry", v)
}

func (a *Admin) loadEmailLogEntry(w http.ResponseWriter, r *http.Request, svc EmailLogService) (emaillog.Row, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		a.notFound(w, r, "Email", a.adminPath+emailLogPath, "Back to the email log")
		return emaillog.Row{}, false
	}
	e, err := svc.Get(r.Context(), id)
	switch {
	case errors.Is(err, emaillog.ErrNotFound):
		a.notFound(w, r, "Email", a.adminPath+emailLogPath, "Back to the email log")
		return emaillog.Row{}, false
	case err != nil:
		a.logger().Error("email log entry", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The email could not be loaded.")
		return emaillog.Row{}, false
	}
	return e, true
}

// handleEmailLogResend sends a stored mail again to the same recipients
// through the one send path, which writes the new, linked log row.
func (a *Admin) handleEmailLogResend(w http.ResponseWriter, r *http.Request) {
	svc, ok := a.emailLogService(w, r)
	if !ok {
		return
	}
	e, ok := a.loadEmailLogEntry(w, r, svc)
	if !ok {
		return
	}
	entry := a.adminPath + emailLogPath + "/" + strconv.FormatInt(e.ID, 10)
	switch {
	case e.Status == email.StatusSuppressed:
		a.SetFlash(w, r, "error", "Nothing was sent for this email because email was switched off, so there is nothing to send again.")
		http.Redirect(w, r, entry, http.StatusSeeOther)
		return
	case !e.HasBody:
		a.SetFlash(w, r, "error", "This email has no stored body, so it cannot be sent again.")
		http.Redirect(w, r, entry, http.StatusSeeOther)
		return
	case a.deps.Mailer == nil || !a.deps.Mailer.IsConfigured():
		a.SetFlash(w, r, "error", "Email is not set up, so nothing was sent.")
		http.Redirect(w, r, entry, http.StatusSeeOther)
		return
	}
	opts := email.SendOptions{Kind: email.Kind(e.Kind), Plugin: e.Plugin, ResentFrom: e.ID}
	msgID, err := a.deps.Mailer.SendTrackedWith(r.Context(), opts, e.Recipients, e.Subject, e.BodyHTML, e.BodyText)
	a.audit(r, "email.resend", "email-log", map[string]any{"id": e.ID, "kind": e.Kind})
	switch {
	case err != nil:
		a.logger().Error("email resend failed", "id", e.ID, "err", err)
		a.SetFlash(w, r, "error", "The email could not be sent. The attempt is in the log.")
	case msgID == "":
		a.SetFlash(w, r, "warning", "Email is switched off, so nothing was sent. The attempt is in the log.")
	default:
		a.SetFlash(w, r, "success", "Email sent again to "+joinAddresses(e.Recipients)+".")
	}
	http.Redirect(w, r, a.adminPath+emailLogPath, http.StatusSeeOther)
}

// emailRetention is the retention card as shown: the saved days, or what was
// submitted after a refused save.
type emailRetention struct {
	BodyDays, EntryDays string
	Errs                map[string]string
}

func (a *Admin) loadEmailRetention(r *http.Request) *emailRetention {
	if a.deps.EmailLog == nil {
		return nil
	}
	body, entry, err := a.deps.EmailLog.Retention(r.Context())
	if err != nil {
		a.logger().Error("email log retention", "err", err)
		body, entry = emaillog.DefaultBodyDays, emaillog.DefaultEntryDays
	}
	return &emailRetention{BodyDays: strconv.Itoa(body), EntryDays: strconv.Itoa(entry), Errs: map[string]string{}}
}

func (a *Admin) handleEmailRetention(w http.ResponseWriter, r *http.Request) {
	svc, ok := a.emailLogService(w, r)
	if !ok {
		return
	}
	_ = r.ParseForm() // a malformed body leaves the form empty and fails validation
	rawBody, rawEntry := r.PostForm.Get("body_days"), r.PostForm.Get("entry_days")
	ret := &emailRetention{BodyDays: rawBody, EntryDays: rawEntry, Errs: map[string]string{}}
	var errs []ui.FieldError
	bad := func(id, label, msg string) {
		ret.Errs[id] = msg
		errs = append(errs, ui.FieldError{ID: id, Label: label, Message: msg})
	}
	days := func(raw, id, label string) int {
		n, err := strconv.Atoi(raw)
		if err != nil || n < emaillog.MinDays || n > emaillog.MaxDays {
			bad(id, label, fmt.Sprintf("Enter a whole number of days from %d to %d.", emaillog.MinDays, emaillog.MaxDays))
		}
		return n
	}
	body := days(rawBody, "body-days", "Keep message bodies (days)")
	entry := days(rawEntry, "entry-days", "Keep log entries (days)")
	if len(errs) == 0 && body > entry {
		bad("body-days", "Keep message bodies (days)", "Bodies cannot be kept longer than log entries.")
	}
	if len(errs) == 0 {
		switch err := svc.SetRetention(r.Context(), body, entry); {
		case errors.Is(err, emaillog.ErrRetention):
			bad("body-days", "Keep message bodies (days)", err.Error())
		case err != nil:
			a.logger().Error("email log retention save", "err", err)
			a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The retention settings could not be saved.")
			return
		}
	}
	if len(errs) > 0 {
		_, stored, ok := a.emailView(w, r)
		if !ok {
			return
		}
		a.renderEmail(w, r, http.StatusUnprocessableEntity, stored, emailFormEcho{}, errs, "", ret)
		return
	}
	a.audit(r, "email.retention", "email-settings", map[string]any{"body_days": body, "entry_days": entry})
	a.SetFlash(w, r, "success", "Email log retention saved.")
	http.Redirect(w, r, a.adminPath+emailPath, http.StatusSeeOther)
}
