// Package email provides email sending functionality for the application.
package email

import (
	"bytes"
	"context"
	"crypto/x509"
	"embed"
	"fmt"
	"html/template"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

// sendTimeout bounds one whole send (connect, TLS, auth and transfer).
const sendTimeout = 30 * time.Second

//go:embed templates/*.html
var templateFS embed.FS

// Config holds SMTP configuration for the email service.
type Config struct {
	SMTPHost     string
	SMTPPort     int
	SMTPUsername string
	SMTPPassword string
	FromAddress  string
	FromName     string
	// FromNameFromSite makes the admin's own mails (reset, invite, test) use the
	// site name they carry as the From name instead of FromName.
	FromNameFromSite bool
	// Security selects how the connection is protected. The zero value means
	// SecurityStarttls.
	Security Security
	// AllowLocalhost makes the host "localhost" count as configured (a local
	// mail sink). Set only for settings saved in the admin; env keeps the
	// historical rule that "localhost" means "not set up".
	AllowLocalhost bool
	// RootCAs overrides the system roots when verifying the server
	// certificate. Nil means system roots; tests use it for self-signed certs.
	RootCAs *x509.CertPool
}

// Security is the transport protection used for an SMTP connection.
type Security string

const (
	// SecurityStarttls connects in plain text and upgrades with STARTTLS
	// (typically port 587). It is the default.
	SecurityStarttls Security = "starttls"
	// SecurityTLS wraps the connection in TLS from the first byte
	// (implicit TLS, typically port 465).
	SecurityTLS Security = "tls"
	// SecurityNone sends in plain text, for local sinks.
	SecurityNone Security = "none"
)

// Valid reports whether s is a known security mode. The zero value is valid
// and means SecurityStarttls.
func (s Security) Valid() bool {
	switch s {
	case "", SecurityStarttls, SecurityTLS, SecurityNone:
		return true
	}
	return false
}

// Service provides email sending functionality.
//
// The configuration can be swapped in place with Reload, so every holder of
// the same *Service sees the change. Each public method reads one snapshot.
type Service struct {
	cfg       atomic.Pointer[Config]
	siteName  atomic.Pointer[func(context.Context) string]
	logger    *slog.Logger
	templates *template.Template
}

// NewService creates a new email service with the given configuration.
func NewService(cfg Config, logger *slog.Logger) (*Service, error) {
	// Parse embedded templates
	tmpl, err := template.New("").Funcs(template.FuncMap{
		"formatTime": func(t time.Time) string {
			return t.Format("January 2, 2006 at 3:04 PM MST")
		},
		"formatDateTime": func(t time.Time) string {
			return t.Format("January 2, 2006 3:04 PM")
		},
		"formatDate": func(t time.Time) string {
			return t.Format("January 2, 2006")
		},
		"nl2br": func(s string) template.HTML {
			escaped := template.HTMLEscapeString(s)
			return template.HTML(strings.ReplaceAll(escaped, "\n", "<br>"))
		},
		"formatScore": func(f float64) string {
			return fmt.Sprintf("%.1f", f)
		},
		"formatPercent": func(f float64) string {
			return fmt.Sprintf("%.1f%%", f)
		},
		"trendColor": func(trend string) string {
			switch trend {
			case "up":
				return "#16a34a"
			case "down":
				return "#dc2626"
			default:
				return "#6b7280"
			}
		},
		"trendIcon": func(trend string) string {
			switch trend {
			case "up":
				return "&#8593;"
			case "down":
				return "&#8595;"
			default:
				return "&#8596;"
			}
		},
		"ge": func(a, b float64) bool {
			return a >= b
		},
		"gt": func(a, b float64) bool {
			return a > b
		},
		"changeColor": func(change float64) string {
			if change > 0 {
				return "#16a34a" // green
			} else if change < 0 {
				return "#dc2626" // red
			}
			return "#6b7280" // gray
		},
		"changePrefix": func(change float64) string {
			if change > 0 {
				return "+"
			}
			return ""
		},
		"add": func(a, b int) int {
			return a + b
		},
	}).ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("failed to parse email templates: %w", err)
	}

	s := &Service{
		logger:    logger,
		templates: tmpl,
	}
	s.cfg.Store(&cfg)
	return s, nil
}

