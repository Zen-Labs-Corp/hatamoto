// Package modmail adapts core's mail funnel to modhost.Mailer, so a
// compiled-in module sends mail that is logged and suppressed like core mail.
package modmail

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/email"
	"hatamoto.dev/core/internal/modhost"
)

// errNoAddress is returned when the user has no stored email address.
var errNoAddress = errors.New("module: user has no email address")

// Sender is the part of the email service the adapter needs.
type Sender interface {
	SendTrackedWith(ctx context.Context, o email.SendOptions, to []string, subject, html, text string) (string, error)
}

// Adapter implements modhost.Mailer. Every send goes through the funnel, which
// owns the email log and the unconfigured, EMAIL_DISABLED and staging
// suppression; the adapter has no mail checks of its own.
type Adapter struct {
	// Sender returns the current mailer; nil means mail is not available.
	Sender func() Sender
	// Owners lists the addresses of the active owners.
	Owners func(ctx context.Context) ([]string, error)
	// UserEmail returns the address of a user; an unknown user is
	// modhost.ErrUnknownUser.
	UserEmail func(ctx context.Context, id uuid.UUID) (string, error)
}

var _ modhost.Mailer = (*Adapter)(nil)

func (a *Adapter) sender() (Sender, error) {
	if a.Sender != nil {
		if s := a.Sender(); s != nil {
			return s, nil
		}
	}
	return nil, modhost.ErrNoMailer
}

func send(ctx context.Context, s Sender, module, to string, m modhost.Message) error {
	_, err := s.SendTrackedWith(ctx, email.SendOptions{Kind: email.KindModule, Plugin: module}, []string{to}, m.Subject, m.HTML, m.Text)
	return err
}

// ToOwners sends one mail to each active owner and attempts every owner even
// after a failure; the failures are joined.
func (a *Adapter) ToOwners(ctx context.Context, module string, m modhost.Message) error {
	snd, err := a.sender()
	if err != nil {
		return err
	}
	owners, err := a.Owners(ctx)
	if err != nil {
		return err
	}
	if len(owners) == 0 {
		return modhost.ErrNoRecipients
	}
	var errs []error
	for _, to := range owners {
		if err := send(ctx, snd, module, to, m); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// ToUser sends one mail to one user.
func (a *Adapter) ToUser(ctx context.Context, module, userID string, m modhost.Message) error {
	snd, err := a.sender()
	if err != nil {
		return err
	}
	id, err := uuid.Parse(userID)
	if err != nil {
		return modhost.ErrUnknownUser
	}
	to, err := a.UserEmail(ctx, id)
	if err != nil {
		return err
	}
	if to == "" {
		return errNoAddress
	}
	return send(ctx, snd, module, to, m)
}
