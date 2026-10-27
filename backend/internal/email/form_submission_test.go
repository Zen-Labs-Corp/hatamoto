package email

import (
	"bytes"
	"strings"
	"testing"
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
		if strings.Contains(out, "View in Admin") || strings.Contains(out, "/settings/submissions") {
			t.Errorf("%s has an admin link without AdminURL", name)
		}
	}

	base.AdminURL = "/hq-x7k/settings/submissions/s1"
	html, text = render(base)
	for name, out := range map[string]string{"html": html, "text": text} {
		if !strings.Contains(out, "/hq-x7k/settings/submissions/s1") {
			t.Errorf("%s lacks the admin link when AdminURL is set", name)
		}
	}
}
