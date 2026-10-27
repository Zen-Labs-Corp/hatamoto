// Package mailsettings stores the SMTP settings an owner saves in the admin.
// The password is sealed with the server secret; a missing row means the
// server's SMTP_* environment applies.
package mailsettings

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"hatamoto.dev/core/internal/email"
	"hatamoto.dev/core/internal/secretbox"
)

// Providers (presets). The preset decides the host for SES and Gmail.
const (
	ProviderCustom = "custom"
	ProviderSES    = "ses"
	ProviderGmail  = "gmail"
)

// Providers lists the valid provider values.
var Providers = []string{ProviderCustom, ProviderSES, ProviderGmail}

// DefaultSESRegion is preselected for the Amazon SES preset.
const DefaultSESRegion = "us-east-1"

// SESRegions is the closed list of regions the SES preset offers.
var SESRegions = []string{
	"us-east-1", "us-east-2", "us-west-1", "us-west-2",
	"ca-central-1", "sa-east-1",
	"eu-west-1", "eu-west-2", "eu-west-3", "eu-central-1", "eu-north-1", "eu-south-1",
	"ap-south-1", "ap-northeast-1", "ap-northeast-2", "ap-northeast-3",
	"ap-southeast-1", "ap-southeast-2",
}

// GmailHost is the SMTP host of the Gmail / Google Workspace preset.
const GmailHost = "smtp.gmail.com"

// SESHost returns the SMTP endpoint for an Amazon SES region.
func SESHost(region string) string { return "email-smtp." + region + ".amazonaws.com" }

// Field length limits.
const (
	MaxHostLen     = 253
	MaxUsernameLen = 255
	MaxAddressLen  = 254
	MaxFromNameLen = 200
)

const (
	secretDomain = "hatamoto/email/smtp-password"
	secretAAD    = "smtp-settings:password"
)

// Settings is the saved SMTP configuration.
type Settings struct {
	Provider       string
	SESRegion      string // only for ProviderSES
	Host           string
	Port           int
	Security       email.Security
	Username       string
	PasswordSealed []byte // sealed password; nil when none is saved
	FromAddress    string
	FromName       string    // blank = follow the site name
	UpdatedAt      time.Time // set by the database
}

