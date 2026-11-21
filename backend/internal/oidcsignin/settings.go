package oidcsignin

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"hatamoto.dev/core/internal/secretbox"
	"hatamoto.dev/core/internal/user"
)

const (
	secretDomain = "hatamoto/signin/oidc-client-secret"
	secretAAD    = "signin-oidc:client-secret"
)

// Field limits.
const (
	MaxLabel    = 64
	MaxIssuer   = 512
	MaxClientID = 255
	MaxSecret   = 1024
	MaxDomains  = 50
)

// DefaultLabel is shown on the login button when the owner sets no label.
const DefaultLabel = "Single sign-on"

var domainRe = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// Settings is the saved OIDC sign-in configuration.
type Settings struct {
	Enabled        bool
	Label          string
	Issuer         string
	ClientID       string
	SecretSealed   []byte // sealed client secret; nil when none is saved
	AllowedDomains []string
	DefaultRole    string
	LinkExisting   bool
	UpdatedAt      time.Time // set by the database
}

// HasSecret reports whether a sealed client secret is stored.
func (s Settings) HasSecret() bool { return len(s.SecretSealed) > 0 }

// DisplayLabel is the label of the login button.
func (s Settings) DisplayLabel() string {
	if l := strings.TrimSpace(s.Label); l != "" {
		return l
	}
	return DefaultLabel
}

// SecretKey derives the key that seals the client secret from the server secret.
func SecretKey(serverSecret string) []byte { return secretbox.DeriveKey(serverSecret, secretDomain) }

// SealSecret returns the sealed form of a client secret for Settings.SecretSealed.
func SealSecret(key []byte, secret string) ([]byte, error) {
	return secretbox.Seal(key, []byte(secretAAD), []byte(secret))
}

// Secret opens the stored client secret. ok is false when none is saved or it
// cannot be read (the server's secret changed); HasSecret tells the two apart.
func (s Settings) Secret(key []byte) (secret string, ok bool) {
	if len(s.SecretSealed) == 0 {
		return "", false
	}
	b, err := secretbox.Open(key, []byte(secretAAD), s.SecretSealed)
	if err != nil || len(b) == 0 {
		return "", false
	}
	return string(b), true
}

func hasControl(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

func validDomain(d string) bool { return len(d) <= 253 && domainRe.MatchString(d) }

// ParseDomains splits raw on commas, spaces and newlines, trims, lower-cases,
// strips a leading "@", removes duplicates and checks each is a host name.
func ParseDomains(raw string) ([]string, error) {
	fields := strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || unicode.IsSpace(r) })
	out := make([]string, 0, len(fields))
	seen := map[string]bool{}
	for _, f := range fields {
		d := strings.TrimPrefix(strings.ToLower(f), "@")
		if !validDomain(d) {
			return nil, fmt.Errorf("%q is not a valid domain", f)
		}
		if seen[d] {
			continue
		}
		seen[d] = true
		out = append(out, d)
	}
	if len(out) > MaxDomains {
		return nil, fmt.Errorf("at most %d domains", MaxDomains)
	}
	return out, nil
}

// Validate checks the settings. Disabled settings may be incomplete.
func (s Settings) Validate() error {
	if utf8.RuneCountInString(s.Label) > MaxLabel || hasControl(s.Label) {
		return fmt.Errorf("label must be at most %d characters, without control characters", MaxLabel)
	}
	if s.Issuer != "" {
		if err := validIssuer(s.Issuer); err != nil {
			return err
		}
	}
	if len(s.ClientID) > MaxClientID || hasControl(s.ClientID) {
		return fmt.Errorf("client ID must be at most %d bytes, without control characters", MaxClientID)
	}
	if !user.ValidRole(user.Role(s.DefaultRole)) {
		return errors.New("default role is not valid")
	}
	if len(s.AllowedDomains) > MaxDomains {
		return fmt.Errorf("at most %d domains", MaxDomains)
	}
	for _, d := range s.AllowedDomains {
		if !validDomain(d) {
			return fmt.Errorf("%q is not a valid domain", d)
		}
	}
	if s.Enabled {
		switch {
		case s.Issuer == "":
			return errors.New("issuer is required")
		case s.ClientID == "":
			return errors.New("client ID is required")
		case !s.HasSecret():
			return errors.New("client secret is required")
		case len(s.AllowedDomains) == 0:
			return errors.New("at least one allowed domain is required")
		}
	}
	return nil
}

func validIssuer(iss string) error {
	if len(iss) > MaxIssuer {
		return fmt.Errorf("issuer must be at most %d bytes", MaxIssuer)
	}
	u, err := url.Parse(iss)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return errors.New("issuer must be an https URL without query, fragment or credentials")
	}
	return nil
}

// Querier is the part of a pgx pool the Store needs.
type Querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Store reads and writes the single hatamoto_signin_oidc row. It does not cache.
type Store struct{ db Querier }

// NewStore returns a Store over db (a *pgxpool.Pool fits).
func NewStore(db Querier) *Store { return &Store{db: db} }

// Get returns the saved settings; found is false when no row exists.
func (s *Store) Get(ctx context.Context) (set Settings, found bool, err error) {
	err = s.db.QueryRow(ctx, `
		SELECT enabled, label, issuer, client_id, client_secret_sealed,
		       allowed_domains, default_role, link_existing, updated_at
		FROM hatamoto_signin_oidc WHERE id`).Scan(
		&set.Enabled, &set.Label, &set.Issuer, &set.ClientID, &set.SecretSealed,
		&set.AllowedDomains, &set.DefaultRole, &set.LinkExisting, &set.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Settings{}, false, nil
	}
	if err != nil {
		return Settings{}, false, fmt.Errorf("load oidc sign-in settings: %w", err)
	}
	return set, true, nil
}

// Save validates set and stores it as the single row.
func (s *Store) Save(ctx context.Context, set Settings) error {
	if err := set.Validate(); err != nil {
		return err
	}
	domains := set.AllowedDomains
	if domains == nil {
		domains = []string{}
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO hatamoto_signin_oidc
			(id, enabled, label, issuer, client_id, client_secret_sealed,
			 allowed_domains, default_role, link_existing, updated_at)
		VALUES (TRUE, $1, $2, $3, $4, $5, $6, $7, $8, now())
		ON CONFLICT (id) DO UPDATE SET enabled = EXCLUDED.enabled, label = EXCLUDED.label,
			issuer = EXCLUDED.issuer, client_id = EXCLUDED.client_id,
			client_secret_sealed = EXCLUDED.client_secret_sealed,
			allowed_domains = EXCLUDED.allowed_domains, default_role = EXCLUDED.default_role,
			link_existing = EXCLUDED.link_existing, updated_at = now()`,
		set.Enabled, set.Label, set.Issuer, set.ClientID, set.SecretSealed,
		domains, set.DefaultRole, set.LinkExisting)
	if err != nil {
		return fmt.Errorf("save oidc sign-in settings: %w", err)
	}
	return nil
}
