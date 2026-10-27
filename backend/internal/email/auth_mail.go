package email

import (
	"bytes"
	"context"
	"fmt"
)

// ResetMailData drives password_reset.html. URL carries the one-time token.
type ResetMailData struct {
	SiteName  string
	URL       string
	ExpiresIn string // "1 hour"
}

// InviteMailData drives invite.html.
type InviteMailData struct {
	SiteName    string
	InviterName string
	Role        string
	URL         string
	ExpiresIn   string // "7 days"
}

// AdminAddressChangedData drives admin_address_changed.html. Address is the
// full URL, or the path alone when the site has no base URL.
type AdminAddressChangedData struct {
	SiteName  string
	Address   string
	ChangedBy string
	When      string // already formatted, UTC
	IP        string
}

// AdminAddressSetupData drives admin_address_setup.html.
type AdminAddressSetupData struct {
	SiteName string
	Address  string
}

func (s *Service) render(name string, data any) (string, error) {
	var buf bytes.Buffer
	if err := s.templates.ExecuteTemplate(&buf, name, data); err != nil {
		return "", fmt.Errorf("render %s: %w", name, err)
	}
	return buf.String(), nil
}

// SendPasswordReset mails the reset link (HTML plus a plain-text part). Like
// every send it is a no-op when the service is unconfigured; callers check
// IsConfigured first when they must tell the user.
func (s *Service) SendPasswordReset(ctx context.Context, to string, data ResetMailData) error {
	html, err := s.render("password_reset.html", data)
	if err != nil {
		return err
	}
	text := fmt.Sprintf("Reset your %s admin password.\n\nSomeone asked to reset it. Open this link to choose a new password (it works once and expires in %s):\n\n%s\n\nIf you did not ask for this, ignore this email.\n",
		data.SiteName, data.ExpiresIn, data.URL)
	return s.sendAuth(ctx, to, data.SiteName, "Reset your password", html, text)
}

// SendInvite mails an invitation link.
func (s *Service) SendInvite(ctx context.Context, to string, data InviteMailData) error {
	html, err := s.render("invite.html", data)
	if err != nil {
		return err
	}
	who := "You are invited"
	if data.InviterName != "" {
		who = data.InviterName + " invited you"
	}
	text := fmt.Sprintf("%s to help run %s as %s.\n\nAccept the invitation to choose your password (the link works once and expires in %s):\n\n%s\n",
		who, data.SiteName, data.Role, data.ExpiresIn, data.URL)
	return s.sendAuth(ctx, to, data.SiteName, "Join "+data.SiteName, html, text)
}

// SendAdminAddressChanged tells an owner the admin address changed.
func (s *Service) SendAdminAddressChanged(ctx context.Context, to string, data AdminAddressChangedData) error {
	html, err := s.render("admin_address_changed.html", data)
	if err != nil {
		return err
	}
	text := fmt.Sprintf("The admin address of %s was changed by %s at %s from %s.\n\nThe new address is:\n\n%s\n\nIf this was not you, run `hatamoto admin-url` on the server to see or reset the address, and change your password.\n",
		data.SiteName, data.ChangedBy, data.When, data.IP, data.Address)
	return s.sendAuth(ctx, to, data.SiteName, "The admin address of "+data.SiteName+" changed", html, text)
}

// SendAdminAddressSetup mails the owner their admin address after setup.
func (s *Service) SendAdminAddressSetup(ctx context.Context, to string, data AdminAddressSetupData) error {
	html, err := s.render("admin_address_setup.html", data)
	if err != nil {
		return err
	}
	text := fmt.Sprintf("This is where you sign in to %s. Keep this email, or bookmark the address.\n\n%s\n\nYou can always see it on the server with `hatamoto admin-url`.\n",
		data.SiteName, data.Address)
	return s.sendAuth(ctx, to, data.SiteName, "Your admin address for "+data.SiteName, html, text)
}

// SendTest sends the "is my SMTP configuration working" message.
func (s *Service) SendTest(ctx context.Context, to, siteName string) error {
	return s.sendAuth(ctx, to, siteName, "Test email from "+siteName,
		"<p>This is a test email from "+htmlEscape(siteName)+". Your SMTP settings work.</p>",
		"This is a test email from "+siteName+". Your SMTP settings work.\n")
}

// sendAuth sends one admin mail. The From name is the site name as the caller
// read it from the database just now (unless SMTP_FROM_NAME pins one), so a
// renamed site does not keep mailing as the name it had at boot.
func (s *Service) sendAuth(ctx context.Context, to, siteName, subject, html, text string) error {
	var opts SendOptions
	if s.cfg.Load().FromNameFromSite {
		opts.FromName = siteName
	}
	_, err := s.SendTrackedWith(ctx, opts, []string{to}, subject, html, text)
	return err
}

func htmlEscape(s string) string {
	var b bytes.Buffer
	for _, r := range s {
		switch r {
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '&':
			b.WriteString("&amp;")
		case '"':
			b.WriteString("&quot;")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
