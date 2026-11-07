package server

import (
	"slices"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/form"
)

func notifyForm(notify, subject, replyTo string) *form.Form {
	f := &form.Form{
		Name: "Contact",
		Fields: []form.FormField{
			{Name: "first_name", Type: form.FieldTypeText},
			{Name: "email", Type: form.FieldTypeEmail},
			{Name: "city", Type: form.FieldTypeText},
		},
		Settings: form.FormSettings{NotifySubject: subject, NotifyReplyTo: replyTo},
	}
	if notify != "" {
		f.Settings.NotifyEmail = &notify
	}
	return f
}

func TestFormNotificationBuilder(t *testing.T) {
	t.Parallel()
	data := map[string]any{"first_name": "Ada", "email": "ada@visitor.test", "city": "Oslo"}

	t.Run("two recipients, subject tokens, reply-to", func(t *testing.T) {
		t.Parallel()
		n := buildFormNotification(notifyForm("a@x.test, b@y.test", "New enquiry from {{first_name}}", "email"), data, "site@x.test")
		if !slices.Equal(n.To, []string{"a@x.test", "b@y.test"}) {
			t.Errorf("To = %v", n.To)
		}
		if n.Subject != "New enquiry from Ada" {
			t.Errorf("Subject = %q", n.Subject)
		}
		if n.ReplyTo != "ada@visitor.test" {
			t.Errorf("ReplyTo = %q", n.ReplyTo)
		}
	})

	t.Run("empty recipients fall back to the site address", func(t *testing.T) {
		t.Parallel()
		for _, notify := range []string{"", " , ", "not-an-address"} {
			n := buildFormNotification(notifyForm(notify, "", ""), data, "site@x.test")
			if !slices.Equal(n.To, []string{"site@x.test"}) {
				t.Errorf("notify %q: To = %v", notify, n.To)
			}
		}
	})

	t.Run("no recipient at all", func(t *testing.T) {
		t.Parallel()
		if n := buildFormNotification(notifyForm("", "", ""), data, ""); n.To != nil {
			t.Errorf("To = %v, want nil", n.To)
		}
	})

	t.Run("defaults keep subject and reply-to empty", func(t *testing.T) {
		t.Parallel()
		n := buildFormNotification(notifyForm("a@x.test", "", ""), data, "")
		if n.Subject != "" || n.ReplyTo != "" {
			t.Errorf("got subject %q reply-to %q", n.Subject, n.ReplyTo)
		}
	})

	t.Run("reply-to must be an email field", func(t *testing.T) {
		t.Parallel()
		d := map[string]any{"city": "bob@x.test"}
		if n := buildFormNotification(notifyForm("a@x.test", "", "city"), d, ""); n.ReplyTo != "" {
			t.Errorf("text field used as reply-to: %q", n.ReplyTo)
		}
	})

	t.Run("CR/LF never reaches the headers", func(t *testing.T) {
		t.Parallel()
		d := map[string]any{"first_name": "Ada\r\nBcc: evil@x.test", "email": "ada@v.test\r\nBcc: evil@x.test"}
		n := buildFormNotification(notifyForm("a@x.test", "Hi {{first_name}}", "email"), d, "")
		if n.ReplyTo != "" {
			t.Errorf("ReplyTo = %q, want empty", n.ReplyTo)
		}
		if strings.ContainsAny(n.Subject, "\r\n") {
			t.Errorf("Subject carries CR/LF: %q", n.Subject)
		}
	})

	t.Run("display-name reply-to is refused", func(t *testing.T) {
		t.Parallel()
		d := map[string]any{"email": "Eve <eve@x.test>"}
		if n := buildFormNotification(notifyForm("a@x.test", "", "email"), d, ""); n.ReplyTo != "" {
			t.Errorf("ReplyTo = %q", n.ReplyTo)
		}
	})
}
