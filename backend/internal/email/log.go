package email

import "context"

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
