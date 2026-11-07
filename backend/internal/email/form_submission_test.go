package email

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/email/smtptest"
)

func TestFormSubmissionAdminLinkOnlyWhenSet(t *testing.T) {
	svc := newTestService(t)
	render := func(d FormSubmissionData) (html, text string) {
		var b bytes.Buffer
		if err := svc.templates.ExecuteTemplate(&b, "form_submission.html", d); err != nil {
			t.Fatal(err)
		}
		return b.String(), svc.createPlainTextSubmission(d)
	}
	base := FormSubmissionData{FormName: "Contact", SubmissionID: "s1", SiteName: "Site"}

	html, text := render(base)
	for name, out := range map[string]string{"html": html, "text": text} {
		if strings.Contains(out, "View in Admin") || strings.Contains(out, "/forms/submissions") {
			t.Errorf("%s has an admin link without AdminURL", name)
		}
	}

	base.AdminURL = "/hq-x7k/forms/submissions/s1"
	html, text = render(base)
	for name, out := range map[string]string{"html": html, "text": text} {
		if !strings.Contains(out, "/hq-x7k/forms/submissions/s1") {
			t.Errorf("%s lacks the admin link when AdminURL is set", name)
		}
	}
}

func TestSendFormSubmissionSubjectAndReplyTo(t *testing.T) {
	svc, _, srv := newLoggedSink(t, smtptest.Options{}, nil)
	ctx := context.Background()
	fd := FormSubmissionData{FormName: "Contact", SubmissionID: "12345678-aaaa"}

	if err := svc.SendFormSubmission(ctx, []string{"a@x.test", "b@x.test"}, fd,
		FormNotifyOptions{Subject: "New enquiry from Ada", ReplyTo: "ada@visitor.test"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.SendFormSubmission(ctx, []string{"a@x.test"}, fd, FormNotifyOptions{}); err != nil {
		t.Fatal(err)
	}
	msgs := srv.Messages()
	if len(msgs) != 2 {
		t.Fatalf("messages = %d, want 2", len(msgs))
	}
	custom, plain := msgs[0].Data, msgs[1].Data
	if !strings.Contains(custom, "Subject: New enquiry from Ada\r\n") || !strings.Contains(custom, "Reply-To: ada@visitor.test\r\n") {
		t.Errorf("custom headers missing:\n%s", custom)
	}
	if len(msgs[0].To) != 2 {
		t.Errorf("recipients = %v, want 2", msgs[0].To)
	}
	if !strings.Contains(plain, "Subject: New Form Submission: Contact\r\n") || strings.Contains(plain, "Reply-To:") {
		t.Errorf("defaults changed:\n%s", plain)
	}
}