// IsLoopbackHost reports whether host is one of the three names Go's
// smtp.PlainAuth accepts without TLS: "localhost", "127.0.0.1" and "::1",
// compared exactly (callers lower-case the host first). Security "none" is
// only accepted for these, so a wider rule would pass validation and then fail
// at AUTH.
func IsLoopbackHost(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

// Validate checks every field against its allowed values.
func (s Settings) Validate() error {
	if !contains(Providers, s.Provider) {
		return fmt.Errorf("unknown provider %q", s.Provider)
	}
	switch s.Provider {
	case ProviderSES:
		if !contains(SESRegions, s.SESRegion) {
			return fmt.Errorf("unknown Amazon SES region %q", s.SESRegion)
		}
		if s.Host != SESHost(s.SESRegion) {
			return errors.New("host does not match the Amazon SES region")
		}
	case ProviderGmail:
		if s.Host != GmailHost {
			return errors.New("host does not match the Gmail preset")
		}
	}
	if s.Host == "" {
		return errors.New("host is required")
	}
	if len(s.Host) > MaxHostLen || strings.ContainsAny(s.Host, " \t\r\n/@") {
		return errors.New("host is not valid")
	}
	if s.Port < 1 || s.Port > 65535 {
		return errors.New("port must be between 1 and 65535")
	}
	switch s.Security {
	case email.SecurityStarttls, email.SecurityTLS:
	case email.SecurityNone:
		if !IsLoopbackHost(s.Host) {
			return errors.New("security None is only allowed for localhost, 127.0.0.1 or ::1")
		}
	default:
		return fmt.Errorf("unknown security %q", s.Security)
	}
	if len(s.Username) > MaxUsernameLen {
		return errors.New("username is too long")
	}
	if _, ok := email.SanitizeHeader(s.Username); !ok {
		return errors.New("username contains a line break")
	}
	if s.FromAddress == "" || !strings.Contains(s.FromAddress, "@") || len(s.FromAddress) > MaxAddressLen {
		return errors.New("from address is not valid")
	}
	if _, ok := email.SanitizeHeader(s.FromAddress); !ok {
		return errors.New("from address contains a line break")
	}
	if len(s.FromName) > MaxFromNameLen {
		return errors.New("from name is too long")
	}
	if _, ok := email.SanitizeHeader(s.FromName); !ok {
		return errors.New("from name contains a line break")
	}
	return nil
}

// EmailConfig builds the service configuration for these settings with the
// opened password. Saved settings may use host "localhost".
func (s Settings) EmailConfig(password string) email.Config {
	return email.Config{
		SMTPHost:         s.Host,
		SMTPPort:         s.Port,
		SMTPUsername:     s.Username,
		SMTPPassword:     password,
		FromAddress:      s.FromAddress,
		FromName:         s.FromName,
		FromNameFromSite: s.FromName == "",
		Security:         s.Security,
		AllowLocalhost:   true,
	}
}

// HasPassword reports whether a sealed password is stored.
func (s Settings) HasPassword() bool { return len(s.PasswordSealed) > 0 }

// SealPassword returns the sealed form of a password for Settings.PasswordSealed.
func SealPassword(key []byte, password string) ([]byte, error) {
	return secretbox.Seal(key, []byte(secretAAD), []byte(password))
}

// Password opens the stored password. ok is false when none is saved or it
// cannot be read (the server's secret changed); use HasPassword to tell the
// two apart.
func (s Settings) Password(key []byte) (password string, ok bool) {
	if len(s.PasswordSealed) == 0 {
		return "", false
	}
	b, err := secretbox.Open(key, []byte(secretAAD), s.PasswordSealed)
	if err != nil || len(b) == 0 {
		return "", false
	}
	return string(b), true
}

// PasswordKey derives the key that seals the password from the server secret.
func PasswordKey(serverSecret string) []byte { return secretbox.DeriveKey(serverSecret, secretDomain) }

// Querier is the part of a pgx pool the Store needs.
type Querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Store reads and writes the single hatamoto_smtp_settings row. It does not
// cache: it is read at boot, after each admin change and by the replica
// convergence check, never per request.
type Store struct{ db Querier }

// NewStore returns a Store over db (a *pgxpool.Pool fits).
func NewStore(db Querier) *Store { return &Store{db: db} }

// Get returns the saved settings; found is false when no row exists.
func (s *Store) Get(ctx context.Context) (set Settings, found bool, err error) {
	var security string
	err = s.db.QueryRow(ctx, `
		SELECT provider, ses_region, host, port, security, username,
		       password_sealed, from_address, from_name, updated_at
		FROM hatamoto_smtp_settings WHERE id`).Scan(
		&set.Provider, &set.SESRegion, &set.Host, &set.Port, &security, &set.Username,
		&set.PasswordSealed, &set.FromAddress, &set.FromName, &set.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Settings{}, false, nil
	}
	if err != nil {
		return Settings{}, false, fmt.Errorf("load smtp settings: %w", err)
	}
	set.Security = email.Security(security)
	return set, true, nil
}

// Save validates set and stores it as the single row.
func (s *Store) Save(ctx context.Context, set Settings) error {
	if err := set.Validate(); err != nil {
		return err
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO hatamoto_smtp_settings
			(id, provider, ses_region, host, port, security, username,
			 password_sealed, from_address, from_name, updated_at)
		VALUES (TRUE, $1, $2, $3, $4, $5, $6, $7, $8, $9, now())
		ON CONFLICT (id) DO UPDATE SET provider = EXCLUDED.provider,
			ses_region = EXCLUDED.ses_region, host = EXCLUDED.host, port = EXCLUDED.port,
			security = EXCLUDED.security, username = EXCLUDED.username,
			password_sealed = EXCLUDED.password_sealed, from_address = EXCLUDED.from_address,
			from_name = EXCLUDED.from_name, updated_at = now()`,
		set.Provider, set.SESRegion, set.Host, set.Port, string(set.Security), set.Username,
		set.PasswordSealed, set.FromAddress, set.FromName)
	if err != nil {
		return fmt.Errorf("save smtp settings: %w", err)
	}
	return nil
}

// Clear deletes the saved row so the server's environment applies again.
func (s *Store) Clear(ctx context.Context) error {
	if _, err := s.db.Exec(ctx, `DELETE FROM hatamoto_smtp_settings WHERE id`); err != nil {
		return fmt.Errorf("clear smtp settings: %w", err)
	}
	return nil
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
