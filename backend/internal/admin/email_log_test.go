package admin

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/admin/a11y"
	"hatamoto.dev/core/internal/email"
	"hatamoto.dev/core/internal/emaillog"
)

// fakeEmailLog is an in-memory EmailLogService.
type fakeEmailLog struct {
	rows       []emaillog.Row
	body, days int
	gotStatus  string
	gotLimit   int
	gotOffset  int
}

func (f *fakeEmailLog) List(_ context.Context, status string, limit, offset int) ([]emaillog.Row, int, error) {
	f.gotStatus, f.gotLimit, f.gotOffset = status, limit, offset
	var out []emaillog.Row
	for _, r := range f.rows {
		if status == "" || r.Status == status {
			out = append(out, r)
		}
	}
	return out, len(out), nil
}

func (f *fakeEmailLog) Get(_ context.Context, id int64) (emaillog.Row, error) {
	for _, r := range f.rows {
		if r.ID == id {
			return r, nil
		}
	}
	return emaillog.Row{}, emaillog.ErrNotFound
}

func (f *fakeEmailLog) Retention(context.Context) (int, int, error) { return f.body, f.days, nil }

func (f *fakeEmailLog) SetRetention(_ context.Context, body, entry int) error {
	if err := emaillog.ValidateRetention(body, entry); err != nil {
		return err
	}
	f.body, f.days = body, entry
	return nil
}

func newEmailLogRig(t *testing.T) (*ownerRig, *fakeEmailLog, *[]AuditEvent) {
	t.Helper()
	g := newOwnerRig(t, true)
	at := time.Date(2026, 10, 1, 9, 30, 0, 0, time.UTC)
	f := &fakeEmailLog{body: 30, days: 90, rows: []emaillog.Row{
		{ID: 3, CreatedAt: at.Add(2 * time.Hour), Recipients: []string{"owner@example.test"}, Subject: "Reset your password", Kind: "password_reset", Status: "sent"},
		{ID: 2, CreatedAt: at.Add(time.Hour), Recipients: []string{"a@example.test", "b@example.test"}, Subject: "New Form Submission: Contact", Kind: "form_notification", Status: "failed",
			Error: "failed to set recipient a@example.test: 550 5.1.1 mailbox unavailable", HasBody: true, BodyText: "Name: Ann\nMessage: Hello", BodyHTML: "<p>Hello</p>"},
		{ID: 1, CreatedAt: at, Recipients: []string{"c@example.test"}, Subject: "Hi from a plugin", Kind: "plugin", Plugin: "acme", Status: "suppressed"},
	}}
	g.a.deps.EmailLog = f
	var events []AuditEvent
	g.a.deps.Audit = func(_ context.Context, e AuditEvent) { events = append(events, e) }
	return g, f, &events
}

func TestEmailLogListGoldenAndFilter(t *testing.T) {
	t.Parallel()

	g, f, _ := newEmailLogRig(t)
	rec := g.do("GET", "/admin/settings/email/log", "owner-token", nil, nil)
	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("GET = %d", rec.Code)
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %s", strings.Join(issues, "; "))
	}
	compareGolden(t, "pages/settings-emaillog.html", normalizePage(body))
	for _, want := range []string{"Plugin: acme", "Password reset", "Suppressed", "Failed", "Sent", "a@example.test, b@example.test", "Email log"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if f.gotLimit != 25 || f.gotOffset != 0 {
		t.Errorf("paging = %d/%d", f.gotLimit, f.gotOffset)
	}

	body = g.do("GET", "/admin/settings/email/log?status=failed&page=2", "owner-token", nil, nil).Body.String()
	if f.gotStatus != "failed" || f.gotOffset != 25 {
		t.Errorf("asked status %q offset %d", f.gotStatus, f.gotOffset)
	}
	body = g.do("GET", "/admin/settings/email/log?status=failed", "owner-token", nil, nil).Body.String()
	if !strings.Contains(body, "New Form Submission: Contact") || strings.Contains(body, "Reset your password") || strings.Contains(body, "Hi from a plugin") {
		t.Errorf("status=failed must list only the failed row:\n%s", body)
	}
	if !strings.Contains(body, `method="get"`) {
		t.Error("the filter is not a plain GET form")
	}
}

