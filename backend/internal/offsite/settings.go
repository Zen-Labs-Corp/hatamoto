// Package offsite keeps a verified copy of each scheduled backup in an
// S3-compatible bucket: the saved target and schedule (the secret key sealed
// with the server secret), the upload, its verification and the retention of
// the remote copies.
package offsite

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"hatamoto.dev/core/internal/s3"
	"hatamoto.dev/core/internal/secretbox"
)

// Services (presets). Only AWS derives the endpoint from the region; for the
// others the preset is help text and the endpoint field decides.
const (
	ServiceAWS    = "aws"
	ServiceR2     = "r2"
	ServiceB2     = "b2"
	ServiceMinIO  = "minio"
	ServiceCustom = "custom"
)

// Services lists the valid service values.
var Services = []string{ServiceAWS, ServiceR2, ServiceB2, ServiceMinIO, ServiceCustom}

// Field limits and defaults.
const (
	MaxPrefixLen  = 200
	MaxAccessKey  = 256
	MinKeep       = 1
	MaxKeep       = 365
	DefaultKeep   = 7
	DefaultHour   = 3
	MaxEndpoint   = 253
	secretDomain  = "hatamoto/backup/offsite-secret-key"
	secretAAD     = "offsite-settings:secret-key"
	awsEndpointFm = "https://s3.%s.amazonaws.com"
)

var (
	bucketPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)
	regionPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
)

// Result is the outcome of the latest upload.
type Result struct {
	At     time.Time
	OK     bool
	Size   int64
	SHA256 string
	Error  string // never contains the secret key
}

// Settings is the saved off-site configuration.
type Settings struct {
	Enabled      bool
	Service      string
	Endpoint     string // ignored for AWS
	Region       string
	Bucket       string
	Prefix       string
	AccessKey    string
	SecretSealed []byte // sealed secret key; nil when none is saved
	Schedule     Schedule
	Keep         int       // remote copies kept
	NextDueAt    time.Time // next scheduled run; zero when none
	Last         Result    // zero At: nothing uploaded yet
	UpdatedAt    time.Time // set by the database
}

// Defaults are the settings of a site that never configured off-site backups.
func Defaults() Settings {
	return Settings{Service: ServiceAWS, Schedule: Schedule{Kind: ScheduleOff, Hour: DefaultHour}, Keep: DefaultKeep}
}

// EffectiveEndpoint is the endpoint requests go to: derived from the region
// for AWS, the saved endpoint otherwise.
func (s Settings) EffectiveEndpoint() string {
	if s.Service == ServiceAWS {
		return fmt.Sprintf(awsEndpointFm, s.Region)
	}
	return s.Endpoint
}

// Validate checks every field against its allowed values. The secret key is
// required only while the target is enabled.
func (s Settings) Validate() error {
	if !contains(Services, s.Service) {
		return fmt.Errorf("unknown service %q", s.Service)
	}
	if !regionPattern.MatchString(s.Region) {
		return errors.New("region is required (lower-case letters, digits and dashes)")
	}
	if s.Service != ServiceAWS {
		if len(s.Endpoint) > MaxEndpoint {
			return errors.New("endpoint is too long")
		}
	}
	if _, err := s3.ValidateEndpoint(s.EffectiveEndpoint()); err != nil {
		return err
	}
	if !bucketPattern.MatchString(s.Bucket) || strings.Contains(s.Bucket, "..") {
		return errors.New("bucket must be 3 to 63 characters: lower-case letters, digits, dots and dashes")
	}
	if err := validatePrefix(s.Prefix); err != nil {
		return err
	}
	if s.AccessKey == "" || len(s.AccessKey) > MaxAccessKey || strings.ContainsAny(s.AccessKey, " \t\r\n") {
		return errors.New("access key is required")
	}
	if s.Enabled && !s.HasSecret() {
		return errors.New("secret key is required")
	}
	if err := s.Schedule.Validate(); err != nil {
		return err
	}
	if s.Keep < MinKeep || s.Keep > MaxKeep {
		return fmt.Errorf("keep must be between %d and %d", MinKeep, MaxKeep)
	}
	return nil
}

func validatePrefix(p string) error {
	if len(p) > MaxPrefixLen {
		return errors.New("prefix is too long")
	}
	if strings.HasPrefix(p, "/") || strings.Contains(p, "..") {
		return errors.New("prefix must not start with a slash or contain \"..\"")
	}
	for _, r := range p {
		if r < 0x20 || r == 0x7f {
			return errors.New("prefix contains a control character")
		}
	}
	return nil
}

// HasSecret reports whether a sealed secret key is stored.
func (s Settings) HasSecret() bool { return len(s.SecretSealed) > 0 }

// SealSecret returns the sealed form of a secret key for Settings.SecretSealed.
func SealSecret(key []byte, secret string) ([]byte, error) {
	return secretbox.Seal(key, []byte(secretAAD), []byte(secret))
}

// Secret opens the stored secret key. ok is false when none is saved or it
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

// SecretKey derives the key that seals the secret key from the server secret.
func SecretKey(serverSecret string) []byte { return secretbox.DeriveKey(serverSecret, secretDomain) }

// Querier is the part of a pgx pool the Store needs.
type Querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
