package modhost

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"unicode/utf8"
)

// Message is a plain mail a module sends through core.
type Message struct {
	Subject string
	Text    string
	HTML    string // optional
}

// Mailer sends module mail through core's mail funnel; core installs it.
type Mailer interface {
	ToOwners(ctx context.Context, module string, m Message) error
	ToUser(ctx context.Context, module, userID string, m Message) error
}

var (
	// ErrNoMailer is returned when core has not installed a mailer.
	ErrNoMailer = errors.New("module: mail is not available in this process")
	// ErrNoRecipients is returned when there is nobody to mail.
	ErrNoRecipients = errors.New("module: no recipients")
	// ErrUnknownUser is returned when the user id matches no user.
	ErrUnknownUser = errors.New("module: unknown user")
)

var mailer atomic.Pointer[Mailer]

func init() { onReset(func() { mailer.Store(nil) }) }

// SetMailer installs the mailer; core only. It panics on a nil mailer.
func SetMailer(m Mailer) {
	if isNil(m) {
		panic("modhost: nil mailer")
	}
	mailer.Store(&m)
}

func validMessage(module string, m Message) error {
	switch {
	case !ValidName(module):
		return fmt.Errorf("module: invalid module name %q", module)
	case m.Subject == "" || utf8.RuneCountInString(m.Subject) > 255:
		return errors.New("module: mail subject must be 1 to 255 characters")
	case strings.ContainsAny(m.Subject, "\r\n"):
		return errors.New("module: mail subject must be a single line")
	case m.Text == "":
		return errors.New("module: mail text is required")
	}
	return nil
}

// MailToOwners mails every active owner.
func MailToOwners(ctx context.Context, module string, m Message) error {
	if err := validMessage(module, m); err != nil {
		return err
	}
	p := mailer.Load()
	if p == nil {
		return ErrNoMailer
	}
	return (*p).ToOwners(ctx, module, m)
}

// MailToUser mails one user.
func MailToUser(ctx context.Context, module, userID string, m Message) error {
	if err := validMessage(module, m); err != nil {
		return err
	}
	p := mailer.Load()
	if p == nil {
		return ErrNoMailer
	}
	return (*p).ToUser(ctx, module, userID, m)
}
