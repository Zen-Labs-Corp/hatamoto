package email

import (
	"context"
	"log/slog"
	"strings"
	"time"
)

// Kind labels why a mail was sent. The string is stored in the email log.
type Kind string

// The kinds the log knows.
const (
	KindFormNotification    Kind = "form_notification"
	KindFormForward         Kind = "form_forward"
	KindFormConfirmation    Kind = "form_confirmation"
	KindPasswordReset       Kind = "password_reset"
	KindInvite              Kind = "invite"
	KindAdminAddressChanged Kind = "admin_address_changed"
	KindAdminAddressSetup   Kind = "admin_address_setup"
	KindTest                Kind = "test"
	KindRunDigest           Kind = "run_digest"
	KindPlugin              Kind = "plugin"
	KindOther               Kind = "other"
)

// Log statuses.
const (
	StatusSent       = "sent"
	StatusFailed     = "failed"
	StatusSuppressed = "suppressed"
)

// LogEntry is one row of the email log.
type LogEntry struct {
	Recipients []string
	Subject    string
	Kind       Kind
	Plugin     string
	Status     string
	Error      string
	MessageID  string
	BodyText   string
	BodyHTML   string
	ResentFrom int64
}

// Recorder stores log entries. The email package stays free of a database;
// the server plugs in the store.
type Recorder interface {
	Record(ctx context.Context, e LogEntry) (int64, error)
}

// storesBody reports whether the log may keep the bodies of a mail of this
// kind. The four that carry a one-time link or the secret admin address
// never do.
func (k Kind) storesBody() bool {
	switch k {
	case KindPasswordReset, KindInvite, KindAdminAddressChanged, KindAdminAddressSetup:
		return false
	}
	return true
}

const (
	// maxLoggedBody caps each stored body, the same cap as plugin mail.
	maxLoggedBody = 256 << 10
	// maxLoggedError caps the stored error text.
	maxLoggedError = 300
	// recordTimeout bounds one log write, detached from the send's context.
	recordTimeout = 3 * time.Second
)

// cleanError turns a send error into the text the log keeps: the first line,
// whitespace collapsed, the configured SMTP credentials replaced and capped.
func cleanError(err error, c Config) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	for _, secret := range []string{c.SMTPPassword, c.SMTPUsername} {
		if secret != "" {
			msg = strings.ReplaceAll(msg, secret, "[redacted]")
		}
	}
	if i := strings.IndexAny(msg, "\r\n"); i >= 0 {
		msg = msg[:i]
	}
	msg = strings.Join(strings.Fields(msg), " ")
	if r := []rune(msg); len(r) > maxLoggedError {
		msg = string(r[:maxLoggedError])
	}
	return msg
}

func capBody(s string) string {
	if len(s) <= maxLoggedBody {
		return s
	}
	return strings.ToValidUTF8(s[:maxLoggedBody], "")
}

// record writes one log entry. It is the only writer: SendTrackedWith calls
// it on every exit. A failure is logged and swallowed so a log problem never
// turns a delivered mail into an error.
func (s *Service) record(ctx context.Context, c Config, opts SendOptions, to []string, subject, htmlBody, textBody, messageID, status string, sendErr error) {
	rp := s.recorder.Load()
	if rp == nil {
		return
	}
	kind := opts.Kind
	if kind == "" {
		kind = KindOther
	}
	e := LogEntry{
		Recipients: to, Subject: subject, Kind: kind, Plugin: opts.Plugin, Status: status,
		Error: cleanError(sendErr, c), MessageID: messageID, ResentFrom: opts.ResentFrom,
	}
	if kind.storesBody() {
		e.BodyText, e.BodyHTML = capBody(textBody), capBody(htmlBody)
	}
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), recordTimeout)
	defer cancel()
	if _, err := (*rp).Record(rctx, e); err != nil {
		s.logger.Error("email log write failed", slog.String("error", err.Error()))
	}
}
