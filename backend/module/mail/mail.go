package mail

import (
	"context"

	"hatamoto.dev/core/internal/modhost"
)

// Message is a plain mail: Subject (one line, up to 255 characters) and Text
// are required, HTML is optional.
type Message struct {
	Subject string
	Text    string
	HTML    string // optional
}

func (m Message) internal() modhost.Message {
	return modhost.Message{Subject: m.Subject, Text: m.Text, HTML: m.HTML}
}

var (
	// ErrNoMailer is returned outside the serve process, where mail is not available.
	ErrNoMailer = modhost.ErrNoMailer
	// ErrNoRecipients is returned when there is no active owner to mail.
	ErrNoRecipients = modhost.ErrNoRecipients
	// ErrUnknownUser is returned when the user id matches no user.
	ErrUnknownUser = modhost.ErrUnknownUser
)

// ToOwners mails every active owner, one mail each. A failure for one owner
// does not stop the others; the failures are joined.
func ToOwners(ctx context.Context, module string, m Message) error {
	return modhost.MailToOwners(ctx, module, m.internal())
}

// ToUser mails one user by id.
func ToUser(ctx context.Context, module, userID string, m Message) error {
	return modhost.MailToUser(ctx, module, userID, m.internal())
}