func (c *Config) configured() bool {
	return c.SMTPHost != "" && (c.AllowLocalhost || c.SMTPHost != "localhost")
}

// Reload replaces the SMTP configuration. Sends already in flight keep the
// snapshot they started with.
func (s *Service) Reload(c Config) { s.cfg.Store(&c) }

// Config returns a copy of the current configuration snapshot.
func (s *Service) Config() Config { return *s.cfg.Load() }

// IsConfigured returns true if the email service has SMTP credentials configured.
func (s *Service) IsConfigured() bool {
	c := s.cfg.Load()
	return c.configured()
}

// SetSiteNameSource installs the lookup for the owner's stored site name. With
// Config.FromNameFromSite it is asked at send time, so a rename applies to all
// mail without re-applying the configuration. Config.FromName is the fallback
// when the lookup returns nothing.
func (s *Service) SetSiteNameSource(fn func(context.Context) string) {
	s.siteName.Store(&fn)
}

// siteFromName is the send-time From name for FromNameFromSite configs, or ""
// to keep Config.FromName.
func (s *Service) siteFromName(ctx context.Context, c Config) string {
	if !c.FromNameFromSite {
		return ""
	}
	if fn := s.siteName.Load(); fn != nil && *fn != nil {
		return (*fn)(ctx)
	}
	return ""
}

// FromName exposes the From-header display name. Plugins that render their own
// templates use this to keep the From identity uniform with core sends. It
// follows the stored site name when the config asks for that; otherwise it is
// whatever was configured — empty if unset.
func (s *Service) FromName() string {
	c := *s.cfg.Load()
	if n := s.siteFromName(context.Background(), c); n != "" {
		return n
	}
	return c.FromName
}

// FromAddress exposes the configured From-header address. Same rationale
// as FromName — plugin templates render it into footers / CAN-SPAM lines.
func (s *Service) FromAddress() string { return s.cfg.Load().FromAddress }

// SanitizeHeader validates a single string as safe to substitute into an
// email header or subject line. CR/LF characters are the SMTP injection
// vector — if the input contains either, the second return value is false
// and callers MUST reject the entire request rather than try to scrub the
// string (truncating after the CR/LF would just hide the attack from a
// log review).
//
// Clean inputs are returned verbatim — no trimming, no case change. The
// caller decides on further normalisation.
func SanitizeHeader(v string) (string, bool) {
	if strings.ContainsAny(v, "\r\n") {
		return "", false
	}
	return v, true
}

// FormSubmissionData contains data for the form submission email template.
// SiteName is rendered into the shared base.html (<title>, header, footer
// attribution) and the form-submission footer line. Callers MUST populate
// it from cfg.Hatamoto.SiteName — empty string would surface as a blank
// brand in the email.
type FormSubmissionData struct {
	FormName      string
	FormSlug      string
	SubmissionID  string
	SubmittedAt   time.Time
	Fields        []FormFieldData
	SubmitterIP   string
	SubmitterPage string
	AdminURL      string
	SiteName      string
}

// FormFieldData represents a single field in the form submission.
type FormFieldData struct {
	Label string
	Value string
}

// SendFormSubmission sends a notification email for a new form submission.
func (s *Service) SendFormSubmission(ctx context.Context, to []string, data FormSubmissionData) error {
	if len(to) == 0 {
		s.logger.Debug("no recipients for form submission email, skipping")
		return nil
	}

	// Render HTML body
	var htmlBuf bytes.Buffer
	if err := s.templates.ExecuteTemplate(&htmlBuf, "form_submission.html", data); err != nil {
		return fmt.Errorf("failed to render form submission template: %w", err)
	}

	// Create plain text version
	textBody := s.createPlainTextSubmission(data)

	subject := fmt.Sprintf("New Form Submission: %s", data.FormName)

	return s.Send(ctx, to, subject, htmlBuf.String(), textBody)
}