func TestEmailLogOwnerOnlyAndUnavailable(t *testing.T) {
	t.Parallel()

	g, _, _ := newEmailLogRig(t)
	for _, p := range []string{"/admin/settings/email/log", "/admin/settings/email/log/2"} {
		if rec := g.do("GET", p, "editor-token", nil, nil); rec.Code != 403 {
			t.Errorf("editor GET %s = %d, want 403", p, rec.Code)
		}
	}
	if rec := g.do("POST", "/admin/settings/email/log/2/resend", "editor-token", url.Values{}, nil); rec.Code != 403 {
		t.Errorf("editor resend = %d, want 403", rec.Code)
	}
	if rec := g.do("POST", "/admin/settings/email/retention", "editor-token", url.Values{}, nil); rec.Code != 403 {
		t.Errorf("editor retention = %d, want 403", rec.Code)
	}
	g.a.deps.EmailLog = nil
	if rec := g.do("GET", "/admin/settings/email/log", "owner-token", nil, nil); rec.Code != 503 {
		t.Errorf("no service = %d, want 503", rec.Code)
	}
}

func TestEmailLogEntryGoldens(t *testing.T) {
	t.Parallel()

	g, _, _ := newEmailLogRig(t)
	rec := g.do("GET", "/admin/settings/email/log/2", "owner-token", nil, nil)
	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("failed entry = %d", rec.Code)
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %s", strings.Join(issues, "; "))
	}
	compareGolden(t, "pages/settings-emaillog-entry-failed.html", normalizePage(body))
	for _, want := range []string{"550 5.1.1 mailbox unavailable", "Name: Ann", "Body kept until 31 October 2026", `action="/admin/settings/email/log/2/resend"`, ">Resend<"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}

	rec = g.do("GET", "/admin/settings/email/log/3", "owner-token", nil, nil)
	body = rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("reset entry = %d", rec.Code)
	}
	compareGolden(t, "pages/settings-emaillog-entry-reset.html", normalizePage(body))
	if !strings.Contains(body, "Body not stored") || strings.Contains(body, "Resend") {
		t.Error("a password reset must say Body not stored and offer no Resend")
	}

	body = g.do("GET", "/admin/settings/email/log/1", "owner-token", nil, nil).Body.String()
	if !strings.Contains(body, "Body no longer kept") || strings.Contains(body, ">Resend<") {
		t.Error("a purged body must say Body no longer kept and offer no Resend")
	}
	if rec := g.do("GET", "/admin/settings/email/log/99", "owner-token", nil, nil); rec.Code != 404 {
		t.Errorf("unknown id = %d, want 404", rec.Code)
	}
	if rec := g.do("GET", "/admin/settings/email/log/abc", "owner-token", nil, nil); rec.Code != 404 {
		t.Errorf("bad id = %d, want 404", rec.Code)
	}
}

