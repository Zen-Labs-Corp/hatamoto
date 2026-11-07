package server

import (
	"net/mail"
	"strings"

	"hatamoto.dev/core/internal/email"
	"hatamoto.dev/core/internal/form"
)

// formNotification is who a form submission is mailed to and how the mail is
// headed. Subject and ReplyTo are safe for a mail header; an empty value keeps
// the mailer's default.
type formNotification struct {
	To      []string
	Subject string
	ReplyTo string
}

// buildFormNotification works out the notification for one submission. The
// recipients are the form's notify list, or siteAddr when the list holds no
// valid address; To is nil when neither does (nothing to send). The subject
// fills {{field_name}} tokens from the submitted data, and Reply-To is the
// visitor's address from the form's chosen email field.
func buildFormNotification(f *form.Form, data map[string]any, siteAddr string) formNotification {
	var n formNotification
	if f.Settings.NotifyEmail != nil {
		n.To = validAddresses(strings.Split(*f.Settings.NotifyEmail, ","))
	}
	if len(n.To) == 0 {
		n.To = validAddresses([]string{siteAddr})
	}

	if tpl := f.Settings.NotifySubject; tpl != "" {
		if subject, ok := email.SanitizeHeader(form.FillTokens(tpl, f.Fields, data)); ok {
			n.Subject = subject
		}
	}

	if name := f.Settings.NotifyReplyTo; name != "" {
		for _, field := range f.Fields {
			if field.Name != name || field.Type != form.FieldTypeEmail {
				continue
			}
			raw, _ := data[name].(string)
			raw = strings.TrimSpace(raw)
			if addr, err := mail.ParseAddress(raw); err == nil && addr.Address == raw && !strings.ContainsAny(raw, "\r\n") {
				n.ReplyTo = raw
			}
			break
		}
	}
	return n
}

// validAddresses keeps the trimmed entries that parse as bare addresses.
func validAddresses(in []string) []string {
	var out []string
	for _, a := range in {
		a = strings.TrimSpace(a)
		if addr, err := mail.ParseAddress(a); err == nil && addr.Address == a {
			out = append(out, a)
		}
	}
	return out
}