// SendFormSubmissionForward sends a forwarded form submission to an arbitrary recipient.
func (s *Service) SendFormSubmissionForward(ctx context.Context, to []string, data FormSubmissionData, forwardedBy string) error {
	if len(to) == 0 {
		return nil
	}

	var htmlBuf bytes.Buffer
	if err := s.templates.ExecuteTemplate(&htmlBuf, "form_submission.html", data); err != nil {
		return fmt.Errorf("failed to render form submission template: %w", err)
	}

	textBody := s.createPlainTextSubmission(data)

	subject := fmt.Sprintf("Fwd: Form Submission — %s (#%s)", data.FormName, data.SubmissionID[:8])
	if forwardedBy != "" {
		textBody = fmt.Sprintf("Forwarded by: %s\n\n%s", forwardedBy, textBody)
	}

	return s.Send(ctx, to, subject, htmlBuf.String(), textBody)
}

// createPlainTextSubmission creates a plain text version of the form submission email.
func (s *Service) createPlainTextSubmission(data FormSubmissionData) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("New Form Submission: %s\n", data.FormName))
	sb.WriteString(strings.Repeat("=", 50))
	sb.WriteString("\n\n")

	sb.WriteString(fmt.Sprintf("Submitted: %s\n", data.SubmittedAt.Format("January 2, 2006 at 3:04 PM MST")))
	sb.WriteString(fmt.Sprintf("Submission ID: %s\n\n", data.SubmissionID))

	sb.WriteString("Form Data:\n")
	sb.WriteString(strings.Repeat("-", 30))
	sb.WriteString("\n")

	for _, field := range data.Fields {
		sb.WriteString(fmt.Sprintf("%s: %s\n", field.Label, field.Value))
	}

	sb.WriteString("\n")
	sb.WriteString("Submission Details:\n")
	sb.WriteString(strings.Repeat("-", 30))
	sb.WriteString("\n")

	if data.SubmitterIP != "" {
		sb.WriteString(fmt.Sprintf("IP Address: %s\n", data.SubmitterIP))
	}
	if data.SubmitterPage != "" {
		sb.WriteString(fmt.Sprintf("Page URL: %s\n", data.SubmitterPage))
	}

	if data.AdminURL != "" {
		sb.WriteString(fmt.Sprintf("\nView in Admin: %s\n", data.AdminURL))
	}

	return sb.String()
}

// Send sends an email with both HTML and plain text bodies. Returns nil
// on success or when the service is unconfigured / disabled.
//
// Send is the entrypoint for callers that don't need the Message-ID.
func (s *Service) Send(ctx context.Context, to []string, subject, htmlBody, textBody string) error {
	_, err := s.SendTracked(ctx, to, subject, htmlBody, textBody)
	return err
}

// SendOptions overrides per-call envelope/header fields. Any empty field
// falls back to the service's configured default. Used by callers that
// need a different From identity or a Reply-To header for a specific
// send.
type SendOptions struct {
	FromAddress string
	FromName    string
	ReplyTo     string
}

// SendTracked is the Send variant that returns the generated Message-ID
// header value.
//
// An empty return + nil error means the message was suppressed (service
// unconfigured or EMAIL_DISABLED=true).
func (s *Service) SendTracked(ctx context.Context, to []string, subject, htmlBody, textBody string) (string, error) {
	return s.SendTrackedWith(ctx, SendOptions{}, to, subject, htmlBody, textBody)
}