func TestEmailLogResend(t *testing.T) {
	t.Parallel()

	g, _, events := newEmailLogRig(t)
	g.mail.trackedID = "msg-1@example.test"
	rec := g.do("POST", "/admin/settings/email/log/2/resend", "owner-token", url.Values{}, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/settings/email/log" {
		t.Fatalf("resend = %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if len(g.mail.tracked) != 1 {
		t.Fatalf("sent %d, want 1", len(g.mail.tracked))
	}
	m := g.mail.tracked[0]
	if strings.Join(m.to, ",") != "a@example.test,b@example.test" || m.subject != "New Form Submission: Contact" || m.text != "Name: Ann\nMessage: Hello" || m.html != "<p>Hello</p>" {
		t.Errorf("sent %+v", m)
	}
	if m.opts.Kind != email.KindFormNotification || m.opts.ResentFrom != 2 {
		t.Errorf("opts = %+v", m.opts)
	}
	if len(*events) != 1 || (*events)[0].Action != "email.resend" || (*events)[0].Detail["id"] != int64(2) || (*events)[0].Detail["kind"] != "form_notification" {
		t.Errorf("audit = %+v", *events)
	}
	for _, v := range (*events)[0].Detail {
		if s, ok := v.(string); ok && strings.Contains(s, "Ann") {
			t.Error("the audit carries the body")
		}
	}

	// A body-less row is refused, and nothing is sent.
	g.mail.tracked = nil
	for _, id := range []string{"1", "3"} {
		if rec := g.do("POST", "/admin/settings/email/log/"+id+"/resend", "owner-token", url.Values{}, nil); rec.Code != 303 {
			t.Errorf("resend %s = %d", id, rec.Code)
		}
	}
	if len(g.mail.tracked) != 0 {
		t.Error("a row without a body was sent")
	}
	if rec := g.do("POST", "/admin/settings/email/log/99/resend", "owner-token", url.Values{}, nil); rec.Code != 404 {
		t.Errorf("unknown id = %d, want 404", rec.Code)
	}

	// A refused send still redirects with the error and writes no second audit-free path.
	g.mail.trackedErr = errors.New("boom")
	if rec := g.do("POST", "/admin/settings/email/log/2/resend", "owner-token", url.Values{}, nil); rec.Code != 303 {
		t.Errorf("failed resend = %d", rec.Code)
	}
	g.mail.configured = false
	n := len(g.mail.tracked)
	g.do("POST", "/admin/settings/email/log/2/resend", "owner-token", url.Values{}, nil)
	if len(g.mail.tracked) != n {
		t.Error("sent while email is not configured")
	}
}

func TestEmailRetentionForm(t *testing.T) {
	t.Parallel()

	g, f, events := newEmailLogRig(t)
	g.a.deps.EmailSettings = &fakeEmailSettings{}
	body := g.do("GET", "/admin/settings/email", "owner-token", nil, nil).Body.String()
	for _, want := range []string{"Keep message bodies (days)", "Keep log entries (days)", `value="30"`, `value="90"`, "Email log"} {
		if !strings.Contains(body, want) {
			t.Errorf("email page lacks %q", want)
		}
	}
	if strings.Count(body, "<section") != strings.Count(body, "</section>") || strings.Count(body, "<div") != strings.Count(body, "</div>") {
		t.Errorf("email page markup is unbalanced: %d/%d sections, %d/%d divs", strings.Count(body, "<section"), strings.Count(body, "</section>"), strings.Count(body, "<div"), strings.Count(body, "</div>"))
	}
	compareGolden(t, "pages/settings-email-retention.html", normalizePage(body))

	for _, bad := range []url.Values{
		{"body_days": {"0"}, "entry_days": {"90"}},
		{"body_days": {"30"}, "entry_days": {"366"}},
		{"body_days": {"x"}, "entry_days": {"90"}},
		{"body_days": {"60"}, "entry_days": {"30"}},
	} {
		rec := g.do("POST", "/admin/settings/email/retention", "owner-token", bad, nil)
		if rec.Code != 422 || !strings.Contains(rec.Body.String(), "hm-formerrors") {
			t.Errorf("%v = %d, want 422 with errors", bad, rec.Code)
		}
	}
	if f.body != 30 || f.days != 90 || len(*events) != 0 {
		t.Errorf("a refused save changed state: %d/%d, %d events", f.body, f.days, len(*events))
	}

	rec := g.do("POST", "/admin/settings/email/retention", "owner-token", url.Values{"body_days": {"7"}, "entry_days": {"14"}}, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/settings/email" {
		t.Fatalf("save = %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if f.body != 7 || f.days != 14 {
		t.Errorf("saved %d/%d", f.body, f.days)
	}
	if len(*events) != 1 || (*events)[0].Action != "email.retention" || (*events)[0].Detail["body_days"] != 7 || (*events)[0].Detail["entry_days"] != 14 {
		t.Errorf("audit = %+v", *events)
	}
}

func TestEmailLogSuppressedHasNoResend(t *testing.T) {
	t.Parallel()

	g, f, _ := newEmailLogRig(t)
	f.rows = append(f.rows, emaillog.Row{ID: 4, CreatedAt: time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC), Recipients: []string{"d@example.test"},
		Subject: "Dropped", Kind: "form_notification", Status: "suppressed", HasBody: true, BodyText: "Hello"})
	g.mail.trackedID = "msg-1@example.test"

	body := g.do("GET", "/admin/settings/email/log/4", "owner-token", nil, nil).Body.String()
	if !strings.Contains(body, "Nothing was sent") || strings.Contains(body, ">Resend<") || strings.Contains(body, `/log/4/resend`) {
		t.Error("a suppressed entry must say nothing was sent and offer no Resend")
	}
	rec := g.do("POST", "/admin/settings/email/log/4/resend", "owner-token", url.Values{}, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/settings/email/log/4" {
		t.Errorf("suppressed resend = %d %s, want a redirect back to the entry", rec.Code, rec.Header().Get("Location"))
	}
	if len(g.mail.tracked) != 0 {
		t.Error("a suppressed row was sent")
	}
}
