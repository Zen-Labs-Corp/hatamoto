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
	KindAccessEnding        Kind = "access_ending"
	KindCommentConfirmation Kind = "comment_confirmation"
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

// StoresBody reports whether the log may keep the bodies of a mail of this
// kind. The kinds that carry a one-time link or the secret admin address
// never do.
func (k Kind) StoresBody() bool {
	switch k {
	case KindPasswordReset, KindInvite, KindAdminAddressChanged, KindAdminAddressSetup, KindCommentConfirmation:
		return false
	}
	return true
}

// StoresRecipients reports whether the log may keep the recipient addresses of
// a mail of this kind. A comment confirmation goes to an address a visitor
// typed, which may not be theirs, so only a masked form is kept.
func (k Kind) StoresRecipients() bool {
	return k != KindCommentConfirmation
}

// maskAddress keeps the first letter and the domain: "a***@example.com".
func maskAddress(addr string) string {
	at := strings.LastIndex(addr, "@")
	if at < 1 {
		return "***"
	}
	return addr[:1] + "***" + addr[at:]
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
func cleanError(err error, c Config, mask map[string]string) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	// Mask addresses before the cap, so a cut cannot leave a fragment.
	for addr, masked := range mask {
		msg = strings.NewReplacer(addr, masked, strings.ToLower(addr), masked).Replace(msg)
	}
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

// scrub returns the recipients and error text the log may keep for a mail of
// this kind: masked where the kind keeps no plaintext address.
func scrub(kind Kind, to []string, sendErr error, c Config) ([]string, string) {
	if kind.StoresRecipients() {
		return to, cleanError(sendErr, c, nil)
	}
	masked := make([]string, len(to))
	mask := make(map[string]string, len(to))
	for i, addr := range to {
		masked[i] = maskAddress(addr)
		mask[addr] = masked[i]
	}
	return masked, cleanError(sendErr, c, mask)
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
	recipients, errText := scrub(kind, to, sendErr, c)
	e := LogEntry{
		Recipients: recipients, Subject: subject, Kind: kind, Plugin: opts.Plugin, Status: status,
		Error: errText, MessageID: messageID, ResentFrom: opts.ResentFrom,
	}
	if kind.StoresBody() {
		e.BodyText, e.BodyHTML = capBody(textBody), capBody(htmlBody)
	}
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), recordTimeout)
	defer cancel()
	if _, err := (*rp).Record(rctx, e); err != nil {
		s.logger.Error("email log write failed", slog.String("error", err.Error()))
	}
}