// SendTrackedWith is the override-aware variant of SendTracked. Pass
// SendOptions{} for default behaviour identical to SendTracked.
func (s *Service) SendTrackedWith(ctx context.Context, opts SendOptions, to []string, subject, htmlBody, textBody string) (string, error) {
	c := *s.cfg.Load()
	if opts.FromName == "" {
		opts.FromName = s.siteFromName(ctx, c)
	}
	if !c.configured() {
		s.logger.Info("email service not configured, dropping send",
			slog.String("subject", subject),
			slog.Int("recipient_count", len(to)),
		)
		return "", nil
	}

	// Kill switch: set EMAIL_DISABLED=true on the server to short-circuit
	// all sends without erroring. Used during multi-phase development when
	// pages the email links to are still being built — prevents real leads
	// from getting links to half-finished surfaces. Toggle off when done.
	if os.Getenv("EMAIL_DISABLED") == "true" {
		s.logger.Warn("email disabled by EMAIL_DISABLED env var, dropping send",
			slog.String("subject", subject),
			slog.Int("recipient_count", len(to)),
		)
		return "", nil
	}

	messageID := s.newMessageID(c)

	// Resolve effective From / Reply-To: per-call overrides win, else
	// fall back to service defaults. The SMTP envelope MAIL FROM must be
	// on a verified SES identity; SES with Custom MAIL FROM configured
	// rewrites the bounce return-path automatically.
	envelopeFrom := opts.FromAddress
	if envelopeFrom == "" {
		envelopeFrom = c.FromAddress
	}

	// Build message
	msg := s.buildMIMEMessage(c, to, subject, htmlBody, textBody, messageID, opts)

	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	if err := s.deliver(ctx, c, envelopeFrom, to, msg); err != nil {
		s.logger.Error("failed to send email",
			slog.String("error", err.Error()),
			slog.String("subject", subject),
		)
		return "", fmt.Errorf("failed to send email: %w", err)
	}

	s.logger.Info("email sent successfully",
		slog.String("subject", subject),
		slog.Int("recipient_count", len(to)),
		slog.String("message_id", messageID),
	)

	return messageID, nil
}

// deliver sends one message over a fresh connection using the snapshot c,
// with a caller-supplied envelope MAIL FROM so callers can override the
// sender per send.
func (s *Service) deliver(ctx context.Context, c Config, envelopeFrom string, to []string, msg []byte) error {
	client, closeConn, err := dial(ctx, c)
	if err != nil {
		return err
	}
	defer closeConn()

	if err := client.Mail(envelopeFrom); err != nil {
		return fmt.Errorf("failed to set sender: %w", err)
	}
	for _, recipient := range to {
		if err := client.Rcpt(recipient); err != nil {
			return fmt.Errorf("failed to set recipient %s: %w", recipient, err)
		}
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("failed to open data writer: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("failed to write message: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("failed to close data writer: %w", err)
	}
	return client.Quit()
}

// newMessageID returns a fresh Message-ID value (without angle brackets),
// with the sender address's domain as the right-hand side.
func (s *Service) newMessageID(c Config) string {
	domain := "localhost"
	if _, d, ok := strings.Cut(c.FromAddress, "@"); ok && d != "" {
		domain = d
	}
	return fmt.Sprintf("%s@%s", uuid.NewString(), domain)
}

// buildMIMEMessage creates a MIME multipart email message with HTML and
// plain text parts. A non-empty messageID is injected as the Message-ID
// header.
//
// opts.FromAddress / FromName override the service defaults for the
// From header; opts.ReplyTo adds a Reply-To header when non-empty.
func (s *Service) buildMIMEMessage(c Config, to []string, subject, htmlBody, textBody, messageID string, opts SendOptions) []byte {
	boundary := "----=_Part_0_1234567890"

	var msg bytes.Buffer

	// Resolve From header — per-call override wins, fall back to defaults.
	fromAddr := opts.FromAddress
	if fromAddr == "" {
		fromAddr = c.FromAddress
	}
	fromName := opts.FromName
	if fromName == "" {
		fromName = c.FromName
	}
	from := fromAddr
	if fromName != "" {
		from = fmt.Sprintf("%s <%s>", fromName, fromAddr)
	}

	msg.WriteString(fmt.Sprintf("From: %s\r\n", from))
	if opts.ReplyTo != "" {
		msg.WriteString(fmt.Sprintf("Reply-To: %s\r\n", opts.ReplyTo))
	}
	msg.WriteString(fmt.Sprintf("To: %s\r\n", strings.Join(to, ", ")))
	msg.WriteString(fmt.Sprintf("Subject: %s\r\n", subject))
	msg.WriteString("MIME-Version: 1.0\r\n")
	// Message-ID, wrapped in angle-brackets per RFC 5322.
	if messageID != "" {
		msg.WriteString(fmt.Sprintf("Message-ID: <%s>\r\n", messageID))
	}
	msg.WriteString(fmt.Sprintf("Content-Type: multipart/alternative; boundary=\"%s\"\r\n", boundary))
	msg.WriteString("\r\n")

	// Plain text part.
	// Content-Transfer-Encoding: 8bit (not quoted-printable). We were
	// declaring quoted-printable but writing raw UTF-8 — any literal '='
	// in the body (e.g. inside a URL query string like ?token=...) is
	// the QP escape character and gets corrupted by every downstream QP
	// decoder. Either switch to 8bit (this) or wrap the body in
	// quotedprintable.NewWriter. 8bit is simpler and Gmail/Outlook
	// all support it via the 8BITMIME SMTP extension.
	msg.WriteString(fmt.Sprintf("--%s\r\n", boundary))
	msg.WriteString("Content-Type: text/plain; charset=\"UTF-8\"\r\n")
	msg.WriteString("Content-Transfer-Encoding: 8bit\r\n")
	msg.WriteString("\r\n")
	msg.WriteString(textBody)
	msg.WriteString("\r\n")

	// HTML part — same encoding rationale as above.
	msg.WriteString(fmt.Sprintf("--%s\r\n", boundary))
	msg.WriteString("Content-Type: text/html; charset=\"UTF-8\"\r\n")
	msg.WriteString("Content-Transfer-Encoding: 8bit\r\n")
	msg.WriteString("\r\n")
	msg.WriteString(htmlBody)
	msg.WriteString("\r\n")

	// Closing boundary
	msg.WriteString(fmt.Sprintf("--%s--\r\n", boundary))

	return msg.Bytes()
}

// FormConfirmationData drives form_confirmation.html. It deliberately carries
// no submitted values beyond the submitter's name: the message is the form's
// configured confirmation/success text.
type FormConfirmationData struct {
	FormName string
	// Name is the submitter's name from a "name" field; empty when absent.
	Name     string
	Message  string
	SiteName string
}

// SendFormConfirmation emails a receipt to the person who submitted a form.
func (s *Service) SendFormConfirmation(ctx context.Context, to string, data FormConfirmationData) error {
	if to == "" {
		return nil
	}
	var htmlBuf bytes.Buffer
	if err := s.templates.ExecuteTemplate(&htmlBuf, "form_confirmation.html", data); err != nil {
		return fmt.Errorf("failed to render form confirmation template: %w", err)
	}

	var text strings.Builder
	if data.Name != "" {
		text.WriteString("Thank you, " + data.Name + ".\n\n")
	}
	text.WriteString(data.Message + "\n\n")
	text.WriteString("Your submission of \"" + data.FormName + "\" has been received.\n\n")
	text.WriteString(data.SiteName + "\n")

	subject := fmt.Sprintf("We received your submission — %s", data.FormName)
	if clean, ok := SanitizeHeader(subject); ok {
		subject = clean
	} else {
		subject = "We received your submission"
	}
	return s.Send(ctx, []string{to}, subject, htmlBuf.String(), text.String())
}
